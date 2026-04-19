package config

import (
	"errors"
	"os"
	"strings"

	"charm.land/log/v2"
	"gopkg.in/yaml.v3"
)

type Config struct {
	LogLevel       string    `yaml:"log_level"`
	LogLevelParsed log.Level `yaml:"-"`

	DB struct {
		Path string `yaml:"path"`
	} `yaml:"db"`

	SSH struct {
		ListenAddr         string `yaml:"listen_addr"`
		HostKeyPath        string `yaml:"host_key_path"`
		PortalPasswordHash string `yaml:"portal_password_hash"`
		AuthorizedKeysPath string `yaml:"authorized_keys_path"`
	} `yaml:"ssh"`

	OpenRouter struct {
		APIKey  string `yaml:"api_key"`
		BaseURL string `yaml:"base_url"`
		Model   string `yaml:"model"`

		// Provider routing preferences passed through to OpenRouter.
		// See: https://openrouter.ai/docs/guides/routing/provider-selection
		Provider struct {
			AllowFallbacks *bool    `yaml:"allow_fallbacks,omitempty"`
			Ignore         []string `yaml:"ignore,omitempty"`
			Only           []string `yaml:"only,omitempty"`
			Order          []string `yaml:"order,omitempty"`
		} `yaml:"provider"`
	} `yaml:"openrouter"`

	Secrets struct {
		MasterKey string `yaml:"master_key"`
		TTLHours  int    `yaml:"ttl_hours"`
	} `yaml:"secrets"`

	Signal struct {
		Enabled       bool   `yaml:"enabled"`
		AccountNumber string `yaml:"account_number"`
		SignalCLIPath string `yaml:"signal_cli_path"`
		HTTPAddr      string `yaml:"http_addr"`
	} `yaml:"signal"`

	Discord struct {
		Enabled  bool   `yaml:"enabled"`
		BotToken string `yaml:"bot_token"`
	} `yaml:"discord"`

	// MCP configures external Model Context Protocol servers.
	//
	// Server definitions are global, but per-user enablement is stored separately
	// (see user_settings key: mcp.enabled_servers).
	MCP struct {
		// If nil, defaults to true when servers are configured, otherwise false.
		Enabled *bool       `yaml:"enabled,omitempty"`
		Servers []MCPServer `yaml:"servers"`
	} `yaml:"mcp"`

	Paths struct {
		DataDir string `yaml:"data_dir"`
	} `yaml:"paths"`
}

type MCPServer struct {
	Name string `yaml:"name"`
	// Transport currently supports only: "stdio".
	Transport string `yaml:"transport"`
	// Command is the executable to run for stdio transport.
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`

	// Trusted controls how much we rely on MCP tool annotations for safety.
	// Untrusted servers default to requiring confirmation for all tool calls.
	Trusted bool `yaml:"trusted"`

	// DefaultEnabledForAllUsers enables this server for users that have not
	// explicitly configured their enabled server list yet.
	DefaultEnabledForAllUsers bool `yaml:"default_enabled_for_all_users"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}

	// Defaults
	if strings.TrimSpace(cfg.LogLevel) == "" {
		cfg.LogLevel = "info"
	}
	lvl, err := log.ParseLevel(cfg.LogLevel)
	if err != nil {
		return nil, err
	}
	cfg.LogLevelParsed = lvl
	if cfg.DB.Path == "" {
		cfg.DB.Path = "./data/tether.sqlite"
	}
	if cfg.SSH.ListenAddr == "" {
		cfg.SSH.ListenAddr = ":2222"
	}
	if cfg.SSH.HostKeyPath == "" {
		cfg.SSH.HostKeyPath = "./config/ssh_host_ed25519"
	}
	if cfg.Paths.DataDir == "" {
		cfg.Paths.DataDir = "./data"
	}

	adminEnv, err := LoadAdminEnv(cfg.Paths.DataDir)
	if err != nil {
		return nil, err
	}

	if cfg.SSH.PortalPasswordHash == "" {
		return nil, errors.New("ssh.portal_password_hash is required")
	}
	if cfg.SSH.AuthorizedKeysPath == "" {
		cfg.SSH.AuthorizedKeysPath = "./config/authorized_keys"
	}

	// OpenRouter settings
	if strings.TrimSpace(cfg.OpenRouter.APIKey) == "" {
		if adminEnv.OpenRouterAPIKey != "" {
			cfg.OpenRouter.APIKey = adminEnv.OpenRouterAPIKey
		} else {
			cfg.OpenRouter.APIKey = os.Getenv("OPENROUTER_API_KEY")
		}
	}
	if cfg.OpenRouter.BaseURL == "" {
		cfg.OpenRouter.BaseURL = "https://openrouter.ai/api/v1"
	}
	if cfg.OpenRouter.Model == "" {
		cfg.OpenRouter.Model = "z-ai/glm-5.1"
	}

	// OpenRouter provider routing defaults.
	//
	// We default to ignoring Morph because it has been observed to be unreliable
	// for agent workloads (e.g. 503 no_available_workers). Users can override by
	// explicitly setting openrouter.provider.ignore: [] in their config.
	if cfg.OpenRouter.Provider.Ignore == nil {
		cfg.OpenRouter.Provider.Ignore = []string{"morph"}
	}

	// Secrets
	if strings.TrimSpace(cfg.Secrets.MasterKey) == "" {
		if adminEnv.MasterKey != "" {
			cfg.Secrets.MasterKey = adminEnv.MasterKey
		} else {
			cfg.Secrets.MasterKey = os.Getenv("TETHER_MASTER_KEY")
		}
	}
	if cfg.Secrets.TTLHours == 0 {
		cfg.Secrets.TTLHours = 24
	}

	// Signal
	if cfg.Signal.SignalCLIPath == "" {
		cfg.Signal.SignalCLIPath = "signal-cli"
	}
	if strings.TrimSpace(cfg.Signal.AccountNumber) == "" {
		if adminEnv.SignalNumber != "" {
			cfg.Signal.AccountNumber = adminEnv.SignalNumber
		} else {
			cfg.Signal.AccountNumber = os.Getenv("TETHER_SIGNAL_NUMBER")
		}
	}
	if cfg.Signal.HTTPAddr == "" {
		cfg.Signal.HTTPAddr = "127.0.0.1:17800"
	}

	// Discord
	if strings.TrimSpace(cfg.Discord.BotToken) == "" {
		if adminEnv.DiscordBotToken != "" {
			cfg.Discord.BotToken = adminEnv.DiscordBotToken
		} else {
			cfg.Discord.BotToken = os.Getenv("TETHER_DISCORD_BOT_TOKEN")
		}
	}

	// MCP
	if cfg.MCP.Enabled == nil {
		en := false
		if len(cfg.MCP.Servers) > 0 {
			en = true
		}
		cfg.MCP.Enabled = &en
	}
	for i := range cfg.MCP.Servers {
		cfg.MCP.Servers[i].Name = strings.TrimSpace(cfg.MCP.Servers[i].Name)
		cfg.MCP.Servers[i].Transport = strings.TrimSpace(cfg.MCP.Servers[i].Transport)
		cfg.MCP.Servers[i].Command = strings.TrimSpace(cfg.MCP.Servers[i].Command)
		if cfg.MCP.Servers[i].Transport == "" {
			cfg.MCP.Servers[i].Transport = "stdio"
		}
		if cfg.MCP.Servers[i].Env == nil {
			cfg.MCP.Servers[i].Env = map[string]string{}
		}
	}

	return &cfg, nil
}

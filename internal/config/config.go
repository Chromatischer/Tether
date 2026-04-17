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

	Paths struct {
		DataDir string `yaml:"data_dir"`
	} `yaml:"paths"`
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

	return &cfg, nil
}

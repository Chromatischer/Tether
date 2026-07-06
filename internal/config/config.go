package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/log/v2"
	"gopkg.in/yaml.v3"
)

// Agent runtime defaults. These apply when the corresponding config/admin
// values are unset.
const (
	DefaultAgentMaxToolCalls       = 100
	DefaultAgentTurnTimeoutSeconds = 180
	DefaultAgentTemperature        = 0.2
	DefaultAgentReasoningEffort    = "medium"
)

// ReasoningEffortAuto turns on adaptive reasoning: reasoning is enabled but the
// model decides how much to spend instead of a fixed low/medium/high effort.
const ReasoningEffortAuto = "auto"

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

	LLM struct {
		// Provider supports "openrouter" and "deepseek".
		Provider string `yaml:"provider"`
	} `yaml:"llm"`

	OpenRouter struct {
		APIKey  string `yaml:"api_key"`
		BaseURL string `yaml:"base_url"`
		Model   string `yaml:"model"`

		// SecondaryModel is a cheaper/faster model used for simpler tasks
		// (e.g. fetch.summarize). When empty it falls back to Model.
		SecondaryModel string `yaml:"secondary_model"`

		// Provider routing preferences passed through to OpenRouter.
		// See OpenRouter provider routing documentation.
		Provider struct {
			AllowFallbacks *bool    `yaml:"allow_fallbacks,omitempty"`
			Ignore         []string `yaml:"ignore,omitempty"`
			Only           []string `yaml:"only,omitempty"`
			Order          []string `yaml:"order,omitempty"`
		} `yaml:"provider"`
	} `yaml:"openrouter"`

	DeepSeek struct {
		APIKey  string `yaml:"api_key"`
		BaseURL string `yaml:"base_url"`
		Model   string `yaml:"model"`

		// SecondaryModel is a cheaper/faster model used for simpler tasks.
		// When empty it falls back to Model.
		SecondaryModel string `yaml:"secondary_model"`
	} `yaml:"deepseek"`

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

		// Attachments controls ingestion of inbound DM attachments (images,
		// files, voice notes). Files are saved under the user's sandbox at
		// discord/context/<id>.<ext> and referenced inline in the prompt.
		Attachments struct {
			// Enabled toggles attachment ingestion. Default true.
			Enabled *bool `yaml:"enabled,omitempty"`
			// MaxSizeMB caps the size of a single downloaded attachment. Default 25.
			MaxSizeMB int `yaml:"max_size_mb,omitempty"`
			// MaxPerMessage caps how many attachments are ingested per message. Default 10.
			MaxPerMessage int `yaml:"max_per_message,omitempty"`
			// RetentionDays bounds how long context files are kept before the
			// prune sweep deletes them. Default 14. Zero disables pruning.
			RetentionDays int `yaml:"retention_days,omitempty"`
		} `yaml:"attachments"`
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

	// Agent tunes the per-turn tool-calling loop. All fields are optional and
	// admin-editable at runtime via the admin "agent" tab; zero/unset values
	// fall back to the Default* constants.
	Agent struct {
		// MaxToolCalls is the hard cap on tool calls in a single turn before the
		// loop stops and returns to the user. Default 100.
		MaxToolCalls int `yaml:"max_tool_calls,omitempty"`
		// TurnTimeoutSeconds bounds the wall-clock time for one turn. Default 180.
		TurnTimeoutSeconds int `yaml:"turn_timeout_seconds,omitempty"`
		// Temperature is the sampling temperature for the main loop. Pointer so 0
		// is distinguishable from unset. Default 0.2.
		Temperature *float64 `yaml:"temperature,omitempty"`
		// ReasoningEffort is the model reasoning effort (low|medium|high|auto).
		// "auto" enables adaptive reasoning. Default medium.
		ReasoningEffort string `yaml:"reasoning_effort,omitempty"`
	} `yaml:"agent"`

	// Web tunes outbound HTTP behavior for tools like web-fetch.
	Web struct {
		// AllowPrivateNetwork permits web-fetch to reach private, loopback, and
		// link-local addresses. Default false (SSRF protection). Admin-editable.
		AllowPrivateNetwork bool `yaml:"allow_private_network,omitempty"`
	} `yaml:"web"`

	// HostExec gates the non-sandboxed (host) bash tool. Default off; even when
	// enabled, every host command still requires an explicit per-call /confirm
	// approval and a stated reason. Admin-editable.
	HostExec struct {
		Enabled bool `yaml:"enabled,omitempty"`
	} `yaml:"host_exec"`

	Paths struct {
		DataDir string `yaml:"data_dir"`
	} `yaml:"paths"`
}

// AgentMaxToolCalls returns the per-turn tool-call cap, or the default.
func (c *Config) AgentMaxToolCalls() int {
	if c.Agent.MaxToolCalls > 0 {
		return c.Agent.MaxToolCalls
	}
	return DefaultAgentMaxToolCalls
}

// AgentTurnTimeout returns the per-turn wall-clock budget, or the default.
func (c *Config) AgentTurnTimeout() time.Duration {
	secs := c.Agent.TurnTimeoutSeconds
	if secs <= 0 {
		secs = DefaultAgentTurnTimeoutSeconds
	}
	return time.Duration(secs) * time.Second
}

// AgentTemperature returns the main-loop sampling temperature, or the default.
func (c *Config) AgentTemperature() float64 {
	if c.Agent.Temperature != nil {
		return *c.Agent.Temperature
	}
	return DefaultAgentTemperature
}

// AgentReasoningEffort returns the model reasoning effort, or the default.
func (c *Config) AgentReasoningEffort() string {
	if e := strings.TrimSpace(c.Agent.ReasoningEffort); e != "" {
		return e
	}
	return DefaultAgentReasoningEffort
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

	// LLM settings
	cfg.LLM.Provider = strings.ToLower(strings.TrimSpace(cfg.LLM.Provider))
	if cfg.LLM.Provider == "" {
		cfg.LLM.Provider = strings.ToLower(strings.TrimSpace(os.Getenv("TETHER_LLM_PROVIDER")))
	}
	if cfg.LLM.Provider == "" {
		cfg.LLM.Provider = "openrouter"
	}
	switch cfg.LLM.Provider {
	case "openrouter", "deepseek":
	default:
		return nil, errors.New("llm.provider must be openrouter or deepseek")
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
	if adminEnv.OpenRouterModel != "" {
		cfg.OpenRouter.Model = adminEnv.OpenRouterModel
	}
	if adminEnv.OpenRouterSecondaryModel != "" {
		cfg.OpenRouter.SecondaryModel = adminEnv.OpenRouterSecondaryModel
	}

	// DeepSeek settings
	if strings.TrimSpace(cfg.DeepSeek.APIKey) == "" {
		cfg.DeepSeek.APIKey = os.Getenv("DEEPSEEK_API_KEY")
	}
	if cfg.DeepSeek.BaseURL == "" {
		cfg.DeepSeek.BaseURL = "https://api.deepseek.com"
	}
	if cfg.DeepSeek.Model == "" {
		cfg.DeepSeek.Model = "deepseek-v4-flash"
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
	if adminEnv.DiscordEnabled != nil {
		cfg.Discord.Enabled = *adminEnv.DiscordEnabled
	}
	if cfg.Discord.Attachments.Enabled == nil {
		on := true
		cfg.Discord.Attachments.Enabled = &on
	}
	if cfg.Discord.Attachments.MaxSizeMB <= 0 {
		cfg.Discord.Attachments.MaxSizeMB = 25
	}
	if cfg.Discord.Attachments.MaxPerMessage <= 0 {
		cfg.Discord.Attachments.MaxPerMessage = 10
	}
	if cfg.Discord.Attachments.RetentionDays == 0 {
		cfg.Discord.Attachments.RetentionDays = 14
	}

	// Agent runtime tuning (admin-editable overrides layered over config/defaults).
	if n, err := strconv.Atoi(adminEnv.AgentMaxToolCalls); err == nil && n > 0 {
		cfg.Agent.MaxToolCalls = n
	}
	if n, err := strconv.Atoi(adminEnv.AgentTurnTimeoutSeconds); err == nil && n > 0 {
		cfg.Agent.TurnTimeoutSeconds = n
	}
	if f, err := strconv.ParseFloat(adminEnv.AgentTemperature, 64); err == nil && f >= 0 {
		cfg.Agent.Temperature = &f
	}
	if e := strings.TrimSpace(adminEnv.AgentReasoningEffort); e != "" {
		cfg.Agent.ReasoningEffort = e
	}
	if v := strings.TrimSpace(adminEnv.WebAllowPrivateNetwork); v != "" {
		cfg.Web.AllowPrivateNetwork = strings.EqualFold(v, "true")
	}
	if v := strings.TrimSpace(adminEnv.HostExecEnabled); v != "" {
		cfg.HostExec.Enabled = strings.EqualFold(v, "true")
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

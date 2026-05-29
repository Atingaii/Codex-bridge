package config

type Config struct {
	App           AppConfig           `yaml:"app"`
	Gateway       GatewayConfig       `yaml:"gateway"`
	Hub           HubConfig           `yaml:"hub"`
	Bridge        BridgeConfig        `yaml:"bridge"`
	Auth          AuthConfig          `yaml:"auth"`
	Observability ObservabilityConfig `yaml:"observability"`
}

type AppConfig struct {
	Name  string `yaml:"name"`
	Env   string `yaml:"env"`
	Debug bool   `yaml:"debug"`
}

type GatewayConfig struct {
	Host         string   `yaml:"host"`
	Port         int      `yaml:"port"`
	ReadTimeout  Duration `yaml:"read_timeout"`
	WriteTimeout Duration `yaml:"write_timeout"`
}

type HubConfig struct {
	DBPath                   string   `yaml:"db_path"`
	CookieSecure             bool     `yaml:"cookie_secure"`
	HeartbeatInterval        Duration `yaml:"heartbeat_interval"`
	BridgeReadTimeout        Duration `yaml:"bridge_read_timeout"`
	BrowserCloseSession      bool     `yaml:"browser_close_session"`
	BrowserCloseGrace        Duration `yaml:"browser_close_grace"`
	AllowedOrigins           []string `yaml:"allowed_origins"`
	BridgeDownloadURL        string   `yaml:"bridge_download_url"`
	MaxBridgeSendQueue       int      `yaml:"max_bridge_send_queue"`
	MaxBrowserSendQueue      int      `yaml:"max_browser_send_queue"`
	MaxPromptBytes           int64    `yaml:"max_prompt_bytes"`
	MaxAttachmentBytes       int64    `yaml:"max_attachment_bytes"`
	MaxAssistantMessageBytes int64    `yaml:"max_assistant_message_bytes"`
}

type BridgeConfig struct {
	HubURL              string                    `yaml:"hub_url"`
	Token               string                    `yaml:"token"`
	TokenFile           string                    `yaml:"token_file"`
	Name                string                    `yaml:"name"`
	MachineIDFile       string                    `yaml:"machine_id_file"`
	CWD                 string                    `yaml:"cwd"`
	Runner              string                    `yaml:"runner"`
	OrchestrationRunner string                    `yaml:"orchestration_runner"`
	CodexPath           string                    `yaml:"codex_path"`
	ClaudePath          string                    `yaml:"claude_path"`
	CCBPath             string                    `yaml:"ccb_path"`
	CCBTarget           string                    `yaml:"ccb_target"`
	CCBTimeout          Duration                  `yaml:"ccb_timeout"`
	ClaudeModel         string                    `yaml:"claude_model"`
	ClaudeEffort        string                    `yaml:"claude_effort"`
	Model               string                    `yaml:"model"`
	Sandbox             string                    `yaml:"sandbox"`
	ApprovalPolicy      string                    `yaml:"approval_policy"`
	ReconnectMin        Duration                  `yaml:"reconnect_min"`
	ReconnectMax        Duration                  `yaml:"reconnect_max"`
	HeartbeatInterval   Duration                  `yaml:"heartbeat_interval"`
	MaxSessions         int                       `yaml:"max_sessions"`
	LongCommandObserver LongCommandObserverConfig `yaml:"long_command_observer"`
}

type LongCommandObserverConfig struct {
	Enabled         bool     `yaml:"enabled"`
	After           Duration `yaml:"after"`
	CommandPatterns []string `yaml:"command_patterns"`
	AppliesTo       []string `yaml:"applies_to"`
}

type AuthConfig struct {
	JWTSecret         string   `yaml:"jwt_secret"`
	AccessTokenTTL    Duration `yaml:"access_token_ttl"`
	BootstrapUsername string   `yaml:"bootstrap_username"`
	BootstrapPassword string   `yaml:"bootstrap_password"`
}

type ObservabilityConfig struct {
	LogLevel  string `yaml:"log_level"`
	LogFormat string `yaml:"log_format"`
}

func Default() Config {
	return Config{
		App: AppConfig{
			Name:  "codex-bridge",
			Env:   "dev",
			Debug: true,
		},
		Gateway: GatewayConfig{
			Host:         "127.0.0.1",
			Port:         8088,
			ReadTimeout:  Duration{Duration: 10_000_000_000},
			WriteTimeout: Duration{Duration: 0},
		},
		Hub: HubConfig{
			DBPath:                   "data/codex-bridge.db",
			CookieSecure:             false,
			HeartbeatInterval:        Duration{Duration: 15_000_000_000},
			BridgeReadTimeout:        Duration{Duration: 45_000_000_000},
			BrowserCloseSession:      false,
			BrowserCloseGrace:        Duration{Duration: 1500_000_000},
			BridgeDownloadURL:        "",
			MaxBridgeSendQueue:       128,
			MaxBrowserSendQueue:      128,
			MaxPromptBytes:           256 * 1024,
			MaxAttachmentBytes:       8 * 1024 * 1024,
			MaxAssistantMessageBytes: 4 * 1024 * 1024,
		},
		Bridge: BridgeConfig{
			HubURL:              "https://sparkapi.tech",
			Name:                "local-codex",
			MachineIDFile:       "~/.codex-bridge/machine_id",
			CWD:                 ".",
			Runner:              "echo",
			OrchestrationRunner: "",
			CodexPath:           "codex",
			ClaudePath:          "claude",
			CCBPath:             "ccb",
			CCBTarget:           "codex",
			CCBTimeout:          Duration{Duration: 60 * 60 * 1_000_000_000},
			ClaudeModel:         "",
			ClaudeEffort:        "",
			Sandbox:             "workspace-write",
			ApprovalPolicy:      "never",
			ReconnectMin:        Duration{Duration: 5_000_000_000},
			ReconnectMax:        Duration{Duration: 30_000_000_000},
			HeartbeatInterval:   Duration{Duration: 15_000_000_000},
			MaxSessions:         8,
			LongCommandObserver: LongCommandObserverConfig{
				Enabled:         false,
				After:           Duration{Duration: 2 * 60 * 1_000_000_000},
				CommandPatterns: []string{"python -m slow_build"},
				AppliesTo:       []string{"claude", "codex"},
			},
		},
		Auth: AuthConfig{
			JWTSecret:         "dev-only-change-me-32-byte-minimum-secret",
			AccessTokenTTL:    Duration{Duration: 24 * 60 * 60 * 1_000_000_000},
			BootstrapUsername: "admin",
		},
		Observability: ObservabilityConfig{
			LogLevel:  "info",
			LogFormat: "console",
		},
	}
}

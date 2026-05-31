package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

func Load(configDir string) (*Config, error) {
	if v := os.Getenv("CODEX_BRIDGE_CONFIG_DIR"); v != "" {
		configDir = v
	}
	cfg := Default()

	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "dev"
	}
	cfg.App.Env = env

	path := filepath.Join(configDir, env+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
		example := path + ".example"
		data, err = os.ReadFile(example)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read config %s: %w", example, err)
		}
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal config: %w", err)
		}
	}

	applyEnv(&cfg)
	return &cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("APP_ENV"); v != "" {
		cfg.App.Env = v
	}
	if v := os.Getenv("APP_DEBUG"); v != "" {
		cfg.App.Debug = parseBool(v, cfg.App.Debug)
	}
	if v := os.Getenv("APP_HOST"); v != "" {
		cfg.Gateway.Host = v
	}
	if v := os.Getenv("APP_PORT"); v != "" {
		cfg.Gateway.Port = parseInt(v, cfg.Gateway.Port)
	}
	if v := os.Getenv("HUB_DB_PATH"); v != "" {
		cfg.Hub.DBPath = v
	}
	if v := os.Getenv("HUB_COOKIE_SECURE"); v != "" {
		cfg.Hub.CookieSecure = parseBool(v, cfg.Hub.CookieSecure)
	}
	if v := os.Getenv("HUB_BRIDGE_DOWNLOAD_URL"); v != "" {
		cfg.Hub.BridgeDownloadURL = v
	}
	if v := os.Getenv("JWT_SECRET"); v != "" {
		cfg.Auth.JWTSecret = v
	}
	if v := os.Getenv("HUB_USERNAME"); v != "" {
		cfg.Auth.BootstrapUsername = v
	}
	if v := os.Getenv("HUB_PASSWORD"); v != "" {
		cfg.Auth.BootstrapPassword = v
	}
	if v := os.Getenv("BRIDGE_HUB_URL"); v != "" {
		cfg.Bridge.HubURL = v
	}
	if v := os.Getenv("BRIDGE_TOKEN"); v != "" {
		cfg.Bridge.Token = v
	}
	if v := os.Getenv("BRIDGE_TOKEN_FILE"); v != "" {
		cfg.Bridge.TokenFile = v
	}
	if v := os.Getenv("BRIDGE_NAME"); v != "" {
		cfg.Bridge.Name = v
	}
	if v := os.Getenv("BRIDGE_MACHINE_ID_FILE"); v != "" {
		cfg.Bridge.MachineIDFile = v
	}
	if v := os.Getenv("BRIDGE_CWD"); v != "" {
		cfg.Bridge.CWD = v
	}
	if v := os.Getenv("BRIDGE_RUNNER"); v != "" {
		cfg.Bridge.Runner = v
	}
	if v := os.Getenv("BRIDGE_CODEX_PATH"); v != "" {
		cfg.Bridge.CodexPath = v
	}
	if v := os.Getenv("BRIDGE_CLAUDE_PATH"); v != "" {
		cfg.Bridge.ClaudePath = v
	}
	if v := os.Getenv("BRIDGE_CLAUDE_MODEL"); v != "" {
		cfg.Bridge.ClaudeModel = v
	}
	if v := os.Getenv("BRIDGE_CLAUDE_EFFORT"); v != "" {
		cfg.Bridge.ClaudeEffort = v
	}
	if v := os.Getenv("BRIDGE_MODEL"); v != "" {
		cfg.Bridge.Model = v
	}
	if v := os.Getenv("BRIDGE_SANDBOX"); v != "" {
		cfg.Bridge.Sandbox = v
	}
	if v := os.Getenv("BRIDGE_APPROVAL_POLICY"); v != "" {
		cfg.Bridge.ApprovalPolicy = v
	}
	if v := os.Getenv("BRIDGE_LONG_COMMAND_OBSERVER_ENABLED"); v != "" {
		cfg.Bridge.LongCommandObserver.Enabled = parseBool(v, cfg.Bridge.LongCommandObserver.Enabled)
	}
	if v := os.Getenv("BRIDGE_LONG_COMMAND_OBSERVER_AFTER"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Bridge.LongCommandObserver.After = Duration{Duration: d}
		}
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.Observability.LogLevel = v
	}
	if v := os.Getenv("LOG_FORMAT"); v != "" {
		cfg.Observability.LogFormat = v
	}
}

func parseInt(value string, fallback int) int {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func parseBool(value string, fallback bool) bool {
	v, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return v
}

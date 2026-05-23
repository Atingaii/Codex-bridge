package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/bridge"
	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/hub"
	"github.com/tencent/codex-bridge/internal/logger"
	"github.com/tencent/codex-bridge/internal/store"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "[fatal] %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "hub"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}

	cfg, err := config.Load("configs")
	if err != nil {
		return err
	}
	if err := logger.Init(cfg.Observability.LogLevel, cfg.Observability.LogFormat); err != nil {
		return err
	}
	defer logger.Sync()

	switch cmd {
	case "hub":
		return runHub(cfg)
	case "bridge":
		return bridge.NewClient(cfg, Version).Run(context.Background())
	case "connect":
		return runConnect(cfg, args)
	case "user":
		return runUser(cfg, args)
	case "enroll":
		return runEnroll(cfg, args)
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		printUsage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func runConnect(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	hubURL := fs.String("hub", cfg.Bridge.HubURL, "hub URL")
	name := fs.String("name", "", "CLI endpoint name")
	cwd := fs.String("cwd", "", "workspace directory")
	runner := fs.String("runner", "codex", "runner: codex, claude, echo")
	machineIDFile := fs.String("machine-id-file", cfg.Bridge.MachineIDFile, "machine id file")
	sandbox := fs.String("sandbox", cfg.Bridge.Sandbox, "runner sandbox")
	approvalPolicy := fs.String("approval-policy", cfg.Bridge.ApprovalPolicy, "runner approval policy")
	token, flagArgs, err := normalizeConnectArgs(args)
	if err != nil {
		return err
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		if token != "" || fs.NArg() > 1 {
			return errors.New("connect requires exactly one enroll token")
		}
		token = fs.Arg(0)
	}
	if token == "" {
		return errors.New("connect requires exactly one enroll token")
	}
	if *hubURL == "" {
		*hubURL = "https://sparkapi.tech"
	}
	if *cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			*cwd = wd
		} else {
			*cwd = "."
		}
	}
	if *name == "" {
		host, _ := os.Hostname()
		base := filepath.Base(*cwd)
		if host != "" && base != "" && base != "." && base != string(filepath.Separator) {
			*name = host + "-" + base
		} else if host != "" {
			*name = host
		} else {
			*name = "codex-cli"
		}
	}
	cfg.Bridge.HubURL = *hubURL
	cfg.Bridge.Token = token
	cfg.Bridge.TokenFile = ""
	cfg.Bridge.Name = *name
	cfg.Bridge.CWD = *cwd
	cfg.Bridge.Runner = *runner
	cfg.Bridge.MachineIDFile = *machineIDFile
	cfg.Bridge.Sandbox = *sandbox
	cfg.Bridge.ApprovalPolicy = *approvalPolicy
	return bridge.NewClient(cfg, Version).Run(context.Background())
}

func normalizeConnectArgs(args []string) (string, []string, error) {
	token := ""
	flagArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "" {
			continue
		}
		if arg == "--" {
			if len(args)-i != 2 || token != "" {
				return "", nil, errors.New("connect requires exactly one enroll token")
			}
			token = args[i+1]
			break
		}
		if strings.HasPrefix(arg, "-") {
			flagArgs = append(flagArgs, arg)
			if strings.Contains(arg, "=") || isBoolConnectFlag(arg) {
				continue
			}
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("flag needs an argument: %s", arg)
			}
			i++
			flagArgs = append(flagArgs, args[i])
			continue
		}
		if token != "" {
			return "", nil, errors.New("connect requires exactly one enroll token")
		}
		token = arg
	}
	return token, flagArgs, nil
}

func isBoolConnectFlag(arg string) bool {
	return arg == "-h" || arg == "--help"
}

func runHub(cfg *config.Config) error {
	if len(cfg.Auth.JWTSecret) < 32 {
		return errors.New("auth.jwt_secret must be at least 32 bytes")
	}
	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		return err
	}

	srv := hub.NewServer(cfg, st, hub.BuildInfo{Version: Version, BuildTime: BuildTime})
	return srv.Run(context.Background())
}

func runUser(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("user", flag.ContinueOnError)
	username := fs.String("username", cfg.Auth.BootstrapUsername, "login username")
	password := fs.String("password", cfg.Auth.BootstrapPassword, "login password")
	if err := fs.Parse(args); err != nil {
		return err
	}
	userName := strings.TrimSpace(*username)
	passwordValue := *password
	if userName == "" || passwordValue == "" {
		return errors.New("user requires --username and --password")
	}
	if isQuotedEmptyPassword(passwordValue) {
		return errors.New("user --password looks like a quoted empty config value; provide the actual password")
	}

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		return err
	}
	user, err := st.UpsertUser(context.Background(), userName, passwordValue)
	if err != nil {
		return err
	}
	fmt.Printf("user ready: %s (%s)\n", user.Username, user.ID)
	return nil
}

func isQuotedEmptyPassword(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == `""` || trimmed == `''`
}

func runEnroll(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("enroll", flag.ContinueOnError)
	ttl := fs.Duration("ttl", 24*time.Hour, "token time-to-live")
	token := fs.String("token", "", "token value, generated when empty")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		return err
	}
	value := *token
	if value == "" {
		value = store.NewToken("enr")
	}
	expiresAt := time.Now().Add(*ttl)
	if err := st.CreateEnrollToken(context.Background(), value, &expiresAt); err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func printUsage() {
	fmt.Println(`codex-bridge

Usage:
  codex-bridge hub                 Run the public Hub server
  codex-bridge bridge              Run the reverse-connecting Bridge
  codex-bridge connect <token>     Connect this CLI endpoint to https://sparkapi.tech
  codex-bridge user --username u --password p
  codex-bridge enroll [--ttl 24h]

Configuration is loaded from configs/${APP_ENV:-dev}.yaml, then environment variables.`)
}

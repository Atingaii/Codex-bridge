package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
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
	if *username == "" || *password == "" {
		return errors.New("user requires --username and --password")
	}

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		return err
	}
	user, err := st.UpsertUser(context.Background(), *username, *password)
	if err != nil {
		return err
	}
	fmt.Printf("user ready: %s (%s)\n", user.Username, user.ID)
	return nil
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
  codex-bridge user --username u --password p
  codex-bridge enroll [--ttl 24h]

Configuration is loaded from configs/${APP_ENV:-dev}.yaml, then environment variables.`)
}

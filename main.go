package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alevsk/nats-tui/internal/config"
	"github.com/alevsk/nats-tui/internal/monitor"
	"github.com/alevsk/nats-tui/internal/ui"
	"golang.org/x/term"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "nats-tui:", err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("nats-tui", flag.ContinueOnError)
	var server, path, refresh string
	var demo, showVersion bool
	flags.StringVar(&server, "s", "", "NATS server URL (overrides config)")
	flags.StringVar(&server, "server", "", "NATS server URL (same as -s)")
	flags.StringVar(&path, "config", "", "path to named-connections JSON config")
	flags.StringVar(&refresh, "refresh", "", "refresh interval, e.g. 2s (250ms–1h)")
	flags.BoolVar(&demo, "demo", false, "explore the UI with sample data; no server needed")
	flags.BoolVar(&showVersion, "version", false, "print version and exit")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "nats-tui — a live JetStream dashboard\n\nUsage: nats-tui [options]\n\nExamples:\n  nats-tui -s nats://localhost:4222\n  nats-tui --config connections.json\n  nats-tui --demo\n\nOptions:")
		flags.PrintDefaults()
	}
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments; see --help")
	}
	if showVersion {
		fmt.Println("nats-tui", version)
		return nil
	}
	if demo && (path != "" || server != "") {
		return errors.New("--demo cannot be combined with --config or --server")
	}
	if demo {
		server = "nats://demo:4222"
	}
	cfg, err := config.Load(path, server, refresh)
	if err != nil {
		return err
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("an interactive terminal is required; use docker run -it or docker compose run for Docker")
	}
	if os.Getenv("TERM") == "" {
		_ = os.Setenv("TERM", "xterm-256color")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	m := monitor.NewManager(cfg)
	if demo {
		m = monitor.NewDemo(cfg.Refresh)
	}
	updates := m.Start(ctx)
	err = ui.New(m.Initial(), demo, m.Refresh).Run(ctx, updates)
	cancel()
	<-m.Done()
	return err
}

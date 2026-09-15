package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/alevsk/natop/internal/config"
	"github.com/alevsk/natop/internal/monitor"
	"github.com/alevsk/natop/internal/ui"
	"golang.org/x/term"
)

var version = "dev"

func main() {
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok {
			if info.Main.Version != "" && info.Main.Version != "(devel)" {
				version = info.Main.Version
			} else {
				var rev, modified string
				for _, s := range info.Settings {
					switch s.Key {
					case "vcs.revision":
						rev = s.Value
					case "vcs.modified":
						if s.Value == "true" {
							modified = "+dirty"
						}
					}
				}
				if rev != "" {
					if len(rev) > 7 {
						rev = rev[:7]
					}
					version = fmt.Sprintf("dev (%s%s)", rev, modified)
				}
			}
		}
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "natop:", err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("natop", flag.ContinueOnError)
	var server, path, refresh string
	var demo, showVersion bool
	flags.StringVar(&server, "s", "", "NATS server URL (overrides config)")
	flags.StringVar(&server, "server", "", "NATS server URL (same as -s)")
	flags.StringVar(&path, "config", "", "path to named-connections YAML config")
	flags.StringVar(&refresh, "refresh", "", "refresh interval, e.g. 2s (250ms–1h)")
	flags.BoolVar(&demo, "demo", false, "explore the UI with sample data; no server needed")
	flags.BoolVar(&showVersion, "version", false, "print version and exit")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "natop — a live JetStream dashboard\n\nUsage: natop [options]\n\nExamples:\n  natop -s nats://localhost:4222\n  natop --config connections.yaml\n  natop --demo\n\nOptions:")
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
		fmt.Println("natop", version)
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

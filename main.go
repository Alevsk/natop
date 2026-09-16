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
	"github.com/alevsk/natop/internal/report"
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
	code, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "natop:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

// run returns an exit code and an error. The error is reserved for genuine
// tool/config failures, printed by main under the "natop:" prefix; a
// --once report that succeeds but finds an unhealthy fleet is signaled by a
// nonzero code with a nil error, so it never gets misreported as a tool bug.
func run() (int, error) {
	flags := flag.NewFlagSet("natop", flag.ContinueOnError)
	var server, path, refresh, format string
	var demo, showVersion, once bool
	var maxPending, maxAckPending, maxRedelivered uint64
	flags.StringVar(&server, "s", "", "NATS server URL (overrides config)")
	flags.StringVar(&server, "server", "", "NATS server URL (same as -s)")
	flags.StringVar(&path, "config", "", "path to a named-connections YAML config file, or a directory of them")
	flags.StringVar(&refresh, "refresh", "", "refresh interval, e.g. 2s (250ms–1h)")
	flags.BoolVar(&demo, "demo", false, "explore the UI with sample data; no server needed")
	flags.BoolVar(&showVersion, "version", false, "print version and exit")
	flags.BoolVar(&once, "once", false, "poll every connection once, print a report, and exit; no TTY required (for cron/CI/monitoring)")
	flags.StringVar(&format, "format", "text", "report format for --once: text or json, e.g. natop --config fleet.yaml --once --format json | jq")
	flags.Uint64Var(&maxPending, "max-pending", 0, "with --once, fail if any consumer's pending exceeds this (0 = unchecked), e.g. natop --config fleet.yaml --once --max-pending 100000 || page-oncall")
	flags.Uint64Var(&maxAckPending, "max-ack-pending", 0, "with --once, fail if any consumer's ack-pending exceeds this (0 = unchecked)")
	flags.Uint64Var(&maxRedelivered, "max-redelivered", 0, "with --once, fail if any consumer's redelivered exceeds this (0 = unchecked)")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "natop — a live JetStream dashboard\n\nUsage: natop [options]\n\nExamples:\n  natop -s nats://localhost:4222\n  natop --config connections.yaml\n  natop --config examples/fleet\n  natop --demo\n  natop --config fleet.yaml --once --format json | jq\n  natop --config fleet.yaml --once --max-pending 100000 || page-oncall\n\nOptions:")
		flags.PrintDefaults()
	}
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 0, err
	}
	if flags.NArg() != 0 {
		return 0, errors.New("unexpected positional arguments; see --help")
	}
	if showVersion {
		fmt.Println("natop", version)
		return 0, nil
	}
	onceOnlyFlagSet := false
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "format", "max-pending", "max-ack-pending", "max-redelivered":
			onceOnlyFlagSet = true
		}
	})
	if !once && onceOnlyFlagSet {
		return 0, errors.New("--format, --max-pending, --max-ack-pending, and --max-redelivered require --once")
	}
	if once && format != "text" && format != "json" {
		return 0, errors.New("--format must be text or json")
	}
	if demo && (path != "" || server != "") {
		return 0, errors.New("--demo cannot be combined with --config or --server")
	}
	if demo {
		server = "nats://demo:4222"
	}
	cfg, err := config.Load(path, server, refresh)
	if err != nil {
		return 0, err
	}
	if once {
		return runOnce(cfg, demo, format, report.Thresholds{MaxPending: maxPending, MaxAckPending: maxAckPending, MaxRedelivered: maxRedelivered})
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return 0, errors.New("an interactive terminal is required; use docker run -it or docker compose run for Docker")
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
	return 0, err
}

// runOnce performs one poll pass across all configured connections and
// prints a report. It deliberately never touches ui.New/Run: --once exists
// so headless environments without a TTY can script natop.
func runOnce(cfg config.Config, demo bool, format string, thresholds report.Thresholds) (int, error) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	m := monitor.NewManager(cfg)
	if demo {
		m = monitor.NewDemo(cfg.Refresh)
	}
	snapshots := m.Once(ctx)
	var writeErr error
	if format == "json" {
		writeErr = report.WriteJSON(os.Stdout, snapshots)
	} else {
		writeErr = report.WriteText(os.Stdout, snapshots)
	}
	if writeErr != nil {
		return 0, writeErr
	}
	breaches := report.Check(snapshots, thresholds)
	if len(breaches) == 0 {
		return 0, nil
	}
	_ = report.WriteBreaches(os.Stderr, breaches)
	return 1, nil
}

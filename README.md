# natop

A small, keyboard-driven NATS JetStream dashboard. One Go binary shows live
streams and consumer backlogs across multiple deployments, with filtering,
sorting, drill-down, and connection status.

<video src="https://github.com/user-attachments/assets/e73178b1-957c-4052-96f7-b7ecc0cc4b5a" controls="controls" muted="muted" style="max-height:640px;"></video>

```text
 natop  LIVE                       2/2 online  0 issues
 1 Streams   2 Consumers   3 Connections
 all connections · 3 rows · sort: messages ↓
 ┌ Streams ─────────────────────────────────────────────────────────┐
 │ CONNECTION  STREAM           STORAGE  CONSUMERS  MESSAGES  ...   │
 │ production  ORDERS            File            5      1200        │
 │ CONNECTION  STREAM           STORAGE  CONSUMERS  MESSAGES  ...   │
 └─────────────────────────────────────────────────────────────────┘
 Enter open  d details  / filter  c connection  s sort  r refresh
```

## Installation

### Homebrew (macOS/Linux)
```sh
brew install alevsk/tap/natop
```

### Go Install
If you have Go 1.26+ installed:
```sh
go install github.com/alevsk/natop@latest
```

## Quick Start
Build with **Go 1.26+** and Make. A recent Go installation can download the
required toolchain automatically. The executable needs no Go installation,
NATS CLI, database, or Docker at runtime.

```sh
make demo                                   # Try the UI with sample data
make run SERVER=nats://localhost:4222        # Connect to your NATS server
make run CONFIG=examples/connections.yaml    # Open both named deployments
make help                                   # List all targets
```

The demo is clearly labeled and never connects to a server. Quit with `q`.

Build a binary to copy to another machine:

```sh
make build                                  # bin/natop for this machine
make build-linux ARCH=amd64                  # bin/natop-linux-amd64
make build-linux ARCH=arm64                  # bin/natop-linux-arm64
make build VERSION=0.1.0
./bin/natop --version
```

The release builds disable CGO and strip debug information. The NATS server
dependency in `go.mod` is used by tests; it is not embedded in the application.

### Docker

If you prefer to run `natop` inside a container (for example, to easily attach it to existing Docker networks), you can build the image yourself:

```sh
make docker-build IMAGE=natop:latest
make docker-run NETWORK=my-network SERVER=nats://my-nats:4222
```
## Navigation

| Key | Action |
| --- | --- |
| `1`, `2`, `3` | Streams, all consumers, connections |
| Arrows or `j` / `k` | Move selection |
| Left/right or `h` / `l` | Scroll horizontally |
| `g` / `G`, Page Up/Down | Jump or page through rows |
| `Enter` | Open a stream's consumers, or inspect a consumer/connection |
| `d` | Full details for the selected stream, consumer, or connection |
| `/` | Filter visible rows; Enter keeps it, Escape clears it |
| `c` | Show one connection or all connections |
| `s` | Cycle sort columns; numeric columns sort largest first |
| `r` | Refresh all connections immediately |
| `Esc` | Close details/help, clear a filter, or return to streams |
| `?` | Help and metric definitions |
| `q` / Ctrl-C | Quit |

The default view combines all connections. Resource identity includes the
connection, so `AUDIT_EVENTS` in Reelify and Respondent are independent rows.
Refreshes preserve the selected resource. Details show a stable snapshot from
when you opened them; close and reopen to see updated details.

## Configuration

```yaml
refresh: 2s
connections:
  - name: local
    url: nats://localhost:4222
  - name: production
    url: tls://nats.example.com:4222
    credentials: ${NATS_CREDS}
    tls_ca: certs/ca.pem
```

Connection names must be unique. Each entry represents a separate account or
deployment. The client can reconnect to discovered servers within its cluster.
Supported URL schemes are `nats`, `tls`, `ws`, and `wss`; each entry accepts one
endpoint. For a JetStream domain, add `"domain": "your-domain"`.

Configuration precedence:

1. `--server` / `-s` selects an ad hoc connection and overrides config files.
2. `--config FILE` loads an explicit JSON file; a missing file is an error.
3. Otherwise load `natop/config.yaml` below Go's user config directory:
   `$XDG_CONFIG_HOME` or `~/.config` on Linux, `~/Library/Application Support`
   on macOS.
4. If no default file exists, use `NATS_URL`, then `nats://localhost:4222`.

`--refresh` overrides the file's interval. The default is `2s`; allowed values
are `250ms` through `1h`. A slow polling pass finishes before the next one starts.
`make run` respects these defaults unless `SERVER=` or `CONFIG=` is supplied.

Use one authentication method per connection:

| Method | Fields |
| --- | --- |
| NATS credentials file | `"credentials": "path/to/account.creds"` |
| Token | `"token": "${NATS_TOKEN}"` |
| User/password | `"user": "${NATS_USER}", "password": "${NATS_PASSWORD}"` |
| URL authentication | `nats://user:password@host:4222` (percent-encode special characters) |

TLS supports `tls_ca` and the paired `tls_cert` / `tls_key` paths. Certificates
are verified. Auth values, URLs, and credential/TLS paths expand explicit
`${VARIABLE}` references; an unset variable is an error. File paths are relative
to the config file and support `~/`. Credentials are redacted from diagnostics
and URL displays. Environment values inserted into URLs must already be URL-encoded.

In Docker, files and environment variables must exist **inside the container**.
The config directory is `/config`; mount additional credential files and pass
variables with `DOCKER_ARGS`. For example, with `"token": "${NATS_TOKEN}"`:

```sh
export NATS_TOKEN='your-token'
make docker-multi MULTI_CONFIG=/path/to/connections.yaml DOCKER_ARGS='-e NATS_TOKEN'
```

The image runs as UID/GID `65532`. If your mounted config or credentials are
readable only by your own user, run the container as that user:

```sh
make docker-multi MULTI_CONFIG=/path/to/connections.yaml \
  DOCKER_ARGS="--user $(id -u):$(id -g) -v /path/to/account.creds:/config/account.creds:ro"
```

## Scripting / health checks

The interactive TUI needs a real TTY, which does not fit cron, CI, or a
monitoring system checking hundreds of deployments. `--once` polls every
configured connection exactly once, prints a report, and exits — no terminal
required.

```sh
natop --config fleet.yaml --once                        # compact text report
natop --config fleet.yaml --once --format json | jq      # structured for scripts
natop --demo --once --format json                        # see the report shape; no server needed
```

`--format` (`text` or `json`, default `text`) controls the report shape.
`text` prints one greppable `resource key=value ...` line per connection,
stream, and consumer. `json` prints a single JSON array of connections, each
with its streams and their consumers, using natop's own stable, snake_case
field names — not a dump of the underlying NATS client library's types — so
scripts are not coupled to an upstream shape that could change under them.

Add health thresholds to turn `--once` into a pass/fail check:

```sh
natop --config fleet.yaml --once --max-pending 100000 || page-oncall
natop --config fleet.yaml --once --max-ack-pending 5000 --max-redelivered 50
```

`--max-pending`, `--max-ack-pending`, and `--max-redelivered` (each a count,
default `0` = unchecked) fail the run if any consumer's `NumPending`,
`NumAckPending`, or `NumRedelivered` exceeds the given limit. Independently of
thresholds, any connection that isn't `online` always counts as a failure — a
fleet check that ignores dead connections isn't worth running. `--format` and
the threshold flags only apply to `--once`; passing them otherwise is an error.

Exit codes: `0` means the report succeeded and the fleet is healthy; `1`
means either a threshold/connection breach was found (details go to stderr)
or the tool itself failed. Genuine tool/config errors are printed with a
`natop:` prefix; a routine "fleet is unhealthy" result is not.

## What the metrics mean

- **Messages / bytes:** data currently stored in a stream.
- **Pending:** messages not delivered to that consumer yet.
- **Ack pending:** delivered messages awaiting acknowledgment.
- **Redelivered:** messages redelivered and still awaiting acknowledgment,
  not a lifetime total or rate.
- **Deleted:** sequence gaps inside the stored stream's range, not all historical
  deletions or messages expired from the beginning of the stream.
- **Lost:** `—` because the current Go client metadata type does not expose it.
- **Replicas:** configured replica count; full metadata includes cluster details.

Consumers may overlap, so their pending counts are not summed as a unique queue
depth. The monitor uses JetStream stream/consumer metadata APIs. It never creates
a consumer, receives application messages, acknowledges them, or changes streams.
The account needs permission to list streams, read stream information, list
consumers, and receive replies on the client's inbox subjects. Errors remain
visible when an account lacks these permissions.

On a failed refresh, previous data remains **stale**, with an error and the last
successful timestamp. **Partial** means stream metadata refreshed but some
consumer metadata did not. Failed requests never turn a previously known backlog
into a displayed zero. Healthy empty results do clear old rows.

This first version covers **JetStream streams and consumers**. Core NATS queue
groups, server-wide connection monitoring, message browsing, KV/object-store
interfaces, publishing, and administrative mutations are outside its scope.
KV/object stores may appear as their underlying JetStream streams.

## Development

```sh
make fmt
make test       # Starts temporary in-process NATS servers; no Docker needed
make vet
make check      # Vet + race-enabled tests
make clean
```

The source is split into configuration, polling, and terminal packages under
`internal/`. Polling runs independently per connection; only the UI event loop
updates widgets. Shutdown cancels polling, closes connections, and restores the
terminal.

References: [NATS Go JetStream API](https://github.com/nats-io/nats.go/blob/main/jetstream/README.md),
[tview](https://github.com/rivo/tview),
[Docker Compose networking](https://docs.docker.com/compose/how-tos/networking/).

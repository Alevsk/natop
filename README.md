# nats-tui

A small, keyboard-driven NATS JetStream dashboard. One Go binary shows live
streams and consumer backlogs across multiple deployments, with filtering,
sorting, drill-down, and connection status.

```text
 nats-tui  LIVE                       2/2 online  0 issues
 1 Streams   2 Consumers   3 Connections
 all connections · 3 rows · sort: messages ↓
 ┌ Streams ─────────────────────────────────────────────────────────┐
 │ CONNECTION  STREAM           STORAGE  CONSUMERS  MESSAGES  ...   │
 │ respondent  RESPONDENT_WORK   File           12       162        │
 │ respondent  QUERY_EVENTS      File            1        42        │
 │ reelify     AUDIT_EVENTS      File            1         0        │
 └─────────────────────────────────────────────────────────────────┘
 Enter open  d details  / filter  c connection  s sort  r refresh
```

## Start here

Build with **Go 1.26+** and Make. A recent Go installation can download the
required toolchain automatically. The executable needs no Go installation,
NATS CLI, database, or Docker at runtime.

```sh
make demo                                   # Try the UI with sample data
make run SERVER=nats://localhost:4222        # Connect to your NATS server
make run CONFIG=examples/connections.json    # Open both named deployments
make help                                   # List all targets
```

The demo is clearly labeled and never connects to a server. Quit with `q`.

Build a binary to copy to another machine:

```sh
make build                                  # bin/nats-tui for this machine
make build-linux ARCH=amd64                  # bin/nats-tui-linux-amd64
make build-linux ARCH=arm64                  # bin/nats-tui-linux-arm64
make build VERSION=0.1.0
./bin/nats-tui --version
```

The release builds disable CGO and strip debug information. The NATS server
dependency in `go.mod` is used by tests; it is not embedded in the application.

## Your Reelify and Respondent networks

The included config uses the addresses from your reports:

| Connection | NATS address | Existing Docker network |
| --- | --- | --- |
| reelify | `nats://reelify-nats:4222` | `reelify-data` |
| respondent | `nats://respondent-platform-nats-1:4222` | `respondent-platform_respondent-data` |

On the Docker host where these services run:

```sh
make docker-multi
```

This builds the image and starts one interactive container attached to **both**
existing networks. It mounts `examples/connections.json` read-only. `q` exits
and removes the TUI container. It does not create or modify your NATS services.

For a single network:

```sh
make docker-run NETWORK=reelify-data SERVER=nats://reelify-nats:4222

make docker-run \
  NETWORK=respondent-platform_respondent-data \
  SERVER=nats://respondent-platform-nats-1:4222
```

Other Docker commands:

```sh
make docker-build                           # nats-tui:local
make docker-demo                            # No NATS/network setup needed
make docker-multi MULTI_CONFIG=/absolute/path/connections.json
make docker-multi REELIFY_NETWORK=my-first-network RESPONDENT_NETWORK=my-second-network
make docker-run NETWORK=my-network CONFIG=connections.local.json
```

`IMAGE=...` changes the image tag; `ARGS='--refresh 5s'` passes app flags.
`DOCKER_ARGS='...'` passes options to `docker run` or `docker compose run`.
After the first build, rebuilding uses Docker's cache. You can also run
`docker compose run --rm nats-tui` directly to reuse an existing image.

Docker service names resolve inside their networks. Running the native binary
on your laptop or host requires reachable hostnames/published ports or an
existing tunnel. A network or DNS error stays visible in the connections view;
other deployments keep updating. Verify names on the actual Docker host:

```sh
docker network ls
docker network inspect reelify-data
docker network inspect respondent-platform_respondent-data
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

```json
{
  "refresh": "2s",
  "connections": [
    { "name": "local", "url": "nats://localhost:4222" },
    {
      "name": "production",
      "url": "tls://nats.example.com:4222",
      "credentials": "${NATS_CREDS}",
      "tls_ca": "certs/ca.pem"
    }
  ]
}
```

Connection names must be unique. Each entry represents a separate account or
deployment. The client can reconnect to discovered servers within its cluster.
Supported URL schemes are `nats`, `tls`, `ws`, and `wss`; each entry accepts one
endpoint. For a JetStream domain, add `"domain": "your-domain"`.

Configuration precedence:

1. `--server` / `-s` selects an ad hoc connection and overrides config files.
2. `--config FILE` loads an explicit JSON file; a missing file is an error.
3. Otherwise load `nats-tui/config.json` below Go's user config directory:
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
make docker-multi MULTI_CONFIG=/path/to/connections.json DOCKER_ARGS='-e NATS_TOKEN'
```

The image runs as UID/GID `65532`. If your mounted config or credentials are
readable only by your own user, run the container as that user:

```sh
make docker-multi MULTI_CONFIG=/path/to/connections.json \
  DOCKER_ARGS="--user $(id -u):$(id -g) -v /path/to/account.creds:/config/account.creds:ro"
```

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

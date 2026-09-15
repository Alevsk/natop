# natop: first-version design

Status: implemented, including Makefile build/run/Docker targets.

Implementation notes: dependencies require Go 1.26. The lost-message column
displays an unknown marker because the current Go client does not expose that
metric. Verification was focused on core behavior at the user's request; results
are recorded in the implementation plan.

## Purpose

A small Go application that replaces repeated `nats stream report` and
`nats consumer report` commands with a live, keyboard-driven terminal UI.
Ship one executable for Linux and macOS. Connect directly to NATS using the
official Go client; the installed NATS CLI is not a runtime dependency.

The first version focuses on the JetStream streams and consumers shown in the
request. Monitoring uses metadata APIs and never creates consumers, receives
application messages, acknowledges messages, or modifies streams.

## Approaches considered

1. **Direct NATS client with tview (recommended):** ready-made navigable tables,
   small application code, and separate connections to multiple deployments.
2. **Direct NATS client with Bubble Tea:** suits more customized interactions,
   but would require more UI state and rendering code for this table-first app.
3. **Wrap the NATS CLI:** quick prototype, but adds an executable dependency and
   subprocess management. Does not meet the standalone-binary objective well.

## User experience

Default to a combined streams table with a connection column. Allow filtering
to one named connection. Each named connection identifies one NATS account and
deployment; similarly named streams across deployments remain separate rows.

- **Streams:** connection, name, storage, consumer count, stored messages,
  bytes, lost messages, deleted messages, and configured replica count.
- **Consumers:** connection, stream, consumer, pull/push mode, acknowledgment
  policy, acknowledgment wait, pending, acknowledgment pending, and redelivered.
  Available across all configured connections or scoped to a selected stream.
- **Details:** stream subjects, retention, limits and replication information;
  consumer filters, delivery configuration, acknowledgment floor, and state.
- **Connections:** configured name, sanitized address, connection status,
  last successful refresh, and actionable error text.

Use a compact header, full-width table, and persistent keyboard hints. Refresh
every two seconds by default; preserve selection by resource identity during
refresh, filtering, and sorting. Support small terminals through scrolling.

Keys: `1` streams, `2` consumers, `3` connections, arrows or `j`/`k` to move,
`Enter` to drill into a stream's consumers or inspect a consumer, `d` for details,
`Esc` to go back, `/` to filter, `c` to choose a connection or all connections,
`s` to cycle relevant sort columns, `r` to refresh, `?` for help, and `q`/`Ctrl-C`
to quit. Escape closes input or overlays before navigating back.

The UI labels stored messages separately from consumer pending messages:
pending is work not yet delivered to that consumer; acknowledgment pending is
delivered work awaiting acknowledgment. Backlogs from different consumers are
not presented as a unique message total. Redelivered is the server-reported
current consumer statistic, not a calculated lifetime rate.

## Connections and configuration

Support `natop -s nats://localhost:4222` for a single connection and
`natop --config connections.yaml` for named connections. The normal config
location is `natop/config.yaml` beneath Go's `os.UserConfigDir()` directory.
An explicit server flag selects an ad hoc connection. If no file or server flag
is supplied, use `NATS_URL`, falling back to `nats://localhost:4222`.

Use JSON to keep configuration in the Go standard library. Include a refresh
duration and a list of uniquely named connections, each with a URL. Support a
NATS credentials file, token or username/password, and TLS CA/client certificate
paths. Allow explicit environment-variable references for secret values, report
missing referenced variables, and redact credentials from displays and errors.
Reject conflicting authentication settings and invalid configuration clearly.

Provide an example with `reelify` at `nats://reelify-nats:4222` and `respondent`
at `nats://respondent-platform-nats-1:4222`. Each gets an independent client;
these deployments are not configured as failover servers for the same cluster.

## Docker usage

The binary needs network access to each configured address. Docker service
names resolve within their networks. Include an optional Dockerfile and Compose
example attaching the TUI container to both existing external networks:
`reelify-data` and `respondent-platform_respondent-data`. Mount its configuration
read-only and allocate an interactive terminal. A host-installed binary can use
reachable published ports or existing tunnels instead of Docker service names.

The application does not inspect or change Docker networks. The example uses
the network and hostname values supplied in the request; availability on the
remote `cloud` host must be verified there.

## Implementation boundaries and failures

Keep three small internal components: configuration loading, NATS polling and
snapshots, and the terminal UI. A thin main package handles flags and lifecycle.
Use `nats.go/jetstream` and `tview`; avoid a database, web service, and plugins.

Run one cancellable polling worker per connection. Bound requests with timeouts
and avoid overlapping polls for a connection. Publish immutable snapshots to the
UI event loop; network requests never run on that loop. Retry initial connection
failures and reconnect after disconnects. On shutdown cancel work, close clients,
and restore the terminal.

Successful empty results clear old rows. Failed refreshes retain previous data
with a visible stale timestamp and error; never show failed fetches as zero
backlog. Partial consumer-list failures identify the affected stream, while
other streams and connections keep updating. Distinguish an empty deployment
from a connection failure, unavailable JetStream, and access errors whenever
the server response permits. Escape server-provided terminal markup and control
characters before rendering them.

## Verification and deliverables

Deliver source, pinned Go modules, a README, sample connection configuration,
optional Docker packaging, and a `--demo` mode with clearly labeled sample data
so the UI can be tried without a NATS deployment.

Test configuration precedence/validation/redaction, resource identity across
connections, filtering/navigation, and stale/partial refresh handling. Verify
against disposable local JetStream servers with stored messages and consumers
in different acknowledgment states; confirm monitoring leaves those states
unchanged. Exercise a failed connection alongside a healthy one and recovery.
Run race-enabled tests, `go vet`, and native plus Linux builds with CGO disabled.
Measure the resulting binary size rather than promising a size in advance.

## Sources

- [Official NATS Go JetStream API](https://github.com/nats-io/nats.go/blob/main/jetstream/README.md)
- [JetStream management subjects](https://github.com/nats-io/nats.docs/blob/master/using-nats/jetstream/nats_api_reference.md)
- [tview terminal widgets](https://github.com/rivo/tview)
- [Docker Compose networking](https://docs.docker.com/compose/how-tos/networking/)

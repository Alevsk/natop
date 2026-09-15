// Package monitor collects read-only JetStream metadata. Snapshots are immutable
// after publication so the terminal can render them without locking clients.
package monitor

import (
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

type Stream struct {
	Info             *jetstream.StreamInfo
	Consumers        []*jetstream.ConsumerInfo
	Updated          time.Time
	ConsumersUpdated time.Time
	Error            string
}

type Snapshot struct {
	Name    string
	URL     string
	Status  string
	Error   string
	Updated time.Time // Last fully successful refresh.
	Streams []Stream
}

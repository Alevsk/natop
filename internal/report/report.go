// Package report renders monitor snapshots for scriptable, non-interactive
// use (natop --once) and checks them against operator-supplied health
// thresholds. Its JSON schema is natop-owned and stable across releases,
// not a dump of nats.go/jetstream's types, so scripts consuming it are not
// coupled to an upstream library's shape.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/alevsk/natop/internal/monitor"
	"github.com/nats-io/nats.go/jetstream"
)

// Connection is the top-level record: one configured deployment and
// everything discovered on it during a single poll pass.
type Connection struct {
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	Streams   []Stream  `json:"streams"`
}

type Stream struct {
	Name      string     `json:"name"`
	Storage   string     `json:"storage"`
	Replicas  int        `json:"replicas"`
	Messages  uint64     `json:"messages"`
	Bytes     uint64     `json:"bytes"`
	Consumers []Consumer `json:"consumers"`
}

type Consumer struct {
	Name        string `json:"name"`
	Mode        string `json:"mode"`
	Pending     uint64 `json:"pending"`
	AckPending  int    `json:"ack_pending"`
	Redelivered int    `json:"redelivered"`
	AckPolicy   string `json:"ack_policy"`
}

// Build converts monitor snapshots to the stable report schema.
func Build(snapshots []monitor.Snapshot) []Connection {
	out := make([]Connection, 0, len(snapshots))
	for _, s := range snapshots {
		conn := Connection{Name: s.Name, URL: s.URL, Status: s.Status, Error: s.Error, UpdatedAt: s.Updated}
		for _, stream := range s.Streams {
			if stream.Info == nil {
				continue
			}
			st := Stream{
				Name: stream.Info.Config.Name, Storage: storageName(stream.Info.Config.Storage),
				Replicas: stream.Info.Config.Replicas, Messages: stream.Info.State.Msgs, Bytes: stream.Info.State.Bytes,
			}
			for _, c := range stream.Consumers {
				mode := "pull"
				if c.Config.DeliverSubject != "" {
					mode = "push"
				}
				st.Consumers = append(st.Consumers, Consumer{
					Name: c.Name, Mode: mode, Pending: c.NumPending,
					AckPending: c.NumAckPending, Redelivered: c.NumRedelivered, AckPolicy: ackPolicyName(c.Config.AckPolicy),
				})
			}
			conn.Streams = append(conn.Streams, st)
		}
		out = append(out, conn)
	}
	return out
}

func storageName(s jetstream.StorageType) string {
	if s == jetstream.MemoryStorage {
		return "memory"
	}
	return "file"
}

func ackPolicyName(p jetstream.AckPolicy) string {
	switch p {
	case jetstream.AckAllPolicy:
		return "all"
	case jetstream.AckNonePolicy:
		return "none"
	case jetstream.AckFlowControlPolicy:
		return "flow_control"
	default:
		return "explicit"
	}
}

// WriteJSON encodes the report schema as a single JSON array, one entry per
// connection, suitable for `natop --once --format json | jq`.
func WriteJSON(w io.Writer, snapshots []monitor.Snapshot) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(Build(snapshots))
}

// WriteText renders one greppable "resource key=value ..." line per
// connection, stream, and consumer. Distinct from the interactive table;
// meant for cron and CI logs.
func WriteText(w io.Writer, snapshots []monitor.Snapshot) error {
	for _, conn := range Build(snapshots) {
		if _, err := fmt.Fprintf(w, "connection name=%s url=%s status=%s error=%q updated_at=%s\n",
			conn.Name, conn.URL, conn.Status, conn.Error, conn.UpdatedAt.Format(time.RFC3339)); err != nil {
			return err
		}
		for _, s := range conn.Streams {
			if _, err := fmt.Fprintf(w, "stream connection=%s name=%s storage=%s replicas=%d messages=%d bytes=%d\n",
				conn.Name, s.Name, s.Storage, s.Replicas, s.Messages, s.Bytes); err != nil {
				return err
			}
			for _, c := range s.Consumers {
				if _, err := fmt.Fprintf(w, "consumer connection=%s stream=%s name=%s mode=%s ack_policy=%s pending=%d ack_pending=%d redelivered=%d\n",
					conn.Name, s.Name, c.Name, c.Mode, c.AckPolicy, c.Pending, c.AckPending, c.Redelivered); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

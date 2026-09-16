package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alevsk/natop/internal/monitor"
	"github.com/nats-io/nats.go/jetstream"
)

func sample() []monitor.Snapshot {
	updated := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	return []monitor.Snapshot{
		{
			Name: "production", URL: "nats://prod:4222", Status: "online", Updated: updated,
			Streams: []monitor.Stream{{
				Info: &jetstream.StreamInfo{
					Config: jetstream.StreamConfig{Name: "ORDERS", Storage: jetstream.FileStorage, Replicas: 3},
					State:  jetstream.StreamState{Msgs: 1200, Bytes: 45056},
				},
				Consumers: []*jetstream.ConsumerInfo{{
					Name:          "worker",
					Config:        jetstream.ConsumerConfig{AckPolicy: jetstream.AckExplicitPolicy},
					NumPending:    12,
					NumAckPending: 3,
				}},
			}},
		},
		{Name: "staging", URL: "nats://staging:4222", Status: "offline", Error: "dial timeout"},
	}
}

func TestBuildMapsSnapshotsToStableSchema(t *testing.T) {
	conns := Build(sample())
	if len(conns) != 2 {
		t.Fatalf("connections = %d", len(conns))
	}
	prod := conns[0]
	if prod.Name != "production" || prod.Status != "online" || len(prod.Streams) != 1 {
		t.Fatalf("production: %+v", prod)
	}
	stream := prod.Streams[0]
	if stream.Name != "ORDERS" || stream.Storage != "file" || stream.Replicas != 3 || stream.Messages != 1200 || stream.Bytes != 45056 {
		t.Fatalf("stream: %+v", stream)
	}
	if len(stream.Consumers) != 1 {
		t.Fatalf("consumers: %+v", stream.Consumers)
	}
	consumer := stream.Consumers[0]
	if consumer.Name != "worker" || consumer.Mode != "pull" || consumer.Pending != 12 || consumer.AckPending != 3 || consumer.AckPolicy != "explicit" {
		t.Fatalf("consumer: %+v", consumer)
	}
	staging := conns[1]
	if staging.Status != "offline" || staging.Error != "dial timeout" || len(staging.Streams) != 0 {
		t.Fatalf("staging: %+v", staging)
	}
}

func TestAckPolicyAndStorageNames(t *testing.T) {
	cases := []struct {
		policy jetstream.AckPolicy
		want   string
	}{
		{jetstream.AckExplicitPolicy, "explicit"},
		{jetstream.AckAllPolicy, "all"},
		{jetstream.AckNonePolicy, "none"},
		{jetstream.AckFlowControlPolicy, "flow_control"},
	}
	for _, c := range cases {
		if got := ackPolicyName(c.policy); got != c.want {
			t.Errorf("ackPolicyName(%v) = %q, want %q", c.policy, got, c.want)
		}
	}
	if got := storageName(jetstream.MemoryStorage); got != "memory" {
		t.Errorf("storageName(Memory) = %q", got)
	}
	if got := storageName(jetstream.FileStorage); got != "file" {
		t.Errorf("storageName(File) = %q", got)
	}
}

func TestWriteJSONRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, sample()); err != nil {
		t.Fatal(err)
	}
	var conns []Connection
	if err := json.Unmarshal(buf.Bytes(), &conns); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(conns) != 2 {
		t.Fatalf("connections = %d", len(conns))
	}
	if conns[0].Name != "production" || conns[0].Streams[0].Consumers[0].Pending != 12 {
		t.Fatalf("round-tripped: %+v", conns[0])
	}
	// Field names are natop's own, snake_case, and independent of jetstream's shape.
	for _, key := range []string{`"name"`, `"url"`, `"status"`, `"updated_at"`, `"streams"`, `"ack_pending"`, `"ack_policy"`} {
		if !strings.Contains(buf.String(), key) {
			t.Errorf("missing expected key %s in:\n%s", key, buf.String())
		}
	}
}

func TestWriteTextIsOneLinePerResource(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteText(&buf, sample()); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("lines = %d, want 4 (2 connections + 1 stream + 1 consumer):\n%s", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], "connection name=production ") {
		t.Errorf("line 0: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "stream connection=production name=ORDERS ") {
		t.Errorf("line 1: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "consumer connection=production stream=ORDERS name=worker ") {
		t.Errorf("line 2: %q", lines[2])
	}
	if !strings.HasPrefix(lines[3], "connection name=staging ") || !strings.Contains(lines[3], "status=offline") {
		t.Errorf("line 3: %q", lines[3])
	}
}

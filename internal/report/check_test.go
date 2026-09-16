package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alevsk/natop/internal/monitor"
	"github.com/nats-io/nats.go/jetstream"
)

func consumerSnapshot(status string, pending uint64, ackPending, redelivered int) []monitor.Snapshot {
	return []monitor.Snapshot{{
		Name: "prod", Status: status,
		Streams: []monitor.Stream{{
			Info: &jetstream.StreamInfo{Config: jetstream.StreamConfig{Name: "ORDERS"}},
			Consumers: []*jetstream.ConsumerInfo{{
				Name: "worker", NumPending: pending, NumAckPending: ackPending, NumRedelivered: redelivered,
			}},
		}},
	}}
}

func TestCheckThresholds(t *testing.T) {
	cases := []struct {
		name       string
		snapshots  []monitor.Snapshot
		thresholds Thresholds
		wantCount  int
		wantMetric string
	}{
		{
			name:       "zero threshold disables the check even under huge values",
			snapshots:  consumerSnapshot("online", 1_000_000, 1_000_000, 1_000_000),
			thresholds: Thresholds{},
			wantCount:  0,
		},
		{
			name:       "under threshold is healthy",
			snapshots:  consumerSnapshot("online", 10, 1, 0),
			thresholds: Thresholds{MaxPending: 100, MaxAckPending: 100, MaxRedelivered: 100},
			wantCount:  0,
		},
		{
			name:       "pending over threshold breaches",
			snapshots:  consumerSnapshot("online", 150, 0, 0),
			thresholds: Thresholds{MaxPending: 100},
			wantCount:  1,
			wantMetric: "pending",
		},
		{
			name:       "ack_pending over threshold breaches",
			snapshots:  consumerSnapshot("online", 0, 50, 0),
			thresholds: Thresholds{MaxAckPending: 10},
			wantCount:  1,
			wantMetric: "ack_pending",
		},
		{
			name:       "redelivered over threshold breaches",
			snapshots:  consumerSnapshot("online", 0, 0, 5),
			thresholds: Thresholds{MaxRedelivered: 1},
			wantCount:  1,
			wantMetric: "redelivered",
		},
		{
			name:       "value equal to limit does not breach",
			snapshots:  consumerSnapshot("online", 100, 0, 0),
			thresholds: Thresholds{MaxPending: 100},
			wantCount:  0,
		},
		{
			name:       "non-online connection always breaches regardless of thresholds",
			snapshots:  consumerSnapshot("offline", 0, 0, 0),
			thresholds: Thresholds{},
			wantCount:  1,
			wantMetric: "connection_status",
		},
		{
			name:       "non-online connection breaches in addition to consumer breaches",
			snapshots:  consumerSnapshot("partial", 150, 0, 0),
			thresholds: Thresholds{MaxPending: 100},
			wantCount:  2,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Check(c.snapshots, c.thresholds)
			if len(got) != c.wantCount {
				t.Fatalf("breaches = %d, want %d: %+v", len(got), c.wantCount, got)
			}
			if c.wantMetric != "" {
				found := false
				for _, b := range got {
					if b.Metric == c.wantMetric {
						found = true
					}
				}
				if !found {
					t.Fatalf("no breach with metric %q: %+v", c.wantMetric, got)
				}
			}
		})
	}
}

func TestCheckReportsValueAndLimit(t *testing.T) {
	got := Check(consumerSnapshot("online", 150, 0, 0), Thresholds{MaxPending: 100})
	if len(got) != 1 {
		t.Fatalf("breaches = %d", len(got))
	}
	b := got[0]
	if b.Connection != "prod" || b.Stream != "ORDERS" || b.Consumer != "worker" || b.Value != 150 || b.Limit != 100 {
		t.Fatalf("breach: %+v", b)
	}
}

func TestWriteBreaches(t *testing.T) {
	breaches := Check(consumerSnapshot("offline", 150, 0, 0), Thresholds{MaxPending: 100})
	var buf bytes.Buffer
	if err := WriteBreaches(&buf, breaches); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != len(breaches) {
		t.Fatalf("lines = %d, want %d:\n%s", len(lines), len(breaches), buf.String())
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "breach connection=prod ") {
			t.Errorf("line: %q", line)
		}
	}
}

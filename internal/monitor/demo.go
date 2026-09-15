package monitor

import (
	"time"

	"github.com/alevsk/natop/internal/config"
	"github.com/nats-io/nats.go/jetstream"
)

func NewDemo(refresh time.Duration) *Manager {
	m := NewManager(config.Config{Refresh: refresh, Connections: []config.Connection{
		{Name: "reelify", URL: "nats://demo-reelify:4222"},
		{Name: "respondent", URL: "nats://demo-respondent:4222"},
	}})
	m.demo = true
	return m
}

func demoSnapshot(name string, tick uint64) Snapshot {
	now := time.Now()
	snapshot := Snapshot{Name: name, URL: "demo://" + name, Status: "online", Updated: now}
	names := []string{"AUDIT_EVENTS", "CLIP_REQUESTS", "MEDIA_RENDER_REQUESTS", "SYNC_REQUESTS", "VIDEOGEN_REQUESTS"}
	if name == "respondent" {
		names = []string{"AUDIT_EVENTS", "EVENTS", "LAKEHOUSE_EVENTS", "QUERY_EVENTS", "RESPONDENT_WORK"}
	}
	for i, streamName := range names {
		pending := uint64(i*13) + tick%9
		if i == 0 {
			pending = 0
		}
		ack := i * 2
		consumer := &jetstream.ConsumerInfo{
			Name: "worker", Stream: streamName,
			Config:     jetstream.ConsumerConfig{Durable: "worker", AckPolicy: jetstream.AckExplicitPolicy, AckWait: 30 * time.Second, MaxAckPending: 1000},
			NumPending: pending, NumAckPending: ack,
			AckFloor: jetstream.SequenceInfo{Consumer: 100 + tick, Stream: 100 + tick},
		}
		if i == 4 {
			consumer.NumRedelivered = 2
		}
		info := &jetstream.StreamInfo{
			Config: jetstream.StreamConfig{Name: streamName, Subjects: []string{streamName + ".>"}, Storage: jetstream.FileStorage, Retention: jetstream.WorkQueuePolicy, Replicas: 1},
			State:  jetstream.StreamState{Msgs: pending + uint64(ack), Bytes: (pending + uint64(ack)) * 128, Consumers: 1, FirstSeq: 101, LastSeq: 100 + pending + uint64(ack)},
		}
		snapshot.Streams = append(snapshot.Streams, Stream{Info: info, Consumers: []*jetstream.ConsumerInfo{consumer}, Updated: now, ConsumersUpdated: now})
	}
	return snapshot
}

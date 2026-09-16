package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/alevsk/natop/internal/config"
)

func TestManagerOnceReturnsOncePerConnectionWithoutWaitingForRefresh(t *testing.T) {
	s := testServer(t, nil)
	seed(t, s.ClientURL())
	// A huge refresh interval proves Once cannot be waiting on the ticker
	// loop Start uses: if it were, this test would hang past its timeout.
	m := NewManager(config.Config{Refresh: time.Hour, Connections: []config.Connection{
		{Name: "b-good", URL: s.ClientURL()},
		{Name: "a-down", URL: "nats://127.0.0.1:1"},
	}})
	done := make(chan []Snapshot, 1)
	go func() { done <- m.Once(context.Background()) }()
	select {
	case snapshots := <-done:
		if len(snapshots) != 2 {
			t.Fatalf("snapshots = %d, want 2", len(snapshots))
		}
		// Order matches configuration order, not completion order.
		if snapshots[0].Name != "b-good" || snapshots[1].Name != "a-down" {
			t.Fatalf("snapshots out of configured order: %+v", snapshots)
		}
		if snapshots[0].Status != "online" || len(snapshots[0].Streams) != 1 {
			t.Fatalf("good connection: %+v", snapshots[0])
		}
		if snapshots[1].Error == "" {
			t.Fatalf("down connection should report an error: %+v", snapshots[1])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Once did not return promptly; it may be waiting for a refresh tick")
	}
}

func TestManagerOnceDemoTouchesNoNetwork(t *testing.T) {
	m := NewDemo(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshots := m.Once(ctx)
	if len(snapshots) != len(m.config.Connections) {
		t.Fatalf("snapshots = %d, want %d", len(snapshots), len(m.config.Connections))
	}
	for _, s := range snapshots {
		if s.Status != "online" || len(s.Streams) == 0 {
			t.Fatalf("demo snapshot should be sample data with no network involved: %+v", s)
		}
	}
}

package monitor

import (
	"context"
	"sync"
	"time"

	"github.com/alevsk/natop/internal/config"
)

// Manager runs one independent worker per configured deployment.
type Manager struct {
	config   config.Config
	requests []chan struct{}
	done     chan struct{}
	demo     bool
}

func NewManager(cfg config.Config) *Manager {
	m := &Manager{config: cfg, done: make(chan struct{})}
	for range cfg.Connections {
		m.requests = append(m.requests, make(chan struct{}, 1))
	}
	return m
}

func (m *Manager) Done() <-chan struct{} { return m.done }

func (m *Manager) Initial() []Snapshot {
	initial := make([]Snapshot, 0, len(m.config.Connections))
	for _, c := range m.config.Connections {
		initial = append(initial, Snapshot{Name: c.Name, URL: c.SafeURL(), Status: "connecting"})
	}
	return initial
}

// Refresh coalesces repeated key presses while a poll is already running.
func (m *Manager) Refresh() {
	for _, request := range m.requests {
		select {
		case request <- struct{}{}:
		default:
		}
	}
}

// Start must be called once. Cancellation closes all clients and both channels.
func (m *Manager) Start(ctx context.Context) <-chan Snapshot {
	updates := make(chan Snapshot, len(m.config.Connections)*2)
	var wg sync.WaitGroup
	for i, connection := range m.config.Connections {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := NewClient(connection, 2*time.Second)
			defer client.Close()
			previous := Snapshot{Name: connection.Name, URL: connection.SafeURL(), Status: "connecting"}
			for tick := uint64(0); ctx.Err() == nil; tick++ {
				if m.demo {
					previous = demoSnapshot(connection.Name, tick)
				} else {
					previous = client.Fetch(ctx, previous)
				}
				select {
				case updates <- previous:
				case <-ctx.Done():
					return
				}
				timer := time.NewTimer(m.config.Refresh)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-m.requests[i]:
					timer.Stop()
				case <-timer.C:
				}
			}
		}()
	}
	go func() { wg.Wait(); close(updates); close(m.done) }()
	return updates
}

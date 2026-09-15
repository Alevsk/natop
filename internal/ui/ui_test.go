package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alevsk/natop/internal/monitor"
	"github.com/gdamore/tcell/v2"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/rivo/tview"
)

func sample(name string, messages uint64) monitor.Snapshot {
	now := time.Now()
	return monitor.Snapshot{Name: name, URL: "nats://localhost:4222", Status: "online", Updated: now, Streams: []monitor.Stream{{
		Info:      &jetstream.StreamInfo{Config: jetstream.StreamConfig{Name: "WORK", Subjects: []string{"work.>"}, Replicas: 1}, State: jetstream.StreamState{Msgs: messages, Consumers: 1}},
		Consumers: []*jetstream.ConsumerInfo{{Name: "worker", Stream: "WORK", NumPending: messages, Config: jetstream.ConsumerConfig{AckPolicy: jetstream.AckExplicitPolicy}}},
		Updated:   now, ConsumersUpdated: now,
	}}}
}

func press(u *UI, key tcell.Key, ch rune) {
	e := u.key(tcell.NewEventKey(key, ch, tcell.ModNone))
	if e != nil {
		if handler := u.app.GetFocus().InputHandler(); handler != nil {
			handler(e, func(p tview.Primitive) { u.app.SetFocus(p) })
		}
	}
}

func TestNavigationKeepsSameNamedStreamsSeparate(t *testing.T) {
	u := New([]monitor.Snapshot{sample("one", 1), sample("two", 7)}, false, nil)
	if len(u.rows) != 2 {
		t.Fatalf("rows = %d", len(u.rows))
	}
	u.table.Select(2, 0)
	press(u, tcell.KeyEnter, 0)
	if u.view != consumersView || len(u.rows) != 1 || u.rows[0].connection != "two" {
		t.Fatalf("drilldown crossed connections: %+v", u.rows)
	}
	press(u, tcell.KeyEscape, 0)
	if u.view != streamsView || u.selected().connection != "two" {
		t.Fatal("back lost selected stream")
	}
	u.Update(sample("one", 100))
	press(u, tcell.KeyRune, 's')
	if u.selected().connection != "two" {
		t.Fatal("refresh/sort moved selection to another resource")
	}
}

func TestFilterInputDoesNotTriggerShortcuts(t *testing.T) {
	u := New([]monitor.Snapshot{sample("query", 3), sample("reelify", 0)}, false, nil)
	press(u, tcell.KeyRune, '/')
	for _, ch := range "query" {
		press(u, tcell.KeyRune, ch)
	}
	press(u, tcell.KeyEnter, 0)
	if len(u.rows) != 1 || u.rows[0].connection != "query" {
		t.Fatalf("filter did not accept shortcut letters: %+v", u.rows)
	}
	press(u, tcell.KeyEscape, 0)
	if len(u.rows) != 2 {
		t.Fatal("escape did not clear filter")
	}
	press(u, tcell.KeyRune, 'c')
	u.chooser.Select(2, 0)
	press(u, tcell.KeyEnter, 0)
	if len(u.rows) != 1 || u.rows[0].connection != "reelify" {
		t.Fatal("connection selector did not scope results")
	}
}

func TestUnavailableDataIsVisibleAndEmptySnapshotsClearRows(t *testing.T) {
	s := sample("local", 3)
	u := New([]monitor.Snapshot{s}, false, nil)
	s.Status, s.Error = "offline", "connection refused"
	u.Update(s)
	if len(u.rows) != 1 || !strings.Contains(u.rows[0].cells[len(u.rows[0].cells)-1], "stale") {
		t.Fatal("retained row is not labeled stale")
	}
	if !strings.Contains(u.status.GetText(true), "connection refused") {
		t.Fatal("failure is hidden")
	}
	s.Status, s.Error, s.Streams = "online", "", nil
	u.Update(s)
	if len(u.rows) != 0 {
		t.Fatal("successful empty snapshot retained data")
	}
}

func TestTextCannotInjectTerminalMarkup(t *testing.T) {
	got := safe("[red]name\x1b[2J\nnext\x07")
	if strings.ContainsAny(got, "\x1b\n\x07") || tview.TaggedStringWidth(got) < len("[red]namenext") {
		t.Fatalf("unsafe text: %q", got)
	}
}

func TestSimulationScreenLiveUpdatesAndCancellation(t *testing.T) {
	u := New([]monitor.Snapshot{{Name: "test", Status: "connecting"}}, true, nil)
	screen := tcell.NewSimulationScreen("UTF-8")
	u.app.SetScreen(screen)
	frames := make(chan string, 20)
	u.app.SetAfterDrawFunc(func(s tcell.Screen) {
		var b strings.Builder
		w, h := s.Size()
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r, _, _, _ := s.GetContent(x, y)
				b.WriteRune(r)
			}
			b.WriteRune('\n')
		}
		select {
		case frames <- b.String():
		default:
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan monitor.Snapshot, 1)
	done := make(chan error, 1)
	go func() { done <- u.Run(ctx, updates) }()
	updates <- sample("test", 42)
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case frame := <-frames:
			if !strings.Contains(frame, "WORK") {
				continue
			}
			if !strings.Contains(frame, "DEMO") {
				t.Fatal("sample data not labeled")
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("UI did not stop")
			}
			return
		case <-timeout.C:
			t.Fatal("live update was not rendered")
		}
	}
}

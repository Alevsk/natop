package ui

import (
	"strings"
	"testing"

	"github.com/alevsk/natop/internal/config"
	"github.com/alevsk/natop/internal/monitor"
	"github.com/gdamore/tcell/v2"
)

func renderedFrame(t *testing.T, s tcell.SimulationScreen) string {
	t.Helper()
	var b strings.Builder
	w, h := s.Size()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, _, _, _ := s.GetContent(x, y)
			b.WriteRune(r)
		}
		b.WriteRune('\n')
	}
	return b.String()
}

func TestLongNamesAreNotTruncatedWhenThereIsRoom(t *testing.T) {
	longStream := strings.Repeat("stream-name-", 6)     // 72 chars
	longConsumer := strings.Repeat("consumer-name-", 5) // 70 chars
	snap := sample("prod", 1)
	snap.Streams[0].Info.Config.Name = longStream
	snap.Streams[0].Consumers[0].Name = longConsumer
	snap.Streams[0].Consumers[0].Stream = longStream

	m := monitor.NewManager(config.Config{})
	u := New(m)
	for _, s := range []monitor.Snapshot{snap} {
		u.Update(s)
	}
	screen := tcell.NewSimulationScreen("UTF-8")
	u.app.SetScreen(screen)
	screen.SetSize(220, 30)
	u.app.ForceDraw()

	if cell := u.table.GetCell(1, nameColumn[streamsView]); cell.MaxWidth != 0 || cell.Expansion == 0 {
		t.Fatalf("stream name column still capped: MaxWidth=%d Expansion=%d", cell.MaxWidth, cell.Expansion)
	}
	if frame := renderedFrame(t, screen); !strings.Contains(frame, longStream) {
		t.Fatalf("long stream name truncated in a wide terminal:\n%s", frame)
	}

	u.changeView(consumersView)
	u.app.ForceDraw()

	if cell := u.table.GetCell(1, nameColumn[consumersView]); cell.MaxWidth != 0 || cell.Expansion == 0 {
		t.Fatalf("consumer name column still capped: MaxWidth=%d Expansion=%d", cell.MaxWidth, cell.Expansion)
	}
	if frame := renderedFrame(t, screen); !strings.Contains(frame, longConsumer) {
		t.Fatalf("long consumer name truncated in a wide terminal:\n%s", frame)
	}
}

func TestNonIdentityColumnsStayCapped(t *testing.T) {
	m := monitor.NewManager(config.Config{})
	u := New(m)
	for _, s := range []monitor.Snapshot{sample("prod", 1)} {
		u.Update(s)
	}
	if cell := u.table.GetCell(1, 0); cell.MaxWidth != 36 || cell.Expansion != 0 {
		t.Fatalf("non-identity column lost its cap: MaxWidth=%d Expansion=%d", cell.MaxWidth, cell.Expansion)
	}
}

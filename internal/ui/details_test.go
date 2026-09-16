package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alevsk/natop/internal/monitor"
	"github.com/gdamore/tcell/v2"
	"github.com/nats-io/nats.go/jetstream"
)

func TestColorizeJSONCannotInjectTerminalMarkup(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	data, err := json.MarshalIndent(payload{Name: "[red]evil\x1b[2Jtext\x07"}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got := colorizeJSON(data)
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("control characters leaked into colorized JSON: %q", got)
	}
	if strings.Contains(got, "[red]evil") {
		t.Fatalf("bracket-tag-looking JSON string value was not escaped: %q", got)
	}
	if !strings.Contains(got, "evil") || !strings.Contains(got, "text") {
		t.Fatalf("literal JSON content was lost: %q", got)
	}
}

func TestColorizeJSONEscapesMaliciousKeys(t *testing.T) {
	data, err := json.MarshalIndent(map[string]string{"[green]key\x1b": "value"}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got := colorizeJSON(data)
	if strings.ContainsAny(got, "\x1b") {
		t.Fatalf("control character leaked from JSON key: %q", got)
	}
	if strings.Contains(got, "[green]key") {
		t.Fatalf("bracket-tag-looking JSON key was not escaped: %q", got)
	}
}

func TestColorizeJSONHighlightsKeysStringsAndNumbers(t *testing.T) {
	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	data, err := json.MarshalIndent(payload{Name: "WORK", Count: 42}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got := colorizeJSON(data)
	if !strings.Contains(got, colorTag(accent)+`"name"`) {
		t.Fatalf("object key not colorized: %q", got)
	}
	if !strings.Contains(got, colorTag(jsonString)+`"WORK"`) {
		t.Fatalf("string value not colorized: %q", got)
	}
	if !strings.Contains(got, colorTag(jsonNumber)+"42") {
		t.Fatalf("number value not colorized: %q", got)
	}
}

func TestExportWritesRawJSONForOpenDetails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "exports")
	snap := sample("prod", 5)
	u := New([]monitor.Snapshot{snap}, false, nil).SetExportDir(dir)
	u.table.Select(1, 0)
	press(u, tcell.KeyRune, 'd')
	if !u.overlay || u.exportData == nil {
		t.Fatal("details with metadata did not open")
	}
	press(u, tcell.KeyRune, 'e')

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly one exported file in %s, got %v (err %v)", dir, entries, err)
	}
	if !strings.Contains(entries[0].Name(), "prod") || !strings.Contains(entries[0].Name(), "WORK") {
		t.Fatalf("filename is not greppable by connection/stream: %s", entries[0].Name())
	}
	got, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	want, err := marshalDetails(snap.Streams[0].Info)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("exported file is not raw uncolored metadata:\n%s\nwant:\n%s", got, want)
	}
	escaped := "\\u003e"
	if !strings.Contains(string(got), "work.>") || strings.Contains(string(got), escaped) {
		t.Fatalf("exported file HTML-escaped the subject wildcard: %s", got)
	}
	if !strings.Contains(u.overlayView.GetTitle(), "Exported to") {
		t.Fatalf("no export confirmation shown in overlay title: %q", u.overlayView.GetTitle())
	}
}

func TestExportSkipsRowsWithoutMetadata(t *testing.T) {
	dir := t.TempDir()
	u := New([]monitor.Snapshot{sample("prod", 1)}, false, nil).SetExportDir(dir)
	u.changeView(connectionsView)
	u.table.Select(1, 0)
	press(u, tcell.KeyRune, 'd')
	if u.exportData != nil {
		t.Fatal("connection row unexpectedly has exportable metadata")
	}
	press(u, tcell.KeyRune, 'e')

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("export wrote a file for a row with no metadata: %v", entries)
	}
}

func TestDetailsOverlayDoesNotHTMLEscapeSubjects(t *testing.T) {
	u := New([]monitor.Snapshot{sample("prod", 5)}, false, nil) // stream subject is "work.>"
	u.table.Select(1, 0)
	press(u, tcell.KeyRune, 'd')
	text := u.overlayView.GetText(true)
	if !strings.Contains(text, "work.>") {
		t.Fatalf("colorized overlay lost the subject wildcard: %q", text)
	}
	if strings.Contains(text, "\\u003e") {
		t.Fatalf("colorized overlay HTML-escaped the subject wildcard: %q", text)
	}
}

func TestColorizeJSONAnnotatesKnownDurationField(t *testing.T) {
	data, err := marshalDetails(jetstream.ConsumerConfig{AckWait: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"ack_wait": 5000000000`) {
		t.Fatalf("raw export bytes lost the untouched nanosecond value: %s", data)
	}
	got := colorizeJSON(data)
	if !strings.Contains(got, "5000000000") || !strings.Contains(got, "(5s)") {
		t.Fatalf("colorized output missing raw value or human duration annotation: %q", got)
	}
	if !strings.Contains(string(data), `"ack_wait": 5000000000`) {
		t.Fatalf("colorizing mutated the raw export bytes: %s", data)
	}
}

func TestColorizeJSONAnnotatesDurationArrayElements(t *testing.T) {
	data, err := marshalDetails(jetstream.ConsumerConfig{BackOff: []time.Duration{5 * time.Second, 10 * time.Second, time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	got := colorizeJSON(data)
	for _, want := range []string{"5000000000", "(5s)", "10000000000", "(10s)", "60000000000", "(1m0s)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("backoff element annotation missing %q: %q", want, got)
		}
	}
}

func TestColorizeJSONAnnotatesOnlyZeroTimestamp(t *testing.T) {
	type payload struct {
		Zero time.Time `json:"zero"`
		Real time.Time `json:"real"`
	}
	real := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	data, err := marshalDetails(payload{Real: real})
	if err != nil {
		t.Fatal(err)
	}
	got := colorizeJSON(data)
	if !strings.Contains(got, "(never)") {
		t.Fatalf("zero timestamp not annotated: %q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, real.Format(time.RFC3339)) && strings.Contains(line, "(never)") {
			t.Fatalf("non-zero timestamp incorrectly annotated: %q", line)
		}
	}
}

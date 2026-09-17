package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (u *UI) showText(title, text string) {
	v := tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(true)
	v.SetBackgroundColor(background)
	v.SetTextColor(foreground)
	v.SetBorder(true).SetBorderColor(muted).SetTitleColor(accent).SetTitle(" " + safe(title) + " · Esc back ")
	v.SetText(text)
	 
	u.overlayView = v
	u.openOverlay(v, u.summary)
	 
}

func (u *UI) showDetails() {
	r := u.selected()
	if r == nil {
		return
	}
	snapshot := u.snapshots[r.connection]
	var b strings.Builder
	fmt.Fprintf(&b, " [#67e8f9::b]%s[-:-:-]\n %s\n Status: %s · Last successful refresh: %s\n", safe(r.connection), safe(snapshot.URL), safe(snapshot.Status), age(snapshot.Updated))
	if snapshot.Error != "" {
		fmt.Fprintf(&b, " [yellow]%s[-]\n", safe(snapshot.Error))
	}
	var value any
	title := r.connection
	if r.stream != nil {
		if r.stream.Error != "" {
			fmt.Fprintf(&b, " [yellow]Consumer metadata: %s[-]\n", safe(r.stream.Error))
		}
		info := r.stream.Info
		title = info.Config.Name
		fmt.Fprintf(&b, "\n [#67e8f9]Stream %s[-]\n Subjects: %s\n Storage: %s · Retention: %s · Replicas: %d\n Messages: %s · Bytes: %s · Metadata: %s\n", safe(title), safe(strings.Join(info.Config.Subjects, ", ")), info.Config.Storage, info.Config.Retention, info.Config.Replicas, number(info.State.Msgs), bytes(info.State.Bytes), age(r.stream.Updated))
		value = info
	}
	if r.consumer != nil {
		c := r.consumer
		title = c.Name
		fmt.Fprintf(&b, "\n [#67e8f9]Consumer %s[-]\n Pending: %s · Ack pending: %d · Redelivered: %d\n Ack policy: %s · Ack wait: %s · Metadata: %s\n Ack floor: consumer %d / stream %d\n", safe(c.Name), number(c.NumPending), c.NumAckPending, c.NumRedelivered, c.Config.AckPolicy, c.Config.AckWait, age(r.stream.ConsumersUpdated), c.AckFloor.Consumer, c.AckFloor.Stream)
		value = c
	}
	u.exportConnection, u.exportName, u.exportData = "", "", nil
	if value != nil {
		data, err := marshalDetails(value)
		if err == nil {
			b.WriteString("\n [#67e8f9]Full metadata (snapshot when opened; reopen to refresh · e to export)[-]\n")
			b.WriteString(colorizeJSON(data))
			b.WriteString("\n")
			u.exportConnection, u.exportName, u.exportData = r.connection, title, data
		}
	} else {
		fmt.Fprintf(&b, "\n Streams in last snapshot: %d\n\n If a Docker hostname cannot resolve, run on its Docker network or use\n a published host port. Press c to change the connection filter.\n", len(snapshot.Streams))
	}
	u.showText(title, b.String())
}

// marshalDetails renders value as indented JSON exactly like
// json.MarshalIndent(value, "", "  "), except HTML-escaping is disabled so
// subjects/filters containing '<', '>', or '&' survive intact in both the
// colorized overlay and whatever gets exported to disk.
func marshalDetails(value any) ([]byte, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return []byte(strings.TrimSuffix(buf.String(), "\n")), nil
}

// exportDetails writes the raw (uncolored) JSON captured by the open details
// overlay to a file, so an operator can attach it to an incident ticket or
// paste it into Slack without retyping the terminal contents.
func (u *UI) exportDetails() {
	if err := os.MkdirAll(u.exportDir, 0o700); err != nil {
		u.reportExport("Export failed: " + err.Error())
		return
	}
	name := fmt.Sprintf("%s-%s-%d.json", sanitizeFilename(u.exportConnection), sanitizeFilename(u.exportName), time.Now().Unix())
	path := filepath.Join(u.exportDir, name)
	if err := os.WriteFile(path, u.exportData, 0o600); err != nil {
		u.reportExport("Export failed: " + err.Error())
		return
	}
	u.reportExport("Exported to " + path)
}

// reportExport updates the open overlay's border title with a brief result,
// since the overlay covers the status line where other feedback would go.
func (u *UI) reportExport(message string) {
	if u.overlayView == nil {
		return
	}
	u.overlayView.SetTitle(" " + safe(u.exportName) + " · Esc back · " + safe(message) + " ")
}

var filenameUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeFilename(s string) string {
	return strings.Trim(filenameUnsafe.ReplaceAllString(s, "_"), "_")
}

var (
	jsonString = tcell.NewHexColor(0x86efac) // string values
	jsonNumber = tcell.NewHexColor(0x93c5fd) // numeric values
)

var (
	jsonLineRe     = regexp.MustCompile(`^(\s*)(?:("(?:[^"\\]|\\.)*")(\s*:\s*))?(.*)$`)
	jsonStringRe   = regexp.MustCompile(`^"(?:[^"\\]|\\.)*",?$`)
	jsonNumberRe   = regexp.MustCompile(`^-?\d+(\.\d+)?([eE][+-]?\d+)?,?$`)
	jsonLiteralRe  = regexp.MustCompile(`^(?:true|false|null),?$`)
	jsonZeroTimeRe = regexp.MustCompile(`^"0001-01-01T00:00:00Z",?$`)
)

// durationJSONKeys are the json field names of every time.Duration field on
// jetstream.StreamConfig, jetstream.StreamState, jetstream.ConsumerConfig,
// jetstream.ConsumerInfo, and the nested types they embed (StreamConsumerLimits,
// ClusterInfo/PeerInfo, StreamSourceInfo), per nats-io/nats.go@v1.53.1's
// jetstream package. Matched by bare key name rather than JSON path, so this
// is a display-only annotation and never touches the exported bytes; the
// trade-off is that an unrelated field sharing one of these names (e.g. a
// subject literally named "active") would also get annotated.
var durationJSONKeys = map[string]bool{
	"max_age":                   true, // StreamConfig.MaxAge
	"duplicate_window":          true, // StreamConfig.Duplicates
	"subject_delete_marker_ttl": true, // StreamConfig.SubjectDeleteMarkerTTL
	"inactive_threshold":        true, // ConsumerConfig / StreamConsumerLimits InactiveThreshold
	"active":                    true, // PeerInfo.Active, StreamSourceInfo.Active
	"ack_wait":                  true, // ConsumerConfig.AckWait
	"max_expires":               true, // ConsumerConfig.MaxRequestExpires
	"priority_timeout":          true, // ConsumerConfig.PinnedTTL
	"idle_heartbeat":            true, // ConsumerConfig.IdleHeartbeat
	"pause_remaining":           true, // ConsumerInfo.PauseRemaining
}

// durationArrayJSONKeys are []time.Duration fields (currently only
// ConsumerConfig.BackOff); every bare-number line between the matching "["
// and "]" gets the same annotation as a scalar duration field.
var durationArrayJSONKeys = map[string]bool{
	"backoff": true, // ConsumerConfig.BackOff
}

// colorizeJSON syntax-highlights already-indented json.MarshalIndent-style
// output for the details overlay. Indentation and punctuation come from Go's
// own encoder and are structural, not attacker-controlled, so they are
// written verbatim; every other token originates in the untrusted JSON
// payload and is passed through safe() before being wrapped in our own
// (trusted) color tags, exactly like the plain-text rendering it replaces.
// Known duration fields and the zero time.Time literal get a dimmed,
// additive human-readable annotation; the raw value is never altered, since
// this same []byte is also written verbatim by the export feature.
func colorizeJSON(data []byte) string {
	lines := strings.Split(string(data), "\n")
	inDurationArray := false
	for i, line := range lines {
		lines[i] = " " + colorizeJSONLine(line, &inDurationArray)
	}
	return strings.Join(lines, "\n")
}

func colorizeJSONLine(line string, inDurationArray *bool) string {
	m := jsonLineRe.FindStringSubmatch(line)
	if m == nil {
		return safe(line)
	}
	indent, key, sep, value := m[1], m[2], m[3], m[4]
	var b strings.Builder
	b.WriteString(indent)
	if key != "" {
		b.WriteString(colorTag(accent))
		b.WriteString(safe(key))
		b.WriteString("[-]")
		b.WriteString(sep)
	}
	b.WriteString(colorizeJSONValue(value))

	name := strings.Trim(key, `"`)
	switch {
	case durationArrayJSONKeys[name] && value == "[":
		*inDurationArray = true
	case *inDurationArray && (value == "]" || value == "],"):
		*inDurationArray = false
	case key != "" && durationJSONKeys[name] && jsonNumberRe.MatchString(value):
		b.WriteString(durationAnnotation(value))
	case key == "" && *inDurationArray && jsonNumberRe.MatchString(value):
		b.WriteString(durationAnnotation(value))
	case jsonZeroTimeRe.MatchString(value):
		b.WriteString(annotate("never"))
	}
	return b.String()
}

// annotate renders a dimmed "(text)" suffix appended after an already
// colorized value, e.g. "5000000000  [muted](5s)[-]".
func annotate(text string) string {
	return "  " + colorTag(muted) + "(" + text + ")[-]"
}

func durationAnnotation(value string) string {
	n, err := strconv.ParseInt(strings.TrimSuffix(value, ","), 10, 64)
	if err != nil {
		return ""
	}
	return annotate(time.Duration(n).String())
}

func colorizeJSONValue(v string) string {
	switch {
	case v == "":
		return ""
	case jsonStringRe.MatchString(v):
		return colorTag(jsonString) + safe(v) + "[-]"
	case jsonNumberRe.MatchString(v):
		return colorTag(jsonNumber) + safe(v) + "[-]"
	case jsonLiteralRe.MatchString(v):
		return colorTag(muted) + safe(v) + "[-]"
	default:
		// Punctuation only ("{", "},", "[", etc.); still untrusted, still escaped.
		return safe(v)
	}
}

func colorTag(c tcell.Color) string { return fmt.Sprintf("[#%06x]", c.Hex()) }

func (u *UI) showHelp() {
	u.showText("Help", ` [#67e8f9::b]natop[-:-:-] · JetStream at a glance

 [#67e8f9]1 / 2 / 3[-]     Streams / all consumers / connections
 [#67e8f9]↑ ↓ or j k[-]    Move selection
 [#67e8f9]← → or h l[-]    Scroll horizontally
 [#67e8f9]g / G[-]         First / last row
 [#67e8f9]PgUp / PgDn[-]   Scroll a page
 [#67e8f9]Enter[-]         Open stream's consumers, or view consumer's messages
 [#67e8f9]d[-]             Inspect selected row's full metadata
 [#67e8f9]m[-]             View pending messages for selected stream or consumer
 [#67e8f9]e[-]             While details are open, export its raw JSON to disk
 [#67e8f9]/[-]             Filter rows; Enter keeps filter, Esc clears it
 [#67e8f9]![-]             Toggle issues-only: hide healthy rows in this view
 [#67e8f9]c[-]             Select one connection or all connections
 [#67e8f9]s[-]             Cycle sort columns (numeric sorts are descending)
 [#67e8f9]r[-]             Refresh now
 [#67e8f9]Esc[-]           Close overlay, clear filter, or return to streams
 [#67e8f9]?[-]             This help
 [#67e8f9]q / Ctrl-C[-]    Quit

 [#67e8f9]Reading the numbers[-]
 Messages       Stored messages, not necessarily undelivered work.
 Pending        Messages not yet delivered to this consumer.
 Ack pending    Delivered messages awaiting acknowledgment.
 Redelivered    Messages redelivered and still awaiting acknowledgment.
 Deleted        Gaps inside the stored stream sequence range.
 Lost           — means the client API does not expose this metric.

 Different consumers may observe the same messages; their pending counts
 are not added together as a unique queue depth. Push consumers and paused
 consumers are identified in the consumer table.

 [#67e8f9]Freshness[-]
 Live rows show the last successful metadata response. Stale rows retain
 earlier data after a failure. Partial streams have current stream metadata
 but unavailable consumer metadata. Errors and last-refresh times remain
 visible. Details are a snapshot when opened; reopen to update them.

 Monitoring reads stream/consumer metadata and never consumes or acknowledges
 application messages. This view covers JetStream, not Core NATS queue groups.
`)
}

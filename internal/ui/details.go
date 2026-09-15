package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rivo/tview"
)

func (u *UI) showText(title, text string) {
	v := tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(true)
	v.SetBackgroundColor(background)
	v.SetTextColor(foreground)
	v.SetBorder(true).SetBorderColor(muted).SetTitleColor(accent).SetTitle(" " + safe(title) + " · Esc back ")
	v.SetText(text)
	u.overlay = true
	u.pages.AddPage("overlay", v, true, true)
	u.app.SetFocus(v)
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
	if value != nil {
		data, err := json.MarshalIndent(value, "", "  ")
		if err == nil {
			b.WriteString("\n [#67e8f9]Full metadata (snapshot when opened; reopen to refresh)[-]\n")
			// Preserve our own formatting, but escape each untrusted JSON line.
			for _, line := range strings.Split(string(data), "\n") {
				b.WriteString(" " + safe(line) + "\n")
			}
		}
	} else {
		fmt.Fprintf(&b, "\n Streams in last snapshot: %d\n\n If a Docker hostname cannot resolve, run on its Docker network or use\n a published host port. Press c to change the connection filter.\n", len(snapshot.Streams))
	}
	u.showText(title, b.String())
}

func (u *UI) showHelp() {
	u.showText("Help", ` [#67e8f9::b]nats-tui[-:-:-] · JetStream at a glance

 [#67e8f9]1 / 2 / 3[-]     Streams / all consumers / connections
 [#67e8f9]↑ ↓ or j k[-]    Move selection
 [#67e8f9]← → or h l[-]    Scroll horizontally
 [#67e8f9]g / G[-]         First / last row
 [#67e8f9]PgUp / PgDn[-]   Scroll a page
 [#67e8f9]Enter[-]         Open a stream's consumers or selected row's details
 [#67e8f9]d[-]             Inspect selected row's full metadata
 [#67e8f9]/[-]             Filter rows; Enter keeps filter, Esc clears it
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

package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alevsk/natop/internal/monitor"
	"github.com/gdamore/tcell/v2"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/rivo/tview"
)

// nameColumn is each view's identity column: STREAM for streams,
// CONSUMER (not the parent STREAM) for consumers, SERVER for connections.
// It gets SetExpansion instead of the fixed SetMaxWidth given to every
// other column.
var nameColumn = map[view]int{streamsView: 1, consumersView: 2, connectionsView: 1}

type row struct {
	id, connection string
	cells          []string
	values         []uint64
	stream         *monitor.Stream
	consumer       *jetstream.ConsumerInfo
}

func (u *UI) selected() *row {
	if u.table == nil {
		return nil
	}
	i, _ := u.table.GetSelection()
	if i < 1 || i > len(u.rows) {
		return nil
	}
	return &u.rows[i-1]
}

func (u *UI) selectID(id string) {
	for i, r := range u.rows {
		if r.id == id {
			u.table.Select(i+1, 0)
			return
		}
	}
}

func (u *UI) orderedSnapshots() []monitor.Snapshot {
	values := make([]monitor.Snapshot, 0, len(u.snapshots))
	for _, s := range u.snapshots {
		values = append(values, s)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return values
}

func (u *UI) sortCount() int {
	if u.view == connectionsView {
		return 3
	}
	return 4
}

// connectionRank orders statuses worst-first: online is healthy, everything
// else needs attention in increasing severity.
func connectionRank(status string) uint64 {
	switch status {
	case "online":
		return 0
	case "connecting":
		return 1
	case "partial":
		return 2
	default:
		return 3
	}
}

func staleness(t time.Time) uint64 {
	if d := time.Since(t); d > 0 {
		return uint64(d)
	}
	return 0
}

// isIssueState matches the same "stale"/"partial" substrings used to color a
// row's state cell yellow, so issues-only filtering agrees with what's shown.
func isIssueState(state string) bool {
	return strings.Contains(state, "stale") || strings.Contains(state, "partial")
}

func (u *UI) render() {
	selectedID := ""
	oldRow, _ := u.table.GetSelection()
	if r := u.selected(); r != nil {
		selectedID = r.id
	}
	u.rows = nil
	var headers []string
	title := "Streams"
	sortName := "name"
	switch u.view {
	case streamsView:
		headers = []string{"CONNECTION", "STREAM", "STORAGE", "CONSUMERS", "MESSAGES", "BYTES", "LOST", "DELETED", "REPLICAS", "STATE"}
		sortName = []string{"name", "messages ↓", "bytes ↓", "consumers ↓"}[u.sort]
	case consumersView:
		title = "Consumers"
		headers = []string{"CONNECTION", "STREAM", "CONSUMER", "MODE", "ACK", "WAIT", "PENDING", "ACK PENDING", "REDELIVERED", "STATE"}
		sortName = []string{"name", "pending ↓", "ack pending ↓", "redelivered ↓"}[u.sort]
	case connectionsView:
		title = "Connections"
		headers = []string{"CONNECTION", "SERVER", "STATUS", "LAST SUCCESS", "ERROR"}
		sortName = []string{"name", "status ↓", "stale ↓"}[u.sort]
	}
	online, issues := 0, 0
	for _, snapshot := range u.orderedSnapshots() {
		if snapshot.Status == "online" {
			online++
		} else if snapshot.Status != "connecting" {
			issues++
		}
		if u.connection != "" && snapshot.Name != u.connection {
			continue
		}
		if u.view == connectionsView {
			u.addRow(row{id: snapshot.Name, connection: snapshot.Name, cells: []string{snapshot.Name, snapshot.URL, snapshot.Status, age(snapshot.Updated), snapshot.Error},
				values: []uint64{connectionRank(snapshot.Status), staleness(snapshot.Updated)}})
			continue
		}
		for _, stream := range snapshot.Streams {
			if stream.Info == nil {
				continue
			}
			name := stream.Info.Config.Name
			if u.scopeStream != "" && (u.scopeConnection != snapshot.Name || u.scopeStream != name) {
				continue
			}
			state := "live"
			if snapshot.Status != "online" && snapshot.Status != "partial" {
				state = "stale"
			} else if stream.Error != "" {
				state = "partial"
			}
			id := snapshot.Name + "\x00" + name
			if u.view == streamsView {
				info := stream.Info
				// The current NATS Go API does not expose a lost-message count.
				// Show unknown instead of reporting an unverified zero.
				u.addRow(row{id: id, connection: snapshot.Name, stream: &stream, cells: []string{
					snapshot.Name, name, info.Config.Storage.String(), strconv.Itoa(info.State.Consumers), number(info.State.Msgs), bytes(info.State.Bytes), "—", strconv.Itoa(info.State.NumDeleted), strconv.Itoa(info.Config.Replicas), state,
				}, values: []uint64{info.State.Msgs, info.State.Bytes, uint64(max(0, info.State.Consumers))}})
				continue
			}
			if stream.Error != "" {
				state = "stale"
			}
			for _, c := range stream.Consumers {
				mode := "Pull"
				if c.Config.DeliverSubject != "" {
					mode = "Push"
				}
				consumerState := state
				if c.Paused {
					consumerState += "/paused"
				}
				u.addRow(row{id: id + "\x00" + c.Name, connection: snapshot.Name, stream: &stream, consumer: c, cells: []string{
					snapshot.Name, name, c.Name, mode, c.Config.AckPolicy.String(), c.Config.AckWait.String(), number(c.NumPending), strconv.Itoa(c.NumAckPending), strconv.Itoa(c.NumRedelivered), consumerState,
				}, values: []uint64{c.NumPending, uint64(max(0, c.NumAckPending)), uint64(max(0, c.NumRedelivered))}})
			}
		}
	}
	sort.SliceStable(u.rows, func(i, j int) bool {
		a, b := u.rows[i], u.rows[j]
		if u.sort > 0 && a.values[u.sort-1] != b.values[u.sort-1] {
			return a.values[u.sort-1] > b.values[u.sort-1]
		}
		return a.id < b.id
	})
	u.table.Clear()
	for j, h := range headers {
		u.table.SetCell(0, j, tview.NewTableCell(" "+h+" ").SetSelectable(false).SetTextColor(accent).SetAttributes(tcell.AttrBold))
	}
	for i, r := range u.rows {
		for j, value := range r.cells {
			color := foreground
			if j == len(r.cells)-1 && isIssueState(value) {
				color = tcell.ColorYellow
			}
			cell := tview.NewTableCell(" " + safe(value) + " ").SetTextColor(color)
			if j == nameColumn[u.view] {
				// The identity column grows into unused terminal width instead of
				// hard-capping at 36; a narrow terminal still shrinks it gracefully
				// (and the h/l horizontal scroll already documented in help covers
				// the rest), so there is no need to ellipsis-truncate real names.
				cell.SetExpansion(1)
			} else {
				cell.SetMaxWidth(36)
			}
			u.table.SetCell(i+1, j, cell)
		}
	}
	if len(u.rows) > 0 {
		u.table.Select(max(1, min(oldRow, len(u.rows))), 0)
		u.selectID(selectedID)
	}
	if u.scopeStream != "" {
		title += " / " + safe(u.scopeConnection) + " / " + safe(u.scopeStream)
	}
	u.table.SetTitle(" " + title + " ")
	mode := "LIVE"
	if u.demo {
		mode = "DEMO · sample data"
	}
	var totalStreams, totalConsumers int
	for _, s := range u.snapshots {
		totalStreams += len(s.Streams)
		for _, st := range s.Streams {
			if st.Info != nil {
				totalConsumers += st.Info.State.Consumers
			}
		}
	}
	u.header.SetText(fmt.Sprintf(" [::b][#67e8f9]natop[-:-:-]  [gray]%s[-]     [green]%d/%d online[-]  [yellow]%d issues[-]\n [#67e8f9]%d[-] Streams   [#67e8f9]%d[-] Consumers   [#67e8f9]%d[-] Connections", mode, online, len(u.snapshots), issues, totalStreams, totalConsumers, len(u.snapshots)))
	connection := "all connections"
	if u.connection != "" {
		connection = safe(u.connection)
	}
	filter := ""
	if u.filter != "" {
		filter = " · filter: " + safe(u.filter)
	}
	onlyIssues := ""
	if u.onlyIssues {
		onlyIssues = " · issues only"
	}
	u.summary.SetText(fmt.Sprintf(" [gray]%s · %d rows · sort: %s%s%s", connection, len(u.rows), sortName, filter, onlyIssues))
	u.hints.SetText(" [#67e8f9]Enter[-] drill down [#67e8f9]d[-] details ([#67e8f9]e[-] export) [#67e8f9]/[-] filter [#67e8f9]![-] issues [#67e8f9]c[-] connection [#67e8f9]s[-] sort [#67e8f9]r[-] refresh [#67e8f9]?[-] help [#67e8f9]q[-] quit")
	u.renderStatus()
}

func (u *UI) addRow(r row) {
	if u.filter != "" && !strings.Contains(strings.ToLower(strings.Join(r.cells, " ")), strings.ToLower(u.filter)) {
		return
	}
	if u.onlyIssues {
		if u.view == connectionsView {
			if r.cells[2] == "online" {
				return
			}
		} else if !isIssueState(r.cells[len(r.cells)-1]) {
			return
		}
	}
	u.rows = append(u.rows, r)
}

func (u *UI) openSelected() {
	r := u.selected()
	if r == nil {
		return
	}
	if u.view == streamsView {
		u.backID = r.id
		u.scopeConnection, u.scopeStream = r.connection, r.stream.Info.Config.Name
		u.view, u.sort = consumersView, 0
		u.input.SetText("")
		u.table.ScrollToBeginning().Select(1, 0)
		u.render()
	} else if u.view == consumersView {
		u.showMessages()
	} else {
		u.showDetails()
	}
}

func number(n uint64) string {
	s := strconv.FormatUint(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func bytes(n uint64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	f := float64(n)
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	for _, unit := range units {
		f /= 1024
		if f < 1024 || unit == "EiB" {
			return fmt.Sprintf("%.1f %s", f, unit)
		}
	}
	return ""
}

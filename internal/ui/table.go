package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/alevsk/natop/internal/monitor"
	"github.com/gdamore/tcell/v2"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/rivo/tview"
)

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
		return 1
	}
	return 4
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
			u.addRow(row{id: snapshot.Name, connection: snapshot.Name, cells: []string{snapshot.Name, snapshot.URL, snapshot.Status, age(snapshot.Updated), snapshot.Error}})
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
			if j == len(r.cells)-1 && (strings.Contains(value, "stale") || strings.Contains(value, "partial")) {
				color = tcell.ColorYellow
			}
			cell := tview.NewTableCell(" " + safe(value) + " ").SetTextColor(color).SetMaxWidth(36)
			if j == 1 {
				cell.SetExpansion(1)
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
	u.header.SetText(fmt.Sprintf(" [::b][#67e8f9]natop[-:-:-]  [gray]%s[-]     [green]%d/%d online[-]  [yellow]%d issues[-]\n [#67e8f9]1[-] Streams   [#67e8f9]2[-] Consumers   [#67e8f9]3[-] Connections", mode, online, len(u.snapshots), issues))
	connection := "all connections"
	if u.connection != "" {
		connection = safe(u.connection)
	}
	filter := ""
	if u.filter != "" {
		filter = " · filter: " + safe(u.filter)
	}
	u.summary.SetText(fmt.Sprintf(" [gray]%s · %d rows · sort: %s%s", connection, len(u.rows), sortName, filter))
	u.hints.SetText(" [#67e8f9]Enter[-] open [#67e8f9]d[-] details [#67e8f9]/[-] filter [#67e8f9]c[-] connection [#67e8f9]s[-] sort [#67e8f9]r[-] refresh [#67e8f9]?[-] help [#67e8f9]q[-] quit")
	u.renderStatus()
}

func (u *UI) addRow(r row) {
	if u.filter != "" && !strings.Contains(strings.ToLower(strings.Join(r.cells, " ")), strings.ToLower(u.filter)) {
		return
	}
	u.rows = append(u.rows, r)
}

func (u *UI) openSelected() {
	r := u.selected()
	if r == nil {
		return
	}
	if u.view != streamsView {
		u.showDetails()
		return
	}
	u.backID = r.id
	u.scopeConnection, u.scopeStream = r.connection, r.stream.Info.Config.Name
	u.view, u.sort = consumersView, 0
	u.input.SetText("")
	u.table.ScrollToBeginning().Select(1, 0)
	u.render()
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

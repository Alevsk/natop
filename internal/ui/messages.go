package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/rivo/tview"
)

func (u *UI) showMessages() {
	r := u.selected()
	if r == nil {
		return
	}
	if r.stream == nil && r.consumer == nil {
		return
	}

	streamName := ""
	var startSeq uint64 = 1
	title := ""

	if r.consumer != nil {
		c := r.consumer
		streamName = c.Stream
		startSeq = c.AckFloor.Stream + 1
		title = fmt.Sprintf("Messages for %s", c.Name)
	} else if r.stream != nil {
		s := r.stream.Info
		streamName = s.Config.Name
		startSeq = s.State.LastSeq
		if startSeq > 50 {
			startSeq -= 49
		} else {
			startSeq = 1
		}
		title = fmt.Sprintf("Recent messages for %s", streamName)
	}

	u.openMessagesBrowser(title, r.connection, streamName, startSeq, r.consumer)
}

func (u *UI) openMessagesBrowser(title, connection, streamName string, initialSeq uint64, consumer *jetstream.ConsumerInfo) {
	flex := tview.NewFlex().SetDirection(tview.FlexRow)

	table := newTable()
	table.SetTitle(fmt.Sprintf(" %s (from seq %d) · Esc back · Tab focus · n next 50 · p prev 50 ", title, initialSeq))

	detail := tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetScrollable(true)
	detail.SetBackgroundColor(background)
	detail.SetTextColor(foreground)
	detail.SetBorder(true).SetBorderColor(muted).SetTitleColor(accent).SetTitle(" Payload (JSON formatted if valid) ")

	flex.AddItem(table, 0, 1, true)
	flex.AddItem(detail, 0, 1, false)

	u.overlay = true
	u.overlayView = nil
	u.exportData = nil
	
	summary := tview.NewTextView().SetDynamicColors(true)
	summary.SetBackgroundColor(background)
	summary.SetTextColor(foreground)
	u.openOverlay(flex, summary)

	headers := []string{"SEQUENCE", "SUBJECT", "TIME", "SIZE", "HEADERS"}
	if consumer != nil {
		headers = append(headers, "STATE")
	}
	for j, h := range headers {
		table.SetCell(0, j, tview.NewTableCell(" "+h+" ").SetSelectable(false).SetTextColor(accent).SetAttributes(tcell.AttrBold))
	}

	var currentMsgs []*jetstream.RawStreamMsg

	updateDetail := func(row int) {
		if row < 1 || row > len(currentMsgs) {
			detail.SetText("")
			return
		}
		m := currentMsgs[row-1]
		var b strings.Builder
		if len(m.Header) > 0 {
			b.WriteString(" [#67e8f9]Headers[-]\n")
			for k, v := range m.Header {
				fmt.Fprintf(&b, "  %s: %s\n", safe(k), safe(strings.Join(v, ", ")))
			}
			b.WriteString("\n")
		}
		
		dataStr := string(m.Data)
		if len(m.Data) == 0 {
			b.WriteString(" (empty payload)")
		} else {
			var parsed any
			if err := json.Unmarshal(m.Data, &parsed); err == nil {
				formatted, merr := marshalDetails(parsed)
				if merr == nil {
					b.WriteString(colorizeJSON(formatted))
				} else {
					b.WriteString(safe(dataStr))
				}
			} else {
				if len(dataStr) > 5000 {
					b.WriteString(safe(dataStr[:5000]) + "\n\n... (truncated)")
				} else {
					b.WriteString(safe(dataStr))
				}
			}
		}
		detail.SetText(b.String())
		detail.ScrollToBeginning()
	}

	table.SetSelectionChangedFunc(func(row, column int) {
		updateDetail(row)
	})

	renderPage := func(msgs []*jetstream.RawStreamMsg, notice string) {
		table.Clear()
		for j, h := range headers {
			table.SetCell(0, j, tview.NewTableCell(" "+h+" ").SetSelectable(false).SetTextColor(accent).SetAttributes(tcell.AttrBold))
		}
		connStr := connection
		if connStr == "" {
			connStr = "all connections"
		}
		if len(msgs) == 0 {
			table.SetCell(1, 0, tview.NewTableCell(" No messages to display. ").SetTextColor(muted).SetSelectable(false))
			detail.SetText("")
			summary.SetText(fmt.Sprintf(" %s · 0 rows", connStr))
			return
		}
		
		summary.SetText(fmt.Sprintf(" %s · %d rows", connStr, len(msgs)))
		
		for i, m := range msgs {
			hasHeaders := "No"
			if len(m.Header) > 0 {
				hasHeaders = "Yes"
			}
			table.SetCell(i+1, 0, tview.NewTableCell(" "+fmt.Sprintf("%d", m.Sequence)+" ").SetTextColor(foreground))
			table.SetCell(i+1, 1, tview.NewTableCell(" "+safe(m.Subject)+" ").SetTextColor(foreground))
			table.SetCell(i+1, 2, tview.NewTableCell(" "+m.Time.Format("2006-01-02 15:04:05")+" ").SetTextColor(foreground))
			table.SetCell(i+1, 3, tview.NewTableCell(" "+bytesFmt(uint64(len(m.Data)))+" ").SetTextColor(foreground))
			table.SetCell(i+1, 4, tview.NewTableCell(" "+hasHeaders+" ").SetTextColor(foreground))
			if consumer != nil {
				state := "Pending"
				color := tcell.ColorYellow
				if m.Sequence <= consumer.AckFloor.Stream {
					state = "Acked"
					color = tcell.ColorGreen
				} else if m.Sequence <= consumer.Delivered.Stream {
					state = "Ack Pending"
					color = tcell.ColorOrange
				}
				table.SetCell(i+1, 5, tview.NewTableCell(" "+state+" ").SetTextColor(color))
			}
		}
		
		titleSuffix := ""
		if notice != "" {
			titleSuffix = fmt.Sprintf(" [yellow](%s)[-] ", notice)
		}
		table.SetTitle(fmt.Sprintf(" %s (seq %d - %d) · Esc back · Tab focus · n next 50 · p prev 50 %s", title, msgs[0].Sequence, msgs[len(msgs)-1].Sequence, titleSuffix))
		
		table.Select(1, 0)
		updateDetail(1)
	}

	updateTable := func(msgs []*jetstream.RawStreamMsg, err error, dir string) {
		if !u.overlay {
			return
		}
		if err != nil {
			table.Clear()
			table.SetCell(1, 0, tview.NewTableCell(" Error: "+safe(err.Error())).SetTextColor(tcell.ColorRed).SetSelectable(false))
			return
		}

		if len(msgs) == 0 {
			notice := "End of stream reached"
			if dir == "back" {
				notice = "Start of stream reached"
			}
			renderPage(currentMsgs, notice)
			return
		}

		currentMsgs = msgs
		renderPage(currentMsgs, "")
	}

	fetch := func(start uint64) {
		table.Clear()
		for j, h := range headers {
			table.SetCell(0, j, tview.NewTableCell(" "+h+" ").SetSelectable(false).SetTextColor(accent).SetAttributes(tcell.AttrBold))
		}
		table.SetCell(1, 0, tview.NewTableCell(" Fetching... ").SetTextColor(tcell.ColorYellow).SetSelectable(false))
		detail.SetText("")

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			msgs, err := u.manager.PeekMessages(ctx, connection, streamName, start, 50)
			u.app.QueueUpdateDraw(func() { updateTable(msgs, err, "forward") })
		}()
	}

	fetchBackward := func(end uint64) {
		table.Clear()
		for j, h := range headers {
			table.SetCell(0, j, tview.NewTableCell(" "+h+" ").SetSelectable(false).SetTextColor(accent).SetAttributes(tcell.AttrBold))
		}
		table.SetCell(1, 0, tview.NewTableCell(" Fetching previous... ").SetTextColor(tcell.ColorYellow).SetSelectable(false))
		detail.SetText("")

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			msgs, err := u.manager.PeekMessagesBackward(ctx, connection, streamName, end, 50)
			u.app.QueueUpdateDraw(func() { updateTable(msgs, err, "back") })
		}()
	}

	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyRune {
			switch event.Rune() {
			case 'n':
				if len(currentMsgs) > 0 {
					lastSeq := currentMsgs[len(currentMsgs)-1].Sequence
					fetch(lastSeq + 1)
				}
				return nil
			case 'p':
				if len(currentMsgs) > 0 {
					firstSeq := currentMsgs[0].Sequence
					if firstSeq > 1 {
						fetchBackward(firstSeq)
					}
				}
				return nil
			}
		}
		if event.Key() == tcell.KeyTab {
			u.app.SetFocus(detail)
			return nil
		}
		return event
	})

	detail.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab {
			u.app.SetFocus(table)
			return nil
		}
		return event
	})

	fetch(initialSeq)
}

func bytesFmt(n uint64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	f := float64(n)
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	for _, unit := range units {
		f /= 1024
		if f < 1024 || unit == "TiB" {
			return fmt.Sprintf("%.1f %s", f, unit)
		}
	}
	return ""
}

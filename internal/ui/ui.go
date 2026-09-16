// Package ui provides the keyboard-driven terminal dashboard.
package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/alevsk/natop/internal/monitor"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type view int

const (
	streamsView view = iota
	consumersView
	connectionsView
)

type UI struct {
	app                                              *tview.Application
	pages                                            *tview.Pages
	layout                                           *tview.Flex
	header, summary, status, hints                   *tview.TextView
	table, chooser                                   *tview.Table
	overlayView                                      *tview.TextView
	input                                            *tview.InputField
	snapshots                                        map[string]monitor.Snapshot
	rows                                             []row
	view                                             view
	connection, scopeConnection, scopeStream, backID string
	filter                                           string
	sort                                             int
	overlay                                          bool
	filtering                                        bool
	demo                                             bool
	refresh                                          func()
	wakePending                                      atomic.Bool
	exportDir                                        string
	exportConnection, exportName                     string
	exportData                                       []byte
}

var (
	background = tcell.NewHexColor(0x101820)
	foreground = tcell.NewHexColor(0xcbd5e1)
	accent     = tcell.NewHexColor(0x67e8f9)
	muted      = tcell.NewHexColor(0x94a3b8)
)

func New(initial []monitor.Snapshot, demo bool, refresh func()) *UI {
	u := &UI{app: tview.NewApplication(), snapshots: map[string]monitor.Snapshot{}, demo: demo, refresh: refresh}
	for _, s := range initial {
		u.snapshots[s.Name] = s
	}
	text := func() *tview.TextView {
		v := tview.NewTextView().SetDynamicColors(true)
		v.SetBackgroundColor(background)
		v.SetTextColor(foreground)
		return v
	}
	u.header, u.summary, u.status, u.hints = text(), text(), text(), text()
	u.header.SetTextAlign(tview.AlignLeft)
	u.status.SetWrap(true)
	u.table = newTable()
	u.table.SetSelectedFunc(func(_, _ int) { u.openSelected() })
	u.table.SetSelectionChangedFunc(func(_, _ int) { u.renderStatus() })
	u.input = tview.NewInputField().SetLabel(" / ").SetFieldWidth(0).SetFieldBackgroundColor(background).SetFieldTextColor(accent).SetLabelColor(accent)
	u.input.SetBackgroundColor(background)
	u.input.SetChangedFunc(func(s string) { u.filter = s; u.render() })
	u.input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			u.input.SetText("")
		}
		u.closeFilter()
	})
	u.layout = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(u.header, 2, 0, false).AddItem(u.summary, 1, 0, false).
		AddItem(u.table, 0, 1, true).AddItem(u.status, 2, 0, false).AddItem(u.hints, 1, 0, false)
	u.layout.SetBackgroundColor(background)
	u.pages = tview.NewPages().AddPage("main", u.layout, true, true)
	u.app.SetRoot(u.pages, true).SetFocus(u.table).EnablePaste(true).SetInputCapture(u.key)
	u.render()
	return u
}

// SetExportDir overrides where the export key ('e' in details) writes metadata
// JSON. Callers must set it before Run; tests use a t.TempDir() here instead
// of hardcoding the OS temp directory.
func (u *UI) SetExportDir(dir string) *UI {
	u.exportDir = dir
	return u
}

func newTable() *tview.Table {
	t := tview.NewTable().SetFixed(1, 1).SetSelectable(true, false).SetSeparator(' ').SetEvaluateAllRows(true)
	t.SetBackgroundColor(background)
	t.SetBorder(true).SetBorderColor(tcell.NewHexColor(0x334155)).SetTitleColor(accent)
	t.SetSelectedStyle(tcell.StyleDefault.Background(tcell.NewHexColor(0x164e63)).Foreground(tcell.ColorWhite).Bold(true))
	return t
}

// Run keeps widget mutations on tview's event loop. A coalesced private key
// wakes that loop; unlike QueueUpdateDraw it cannot wait forever after quit.
func (u *UI) Run(ctx context.Context, updates <-chan monitor.Snapshot) error {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	if ctx.Err() != nil {
		return nil
	}
	u.app.SetBeforeDrawFunc(func(_ tcell.Screen) bool {
		changed := false
		for {
			select {
			case snapshot, ok := <-updates:
				if !ok {
					updates = nil
					if changed {
						u.render()
					}
					return false
				}
				u.snapshots[snapshot.Name] = snapshot
				changed = true
			default:
				if changed {
					u.render()
				} else {
					u.renderStatus()
				}
				return false
			}
		}
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				u.app.Stop()
				return
			case <-ticker.C:
				if u.wakePending.CompareAndSwap(false, true) {
					go u.app.QueueEvent(tcell.NewEventKey(tcell.KeyF24, 0, tcell.ModNone))
				}
			}
		}
	}()
	return u.app.Run()
}

// Update may be called before Run or from the UI event loop.
func (u *UI) Update(s monitor.Snapshot) { u.snapshots[s.Name] = s; u.render() }

func (u *UI) key(e *tcell.EventKey) *tcell.EventKey {
	if e.Key() == tcell.KeyF24 {
		u.wakePending.Store(false)
		return nil
	}
	if e.Key() == tcell.KeyCtrlC {
		u.app.Stop()
		return nil
	}
	if u.filtering {
		return e
	}
	if u.overlay {
		if e.Key() == tcell.KeyEscape {
			u.closeOverlay()
			return nil
		}
		if e.Rune() == 'q' {
			u.app.Stop()
			return nil
		}
		if e.Rune() == 'e' && u.exportData != nil {
			u.exportDetails()
			return nil
		}
		return e
	}
	if e.Key() == tcell.KeyEscape {
		switch {
		case u.filter != "":
			u.input.SetText("")
		case u.scopeStream != "":
			u.scopeConnection, u.scopeStream = "", ""
			u.view, u.sort = streamsView, 0
			u.render()
			u.selectID(u.backID)
		case u.view != streamsView:
			u.changeView(streamsView)
		}
		u.render()
		return nil
	}
	if e.Key() != tcell.KeyRune {
		return e
	}
	switch e.Rune() {
	case 'q':
		u.app.Stop()
	case '1':
		u.changeView(streamsView)
	case '2':
		u.changeView(consumersView)
	case '3':
		u.changeView(connectionsView)
	case '/':
		u.filtering = true
		u.layout.AddItem(u.input, 1, 0, true)
		u.app.SetFocus(u.input)
	case 'c':
		u.chooseConnection()
	case 'd':
		u.showDetails()
	case 's':
		u.sort = (u.sort + 1) % u.sortCount()
		u.render()
	case 'r':
		if u.refresh != nil {
			u.refresh()
		}
	case '?':
		u.showHelp()
	default:
		return e
	}
	return nil
}

func (u *UI) closeFilter() {
	u.filtering = false
	u.layout.RemoveItem(u.input)
	u.app.SetFocus(u.table)
	u.render()
}

func (u *UI) changeView(v view) {
	u.view, u.sort = v, 0
	u.scopeConnection, u.scopeStream = "", ""
	u.input.SetText("")
	u.table.ScrollToBeginning().Select(1, 0)
	u.render()
}

func (u *UI) closeOverlay() {
	u.pages.RemovePage("overlay")
	u.overlay = false
	u.overlayView = nil
	u.exportData = nil
	u.app.SetFocus(u.table)
}

func (u *UI) chooseConnection() {
	u.chooser = newTable()
	u.chooser.SetTitle(" Connections · Enter select · Esc back ")
	u.chooser.SetFixed(0, 0)
	names := []string{""}
	for name := range u.snapshots {
		names = append(names, name)
	}
	sort.Strings(names[1:])
	for i, name := range names {
		label := name
		if name == "" {
			label = "All connections"
		}
		u.chooser.SetCell(i, 0, tview.NewTableCell(" "+safe(label)).SetTextColor(foreground).SetExpansion(1))
		if name == u.connection {
			u.chooser.Select(i, 0)
		}
	}
	u.chooser.SetSelectedFunc(func(index, _ int) {
		if index < 0 || index >= len(names) {
			return
		}
		u.connection = names[index]
		u.scopeConnection, u.scopeStream = "", ""
		u.closeOverlay()
		u.render()
	})
	u.overlay = true
	u.pages.AddPage("overlay", u.chooser, true, true)
	u.app.SetFocus(u.chooser)
}

func safe(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
	return tview.Escape(s)
}

func age(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t).Round(time.Second)
	if d < 0 {
		d = 0
	}
	return d.String() + " ago"
}

func (u *UI) renderStatus() {
	if u.status == nil {
		return
	}
	r := u.selected()
	if r == nil {
		var problems []string
		for _, s := range u.orderedSnapshots() {
			if u.connection != "" && s.Name != u.connection {
				continue
			}
			if s.Error != "" {
				problems = append(problems, s.Name+": "+s.Error)
			}
		}
		if len(problems) > 0 {
			u.status.SetText(" [yellow]" + safe(strings.Join(problems, " | ")))
			return
		}
		u.status.SetText(" [gray]No matching rows. / filter · c connections · r refresh")
		return
	}
	s := u.snapshots[r.connection]
	message := fmt.Sprintf(" %s · %s · last full refresh %s", safe(s.Name), safe(s.URL), age(s.Updated))
	if s.Error != "" {
		message += "\n [yellow]" + safe(s.Error)
	}
	if r.stream != nil && r.stream.Error != "" {
		message += "\n [yellow]" + safe(r.stream.Error)
	}
	u.status.SetText(message)
}

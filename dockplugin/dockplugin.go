// Package dockplugin is lazytodo, a small todo list for the dock terminal host
// (github.com/dewald-L/dock). Capture is one line of text; priority (an
// Eisenhower answer), context, person and due date are optional questions.
//
//	dock plugin install github.com/dewald-L/lazytodo/dockplugin
package dockplugin

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dewald-L/dock/plugin"
)

func init() { plugin.Register(Plugin{}) }

type Plugin struct{}

func (Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID: "lazytodo", Name: "Todo", Version: "0.3.1",
		Description: "Quick todo capture; sort by priority, person and due date only when you want to",
		Display:     plugin.DisplayTile, Lifecycle: plugin.Persistent,
		MinWidth: 34, MinHeight: 9,
	}
}

func (Plugin) New(ctx plugin.Context) (tea.Model, error) {
	st, err := OpenStore(ctx.DataDir)
	if err != nil {
		return nil, err
	}
	return newModel(st, LoadConfig(ctx.ConfigDir), ctx.Theme), nil
}

// Summary implements plugin.Summarizer: it only reads todos.json.
func (Plugin) Summary(ctx plugin.Context) (plugin.Summary, error) {
	st, err := OpenStore(ctx.DataDir)
	if err != nil {
		return plugin.Summary{}, err
	}
	return summarize(st, time.Now()), nil
}

// summarize builds the home block: the four quadrants as a 2x2 matrix in
// the order of the 1-4 keys (do now, schedule / delegate, drop), each cell
// listing its open notes in the order the list shows them (due date, then
// age; both bands are the same height), with the unsorted inbox as a line above them. Lines is the compact text
// version for narrow or short terminals.
func summarize(st *Store, now time.Time) plugin.Summary {
	c := st.Counts()
	today := now.Format("2006-01-02")
	notes := map[int][]string{}
	late := map[int]bool{}
	overdue := 0
	for _, it := range st.Visible("", false) {
		text := it.Text
		if it.Due != "" && it.Due < today {
			overdue++
			late[it.Quadrant] = true
			text = "! " + text
		}
		notes[it.Quadrant] = append(notes[it.Quadrant], text)
	}
	cell := func(q int) plugin.Cell {
		l := plugin.LevelNone
		if late[q] {
			l = plugin.LevelWarn
		}
		return plugin.Cell{Title: quadrantNames[q], Count: c[q], Lines: notes[q], Level: l}
	}

	line := fmt.Sprintf("%d do now · %d to sort", c[QDo], c[QInbox])
	if overdue > 0 {
		line += fmt.Sprintf(" · %d overdue", overdue)
	}
	s := plugin.Summary{Lines: []string{
		line, fmt.Sprintf("%d schedule · %d delegate · %d drop", c[QSchedule], c[QDelegate], c[QDrop]),
	}}
	if n := notes[QDo]; len(n) > 0 {
		s.Lines = append(s.Lines, "next: "+n[0])
	}
	// the unsorted inbox comes first, then two equally tall bands of two
	// quadrants each (1+2 rows apiece, plus the inbox line: 7 of the budget)
	if c[QInbox] > 0 || overdue > 0 {
		tail := fmt.Sprintf("%d to sort", c[QInbox])
		l := plugin.LevelNone
		if overdue > 0 {
			tail += fmt.Sprintf(" · %d overdue", overdue)
			l = plugin.LevelWarn
		}
		s.Widgets = append(s.Widgets, plugin.Text(tail, l))
	}
	s.Widgets = append(s.Widgets,
		plugin.Columns(2, cell(QDo), cell(QSchedule)),
		plugin.Columns(2, cell(QDelegate), cell(QDrop)),
	)
	if overdue > 0 {
		s.Level = plugin.LevelWarn
	}
	return s
}

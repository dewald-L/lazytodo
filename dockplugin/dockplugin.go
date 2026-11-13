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
		ID: "lazytodo", Name: "Todo", Version: "0.1.0",
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

func summarize(st *Store, now time.Time) plugin.Summary {
	c := st.Counts()
	overdue := 0
	today := now.Format("2006-01-02")
	for _, it := range st.Items {
		if !it.Done && it.Due != "" && it.Due < today {
			overdue++
		}
	}
	line := fmt.Sprintf("%d do now · %d to sort", c[QDo], c[QInbox])
	if overdue > 0 {
		line += fmt.Sprintf(" · %d overdue", overdue)
	}
	s := plugin.Summary{Lines: []string{line}}
	if v := st.Visible("", false); len(v) > 0 && v[0].Quadrant == QDo {
		s.Lines = append(s.Lines, "next: "+v[0].Text)
	}
	if overdue > 0 {
		s.Level = plugin.LevelWarn
	}
	return s
}

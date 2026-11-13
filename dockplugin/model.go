package dockplugin

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dewald-L/dock/plugin"
)

type mode int

const (
	mList    mode = iota
	mText         // typing a todo (new or edited)
	mAsk          // optional questions about one item
	mFilter       // live filter
	mConfirm      // delete confirmation
	mHelp
)

type (
	addMsg      struct{}
	triageMsg   struct{}
	showDoneMsg struct{}
)

type model struct {
	st  *Store
	cfg Config
	now func() time.Time

	w, h int
	mode mode

	vis      []*Item
	sel, off int
	showDone bool
	filter   string

	ti      textinput.Model
	adding  bool // brain-dump session: the text prompt reopens after each item
	editID  int  // item being renamed (0 = new item)
	hint    string
	hintErr bool

	draft  Item
	isNew  bool
	steps  []string
	step   int
	triage []int // ids still to sort; nil when not triaging
	tpos   int

	accent, muted, ok, warn, errc lipgloss.Style
}

func newModel(st *Store, cfg Config, th plugin.Theme) *model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 300
	m := &model{st: st, cfg: cfg, ti: ti, now: time.Now,
		accent: lipgloss.NewStyle().Foreground(th.Accent).Bold(true),
		muted:  lipgloss.NewStyle().Foreground(th.Muted),
		ok:     lipgloss.NewStyle().Foreground(th.Ok),
		warn:   lipgloss.NewStyle().Foreground(th.Warn),
		errc:   lipgloss.NewStyle().Foreground(th.Err),
	}
	m.refresh(0)
	return m
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) cur() *Item {
	if m.sel >= 0 && m.sel < len(m.vis) {
		return m.vis[m.sel]
	}
	return nil
}

// refresh rebuilds the visible list and keeps the selection on item id (or
// at the same position when it is gone).
func (m *model) refresh(id int) {
	m.vis = m.st.Visible(m.filter, m.showDone)
	for i, it := range m.vis {
		if it.ID == id {
			m.sel = i
			return
		}
	}
	m.sel = max(min(m.sel, len(m.vis)-1), 0)
}

func (m *model) selID() int {
	if it := m.cur(); it != nil {
		return it.ID
	}
	return 0
}

// ---- update ----

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.ti.Width = max(m.w-12, 5)
		return m, nil
	case plugin.FocusMsg:
		if m.textActive() {
			return m, m.ti.Focus()
		}
		return m, nil
	case plugin.BlurMsg:
		m.ti.Blur()
		return m, nil
	case addMsg:
		return m, m.startAdd()
	case triageMsg:
		return m, m.startTriage()
	case showDoneMsg:
		m.toggleDone()
		return m, nil
	case tea.KeyMsg:
		return m.key(msg)
	}
	if m.textActive() {
		var cmd tea.Cmd
		m.ti, cmd = m.ti.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *model) textActive() bool {
	switch m.mode {
	case mText, mFilter:
		return true
	case mAsk:
		return m.isTextStep()
	}
	return false
}

func (m *model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case mText:
		return m.keyText(k)
	case mAsk:
		return m.keyAsk(k)
	case mFilter:
		return m.keyFilter(k)
	case mConfirm:
		if it := m.cur(); k.String() == "y" && it != nil {
			m.mode = mList
			err := m.st.Delete(it.ID)
			m.refresh(0)
			return m, plugin.Error(err)
		}
		m.mode = mList
		return m, nil
	case mHelp:
		m.mode = mList
		return m, nil
	}
	return m.keyList(k)
}

func (m *model) keyList(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	it := m.cur()
	switch s := k.String(); s {
	case "q":
		return m, plugin.Close()
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.refresh(m.selID())
			return m, nil
		}
		return m, plugin.Close()
	case "j", "down":
		m.sel = min(m.sel+1, len(m.vis)-1)
	case "k", "up":
		m.sel = max(m.sel-1, 0)
	case "g", "home":
		m.sel = 0
	case "G", "end":
		m.sel = max(len(m.vis)-1, 0)
	case "a":
		return m, m.startAdd()
	case "t":
		return m, m.startTriage()
	case "h":
		m.toggleDone()
	case "?":
		m.mode = mHelp
	case "/":
		m.mode = mFilter
		m.ti.SetValue(m.filter)
		m.ti.CursorEnd()
		return m, m.ti.Focus()
	case "e":
		if it != nil {
			m.mode, m.editID = mText, it.ID
			m.ti.SetValue(it.Text)
			m.ti.CursorEnd()
			return m, m.ti.Focus()
		}
	case "E":
		if it != nil {
			m.adding = false
			return m, m.startAsk(*it, false, m.cfg.Ask)
		}
	case "d":
		if it != nil {
			m.mode = mConfirm
		}
	case " ", "space", "x", "enter":
		if it != nil {
			done := !it.Done
			err := m.st.SetDone(it.ID, done)
			m.refresh(it.ID)
			if err != nil {
				return m, plugin.Error(err)
			}
			if done {
				return m, plugin.Notify("done: " + clip(it.Text, 40))
			}
		}
	case "0", "1", "2", "3", "4":
		if it != nil {
			c := *it
			c.Quadrant = int(s[0] - '0')
			err := m.st.Update(c)
			m.refresh(it.ID)
			return m, plugin.Error(err)
		}
	}
	return m, nil
}

func (m *model) toggleDone() {
	id := m.selID()
	m.showDone = !m.showDone
	m.refresh(id)
}

// ---- adding and editing text ----

func (m *model) startAdd() tea.Cmd {
	m.mode, m.adding, m.editID, m.hint = mText, true, 0, ""
	m.ti.SetValue("")
	return m.ti.Focus()
}

func (m *model) leaveText() {
	m.mode, m.adding, m.editID, m.hint = mList, false, 0, ""
	m.ti.Blur()
}

func (m *model) keyText(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.ti.Value())
	switch k.String() {
	case "esc":
		m.leaveText()
		return m, nil
	case "enter":
		if text == "" {
			m.leaveText()
			return m, nil
		}
		if m.editID != 0 {
			id := m.editID
			m.leaveText()
			if it := m.st.Get(id); it != nil {
				c := *it
				c.Text = text
				err := m.st.Update(c)
				m.refresh(id)
				return m, plugin.Error(err)
			}
			return m, nil
		}
		it, err := m.st.Add(Item{Text: text})
		if err != nil {
			return m, plugin.Error(err)
		}
		m.ti.SetValue("")
		m.hint = "added: " + clip(it.Text, 40)
		m.refresh(it.ID)
		return m, nil
	case "tab":
		if m.editID == 0 && text != "" {
			m.ti.SetValue("")
			return m, m.startAsk(Item{Text: text}, true, m.cfg.Ask)
		}
	}
	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(k)
	return m, cmd
}

func (m *model) keyFilter(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "enter":
		m.mode = mList
		m.ti.Blur()
		return m, nil
	case "esc":
		m.mode, m.filter = mList, ""
		m.ti.Blur()
		m.refresh(m.selID())
		return m, nil
	}
	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(k)
	m.filter = m.ti.Value()
	m.refresh(m.selID())
	return m, cmd
}

// ---- optional questions ----

var knownSteps = map[string]bool{"quadrant": true, "context": true, "who": true, "due": true}

func (m *model) startAsk(it Item, isNew bool, steps []string) tea.Cmd {
	m.draft, m.isNew, m.steps, m.step = it, isNew, nil, -1
	for _, s := range steps {
		if knownSteps[s] {
			m.steps = append(m.steps, s)
		}
	}
	m.mode = mAsk
	return m.advance()
}

func (m *model) isTextStep() bool {
	if m.step < 0 || m.step >= len(m.steps) {
		return false
	}
	s := m.steps[m.step]
	return s == "who" || s == "due"
}

func (m *model) advance() tea.Cmd {
	m.step++
	m.hint, m.hintErr = "", false
	if m.step >= len(m.steps) {
		return m.finishAsk()
	}
	switch m.steps[m.step] {
	case "who":
		m.ti.SetValue(m.draft.Who)
		m.ti.CursorEnd()
		return m.ti.Focus()
	case "due":
		m.ti.SetValue(m.draft.Due)
		m.ti.CursorEnd()
		return m.ti.Focus()
	}
	m.ti.Blur()
	return nil
}

// finishAsk stores the draft, then moves on: next item when triaging,
// back to the text prompt when brain-dumping, else back to the list.
func (m *model) finishAsk() tea.Cmd {
	var err error
	id := m.draft.ID
	if m.isNew {
		var p *Item
		if p, err = m.st.Add(m.draft); p != nil {
			id = p.ID
		}
	} else {
		err = m.st.Update(m.draft)
	}
	m.ti.Blur()
	m.refresh(id)
	if err != nil {
		m.mode, m.triage, m.adding = mList, nil, false
		return plugin.Error(err)
	}
	if m.triage != nil {
		return m.nextTriage()
	}
	if m.adding {
		m.mode, m.hint = mText, "added: "+clip(m.draft.Text, 40)
		m.ti.SetValue("")
		return m.ti.Focus()
	}
	m.mode = mList
	return nil
}

func (m *model) keyAsk(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "esc" {
		if m.triage != nil {
			m.triage, m.mode = nil, mList
			m.ti.Blur()
			return m, nil
		}
		return m, m.finishAsk() // enough questions: keep what was answered
	}
	if m.step < 0 || m.step >= len(m.steps) {
		return m, m.finishAsk()
	}
	skip := s == "enter" || s == " " || s == "space" || s == "tab" || s == "s"
	switch m.steps[m.step] {
	case "quadrant":
		if len(s) == 1 && s[0] >= '0' && s[0] <= '4' {
			m.draft.Quadrant = int(s[0] - '0')
			return m, m.advance()
		}
		if skip {
			return m, m.advance()
		}
	case "context":
		if len(s) == 1 && s[0] >= '1' && s[0] <= '9' && int(s[0]-'1') < len(m.cfg.Contexts) {
			m.draft.Context = m.cfg.Contexts[s[0]-'1']
			return m, m.advance()
		}
		if s == "0" {
			m.draft.Context = ""
			return m, m.advance()
		}
		if skip {
			return m, m.advance()
		}
	case "who":
		if s == "enter" {
			m.draft.Who = strings.TrimPrefix(strings.TrimSpace(m.ti.Value()), "@")
			return m, m.advance()
		}
		return m.typeInto(k)
	case "due":
		if s == "enter" {
			d, err := ParseDue(m.ti.Value(), m.now())
			if err != nil {
				m.hint, m.hintErr = err.Error(), true
				return m, nil
			}
			m.draft.Due = d
			return m, m.advance()
		}
		return m.typeInto(k)
	}
	return m, nil
}

func (m *model) typeInto(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(k)
	return m, cmd
}

// ---- triage ----

func (m *model) startTriage() tea.Cmd {
	m.triage = nil
	for _, it := range m.st.Items {
		if !it.Done && it.Quadrant == QInbox {
			m.triage = append(m.triage, it.ID)
		}
	}
	if m.triage == nil {
		return plugin.Notify("inbox is empty")
	}
	m.tpos = -1
	m.adding = false
	return m.nextTriage()
}

func (m *model) nextTriage() tea.Cmd {
	for m.tpos++; m.tpos < len(m.triage); m.tpos++ {
		if it := m.st.Get(m.triage[m.tpos]); it != nil && !it.Done {
			return m.startAsk(*it, false, []string{"quadrant"})
		}
	}
	n := len(m.triage)
	m.triage, m.mode = nil, mList
	return plugin.Notify(fmt.Sprintf("inbox sorted (%d)", n))
}

// ---- view ----

func clip(s string, n int) string { return ansi.Truncate(s, max(n, 1), "…") }

func (m *model) View() string {
	if m.w <= 0 || m.h <= 0 {
		return ""
	}
	var foot []string
	switch m.mode {
	case mText:
		label := "+ "
		hint := m.hint
		if m.editID != 0 {
			label = "edit: "
		}
		if hint == "" {
			hint = "enter save · tab add details · esc done"
			if m.editID != 0 {
				hint = "enter save · esc cancel"
			}
		}
		foot = []string{m.muted.Render(hint), m.accent.Render(label) + m.ti.View()}
	case mAsk:
		foot = m.askFooter()
	case mFilter:
		foot = []string{m.accent.Render("/ ") + m.ti.View()}
	case mConfirm:
		if it := m.cur(); it != nil {
			foot = []string{m.warn.Render("Delete “" + clip(it.Text, m.w-20) + "”? y/n")}
		}
	}
	bh := max(m.h-1-len(foot), 1)
	var lines []string
	if m.mode == mHelp {
		lines = m.helpLines()
	} else {
		lines = m.bodyLines(bh)
	}
	for len(lines) < bh {
		lines = append(lines, "")
	}
	out := append([]string{m.header()}, lines[:bh]...)
	out = append(out, foot...)
	for len(out) < m.h {
		out = append(out, "")
	}
	st := lipgloss.NewStyle().MaxWidth(m.w)
	out = out[:m.h]
	for i := range out {
		out[i] = st.Render(out[i])
	}
	return strings.Join(out, "\n")
}

func (m *model) header() string {
	c := m.st.Counts()
	open := c[0] + c[1] + c[2] + c[3] + c[4]
	s := m.accent.Render("Todo") + m.muted.Render(fmt.Sprintf(" · %d open", open))
	if c[QInbox] > 0 {
		s += m.muted.Render(fmt.Sprintf(" · %d to sort (t)", c[QInbox]))
	}
	if m.filter != "" {
		s += m.warn.Render(" · /" + m.filter)
	}
	if m.showDone {
		s += m.muted.Render(" · +done")
	}
	return s
}

func (m *model) askFooter() []string {
	title := clip(m.draft.Text, m.w-14)
	if m.triage != nil {
		title = m.muted.Render(fmt.Sprintf("sort %d/%d  ", m.tpos+1, len(m.triage))) + title
	}
	hint := m.muted.Render("enter skip · esc done")
	if m.hintErr {
		hint = m.errc.Render(m.hint)
	}
	var q string
	switch m.steps[max(m.step, 0)] {
	case "quadrant":
		var p []string
		for i := QDo; i <= QDrop; i++ {
			p = append(p, fmt.Sprintf("%d %s", i, quadrantNames[i]))
		}
		q = m.accent.Render("Priority? ") + strings.Join(p, " · ")
		if m.triage != nil && !m.hintErr {
			hint = m.muted.Render("1 urgent+important · 2 important · 3 urgent · 4 neither · s skip")
		}
	case "context":
		var p []string
		for i, c := range m.cfg.Contexts {
			p = append(p, fmt.Sprintf("%d %s", i+1, c))
		}
		q = m.accent.Render("Context? ") + strings.Join(p, " · ")
	case "who":
		q = m.accent.Render("Who? ") + m.ti.View()
	case "due":
		q = m.accent.Render("Due? ") + m.ti.View()
		if !m.hintErr {
			hint = m.muted.Render("today · tom · fri · +3 · 12-24 · enter skip")
		}
	}
	return []string{title, hint, q}
}

func (m *model) bodyLines(bh int) []string {
	if len(m.vis) == 0 {
		if m.filter != "" {
			return []string{"", m.muted.Render("  nothing matches")}
		}
		return []string{"", m.muted.Render("  Nothing to do. Press a and dump what's on your mind."),
			m.muted.Render("  Just type; sort it later with t.")}
	}
	var lines []string
	var isHdr []bool
	add := func(s string, hdr bool) { lines, isHdr = append(lines, s), append(isHdr, hdr) }
	counts := map[int]int{}
	for _, it := range m.vis {
		counts[it.Quadrant]++
	}
	selLine, last := 0, -1
	for i, it := range m.vis {
		if it.Quadrant != last {
			last = it.Quadrant
			h := m.accent.Render(quadrantNames[last]) + m.muted.Render(fmt.Sprintf(" %d", counts[last]))
			if quadrantHints[last] != "" {
				h += m.muted.Render(" · " + quadrantHints[last])
			}
			if len(lines) > 0 {
				add("", false)
			}
			add(h, true)
		}
		if i == m.sel {
			selLine = len(lines)
		}
		add(m.row(it, i == m.sel), false)
	}
	top := selLine
	if selLine > 0 && isHdr[selLine-1] {
		top--
	}
	if top < m.off {
		m.off = top
	}
	if selLine >= m.off+bh {
		m.off = selLine - bh + 1
	}
	m.off = max(min(m.off, len(lines)-1), 0)
	return lines[m.off:]
}

func (m *model) row(it *Item, selected bool) string {
	cursor, box := "  ", "[ ]"
	if selected {
		cursor = m.accent.Render("▸ ")
	}
	if it.Done {
		box = "[x]"
	}
	var meta, plain []string
	if it.Context != "" {
		meta, plain = append(meta, m.muted.Render(it.Context)), append(plain, it.Context)
	}
	if it.Who != "" {
		meta, plain = append(meta, m.muted.Render("@"+it.Who)), append(plain, "@"+it.Who)
	}
	if it.Due != "" {
		lbl, urg := dueLabel(it.Due, m.now())
		st := m.muted
		switch {
		case it.Done:
		case urg == 2:
			st = m.errc
		case urg == 1:
			st = m.warn
		}
		meta, plain = append(meta, st.Render(lbl)), append(plain, lbl)
	}
	metaS, metaW := "", 0
	if len(meta) > 0 {
		metaS = "  " + strings.Join(meta, m.muted.Render(" · "))
		metaW = 2 + ansi.StringWidth(strings.Join(plain, " · "))
	}
	tw := m.w - 6 - metaW
	if tw < 12 {
		metaS, tw = "", m.w-6
	}
	text := clip(it.Text, tw)
	switch {
	case it.Done, it.Quadrant == QDrop:
		text = m.muted.Render(text)
	case selected:
		text = lipgloss.NewStyle().Bold(true).Render(text)
	}
	return cursor + m.muted.Render(box) + " " + text + metaS
}

func (m *model) helpLines() []string {
	rows := [][2]string{
		{"a", "add (enter saves and keeps going; tab asks details)"},
		{"t", "triage: sort the inbox one by one"},
		{"0-4", "priority: 1 do now, 2 schedule, 3 delegate, 4 drop, 0 inbox"},
		{"space", "done / not done"},
		{"e / E", "edit text / edit all details"},
		{"d", "delete"},
		{"/", "filter (text, context or person)"},
		{"h", "show or hide done items"},
		{"j k g G", "move"},
		{"q esc", "close"},
	}
	out := []string{m.accent.Render("Keys"), ""}
	for _, r := range rows {
		out = append(out, "  "+m.accent.Render(fmt.Sprintf("%-8s", r[0]))+" "+r[1])
	}
	return append(out, "", m.muted.Render("  Everything is optional: add text now, sort later."))
}

// ---- host integration ----

func (m *model) Close() error { return nil }

func (m *model) Help() []plugin.KeyHelp {
	switch m.mode {
	case mText:
		return []plugin.KeyHelp{{Key: "enter", Desc: "save"}, {Key: "tab", Desc: "details"}, {Key: "esc", Desc: "done"}}
	case mAsk:
		return []plugin.KeyHelp{{Key: "enter", Desc: "skip"}, {Key: "esc", Desc: "done"}}
	case mFilter:
		return []plugin.KeyHelp{{Key: "enter", Desc: "keep"}, {Key: "esc", Desc: "clear"}}
	case mConfirm:
		return []plugin.KeyHelp{{Key: "y", Desc: "delete"}, {Key: "n", Desc: "cancel"}}
	}
	return []plugin.KeyHelp{{Key: "a", Desc: "add"}, {Key: "t", Desc: "sort"}, {Key: "space", Desc: "done"},
		{Key: "/", Desc: "filter"}, {Key: "?", Desc: "help"}}
}

func (m *model) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "add todo", Desc: "capture something now", Msg: addMsg{}},
		{Name: "sort inbox", Desc: "triage unsorted todos", Msg: triageMsg{}},
		{Name: "show done", Desc: "show or hide done todos", Msg: showDoneMsg{}},
	}
}

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
	fq       int    // focused quadrant (grid); follows sel unless empty
	empty    bool   // the focused quadrant has no notes, so nothing is selected
	poff     [5]int // per-quadrant scroll offsets of the grid view
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
	if m.empty && m.gridOn() {
		return nil
	}
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
			m.sel, m.empty, m.fq = i, false, it.Quadrant
			return
		}
	}
	m.sel = max(min(m.sel, len(m.vis)-1), 0)
	if m.empty {
		if lo, hi := m.span(m.fq); hi > lo {
			m.sel, m.empty = lo, false
		}
		return
	}
	if it := m.cur(); it != nil {
		m.fq = it.Quadrant
	}
}

// keepPane leaves focus on quadrant q even if its last visible note just
// went away (deleted, done), instead of jumping to another pane.
func (m *model) keepPane(q int) {
	if m.count(q) == 0 {
		m.fq, m.empty = q, true
	}
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
			m.keepPane(it.Quadrant)
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

// ---- list view keys ----
//
// listKeys is the single place the list/grid view's keys are defined. The
// key handler, the help overlay, the footer hints and the panel titles all
// read from it (or from paneKeys / keyPrev / keyNext), so rebinding a key
// here changes behaviour and hint together.

// paneKeys is the key that jumps to each quadrant, indexed by quadrant.
var paneKeys = [...]string{QInbox: "0", QDo: "1", QSchedule: "2", QDelegate: "3", QDrop: "4"}

// keyPrev and keyNext move a todo to the previous / next quadrant, and
// change the answer in the priority prompt.
const keyPrev, keyNext = "[", "]"

type binding struct {
	keys  []string // as tea.KeyMsg.String() reports them; keys[0] is the one shown
	desc  string   // help overlay text; consecutive bindings with the same desc share a row
	short string   // footer hint; empty leaves it out of the footer
	run   func(m *model, it *Item) tea.Cmd
}

// label is how the help overlay and hints name a binding's key.
func (b binding) label() string {
	if b.keys[0] == " " {
		return "space"
	}
	return b.keys[0]
}

var listKeys = buildListKeys()

func buildListKeys() []binding {
	plain := func(f func(m *model)) func(*model, *Item) tea.Cmd {
		return func(m *model, _ *Item) tea.Cmd { f(m); return nil }
	}
	bs := []binding{
		{keys: []string{"a"}, desc: "add (enter saves and keeps going; tab asks details)", short: "add",
			run: func(m *model, _ *Item) tea.Cmd { return m.startAdd() }},
		{keys: []string{"t"}, desc: "triage: sort the inbox one by one", short: "sort",
			run: func(m *model, _ *Item) tea.Cmd { return m.startTriage() }},
		{keys: []string{" ", "space", "x", "enter"}, desc: "done / not done", short: "done", run: (*model).toggleItemDone},
		{keys: []string{"e"}, desc: "edit text", run: func(m *model, it *Item) tea.Cmd {
			if it == nil {
				return nil
			}
			m.mode, m.editID = mText, it.ID
			m.ti.SetValue(it.Text)
			m.ti.CursorEnd()
			return m.ti.Focus()
		}},
		{keys: []string{"E"}, desc: "edit all details", run: func(m *model, it *Item) tea.Cmd {
			if it == nil {
				return nil
			}
			m.adding = false
			return m.startAsk(*it, false, m.cfg.Ask)
		}},
		{keys: []string{"d"}, desc: "delete", run: func(m *model, it *Item) tea.Cmd {
			if it != nil {
				m.mode = mConfirm
			}
			return nil
		}},
		{keys: []string{"/"}, desc: "filter (text, context or person)", short: "filter", run: func(m *model, _ *Item) tea.Cmd {
			m.mode = mFilter
			m.ti.SetValue(m.filter)
			m.ti.CursorEnd()
			return m.ti.Focus()
		}},
		{keys: []string{"H"}, desc: "show or hide done items", run: plain((*model).toggleDone)},
		{keys: []string{keyPrev}, desc: "move todo to the previous / next quadrant", run: func(m *model, it *Item) tea.Cmd { return m.moveItem(it, -1) }},
		{keys: []string{keyNext}, desc: "move todo to the previous / next quadrant", run: func(m *model, it *Item) tea.Cmd { return m.moveItem(it, 1) }},
	}
	for _, q := range gridOrderWithInbox() {
		bs = append(bs, binding{keys: []string{paneKeys[q]}, desc: "jump to a quadrant (its key is in its title)",
			run: func(m *model, it *Item) tea.Cmd { m.focus(q, it); return nil }})
	}
	const moveDesc = "move (within a quadrant in the grid)"
	const sideDesc = "previous / next quadrant (beside it, in the grid)"
	return append(bs,
		binding{keys: []string{"j", "down"}, desc: moveDesc, run: plain(func(m *model) { m.move(1) })},
		binding{keys: []string{"k", "up"}, desc: moveDesc, run: plain(func(m *model) { m.move(-1) })},
		binding{keys: []string{"g", "home"}, desc: moveDesc, run: plain(func(m *model) { m.edge(false) })},
		binding{keys: []string{"G", "end"}, desc: moveDesc, run: plain(func(m *model) { m.edge(true) })},
		binding{keys: []string{"h", "left"}, desc: sideDesc, run: plain(func(m *model) { m.pane(-1, false) })},
		binding{keys: []string{"l", "right"}, desc: sideDesc, run: plain(func(m *model) { m.pane(1, false) })},
		binding{keys: []string{"tab"}, desc: "next quadrant", short: "quadrant", run: plain(func(m *model) { m.pane(1, true) })},
		binding{keys: []string{"shift+tab"}, desc: "previous quadrant", run: plain(func(m *model) { m.pane(-1, true) })},
		binding{keys: []string{"?"}, desc: "this help", short: "help", run: plain(func(m *model) { m.mode = mHelp })},
		binding{keys: []string{"q", "esc"}, desc: "close (esc first clears the filter)", run: func(m *model, _ *Item) tea.Cmd {
			if m.filter != "" {
				m.filter = ""
				m.refresh(m.selID())
				return nil
			}
			return plugin.Close()
		}},
	)
}

func gridOrderWithInbox() []int { return []int{QInbox, QDo, QSchedule, QDelegate, QDrop} }

func (m *model) keyList(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	for _, b := range listKeys {
		for _, key := range b.keys {
			if key == s {
				return m, b.run(m, m.cur())
			}
		}
	}
	return m, nil
}

func (m *model) toggleItemDone(it *Item) tea.Cmd {
	if it == nil {
		return nil
	}
	done := !it.Done
	q := it.Quadrant
	err := m.st.SetDone(it.ID, done)
	m.refresh(it.ID)
	m.keepPane(q)
	if err != nil {
		return plugin.Error(err)
	}
	if done {
		return plugin.Notify("done: " + clip(it.Text, 40))
	}
	return nil
}

// moveItem moves it d quadrants along (see stepQuadrant).
func (m *model) moveItem(it *Item, d int) tea.Cmd {
	if it == nil {
		return nil
	}
	c := *it
	c.Quadrant = stepQuadrant(c.Quadrant, d)
	if c.Quadrant == it.Quadrant {
		return nil
	}
	err := m.st.Update(c)
	m.refresh(it.ID)
	return plugin.Error(err)
}

// stepQuadrant is the quadrant d steps along inbox, do now, schedule,
// delegate, drop (clamped at both ends).
func stepQuadrant(q, d int) int { return min(max(q+d, QInbox), QDrop) }

// ---- grid navigation ----
//
// m.vis stays one list grouped by quadrant (groupOrder) and m.sel indexes it;
// the grid is only another way to draw it. The focused pane is the selected
// note's quadrant, so every editing key works the same in both layouts.

// gridOrder is the reading order of the panes: the 2x2 matrix, then the inbox.
var gridOrder = []int{QDo, QSchedule, QDelegate, QDrop, QInbox}

// gridPartner is the pane beside q in the matrix (the inbox has none).
var gridPartner = map[int]int{QDo: QSchedule, QSchedule: QDo, QDelegate: QDrop, QDrop: QDelegate}

const (
	minPaneW = 24 // each of the two matrix columns
	minPaneH = 3  // border, border and one note row
)

// gridFits says whether the grid can be drawn in a body bh rows tall.
func (m *model) gridFits(bh int) bool {
	return len(m.vis) > 0 && m.w >= 2*minPaneW && bh >= 3*minPaneH
}

func (m *model) gridOn() bool { return m.gridFits(m.h - 1) }

// span is the range m.vis[lo:hi] holding quadrant q's notes (they are contiguous).
func (m *model) span(q int) (lo, hi int) {
	lo = -1
	for i, it := range m.vis {
		if it.Quadrant == q {
			if lo < 0 {
				lo = i
			}
			hi = i + 1
		}
	}
	return max(lo, 0), hi
}

func (m *model) move(d int) {
	if m.empty && m.gridOn() {
		return
	}
	lo, hi := 0, len(m.vis)
	if m.gridOn() && m.cur() != nil {
		lo, hi = m.span(m.cur().Quadrant)
	}
	if hi > lo {
		m.sel = max(min(m.sel+d, hi-1), lo)
	}
}

// edge goes to the first or last note of the pane (or of the whole list).
func (m *model) edge(end bool) {
	if m.empty && m.gridOn() {
		return
	}
	lo, hi := 0, len(m.vis)
	if m.gridOn() && m.cur() != nil {
		lo, hi = m.span(m.cur().Quadrant)
	}
	m.sel = lo
	if end && hi > lo {
		m.sel = hi - 1
	}
}

// pane moves to another quadrant, keeping the row where it can. In the grid
// h/l go to the pane beside this one and tab/shift+tab cycle all of them; in
// the list (cycle or not) they step to the previous or next group. Panes
// with no notes are skipped.
func (m *model) pane(d int, cycle bool) {
	it := m.cur()
	if m.gridOn() {
		if !cycle {
			p, ok := gridPartner[m.fq]
			// h only leaves the right-hand pane, l the left-hand one
			if ok && (p < m.fq) == (d < 0) {
				m.focus(p, it)
			}
			return
		}
		i := 0
		for j, q := range gridOrder {
			if q == m.fq {
				i = j
			}
		}
		m.focus(gridOrder[((i+d)%len(gridOrder)+len(gridOrder))%len(gridOrder)], it)
		return
	}
	if it == nil {
		return
	}
	i := 0
	for j, q := range groupOrder {
		if q == it.Quadrant {
			i = j
		}
	}
	for n := 1; n < len(groupOrder); n++ {
		if m.focus(groupOrder[((i+d*n)%len(groupOrder)+len(groupOrder))%len(groupOrder)], it) {
			return
		}
	}
}

// focus selects the note in quadrant q on the same row as from (clamped);
// false when q has no notes.
func (m *model) focus(q int, from *Item) bool {
	row := 0
	if from != nil {
		flo, _ := m.span(from.Quadrant)
		row = m.sel - flo
	}
	lo, hi := m.span(q)
	if hi <= lo {
		if m.gridOn() { // the grid shows empty panes too: focus is theirs to take
			m.fq, m.empty = q, true
			return true
		}
		return false
	}
	m.fq, m.empty = q, false
	m.sel = lo + min(max(row, 0), hi-lo-1)
	return true
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
		for q, key := range paneKeys {
			if s == key {
				m.draft.Quadrant = q
				return m, m.advance()
			}
		}
		if s == keyPrev || s == keyNext {
			m.draft.Quadrant = stepQuadrant(m.draft.Quadrant, map[string]int{keyPrev: -1, keyNext: 1}[s])
			return m, nil
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
	m.ti.Width = max(m.w-12, 5)
	askModal := m.mode == mAsk && m.w >= 20 && m.h >= 9
	modal := m.mode == mText && m.editID == 0 && m.w >= 20 && m.h >= 8
	switch m.mode {
	case mText:
		if modal {
			break
		}
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
		if askModal {
			break
		}
		title, q, hint := m.askRows()
		foot = append([]string{title, hint}, q...)
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
	} else if m.gridFits(bh) {
		lines = m.gridLines(bh)
	} else {
		lines = m.bodyLines(bh)
	}
	for len(lines) < bh {
		lines = append(lines, "")
	}
	lines = lines[:bh]
	if modal {
		lines = m.overlay(lines, m.addModal())
	} else if askModal {
		lines = m.overlay(lines, m.askModal())
	}
	out := append([]string{m.header()}, lines...)
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

// askRows is the prompt for the current question: the todo, the question and a hint.
func (m *model) askRows() (title string, q []string, hint string) {
	title = clip(m.draft.Text, m.w-14)
	if m.triage != nil {
		title = m.muted.Render(fmt.Sprintf("sort %d/%d  ", m.tpos+1, len(m.triage))) + title
	}
	hint = m.muted.Render("enter skip · esc done")
	if m.hintErr {
		hint = m.errc.Render(m.hint)
	}
	switch m.steps[max(m.step, 0)] {
	case "quadrant":
		// the options laid out like the matrix: do now, schedule / delegate, drop
		opt := func(i int) string {
			c := paneKeys[i] + " " + quadrantNames[i]
			if i == m.draft.Quadrant {
				return m.accent.Render(c) + strings.Repeat(" ", max(14-len(c), 0))
			}
			return c + strings.Repeat(" ", max(14-len(c), 0))
		}
		q = []string{m.accent.Render("Priority?"), opt(QDo) + opt(QSchedule), opt(QDelegate) + opt(QDrop)}
		if !m.hintErr {
			hint = m.muted.Render(paneKeys[QInbox] + " inbox · " + keyPrev + " " + keyNext + " change · enter keep · s skip")
		}
	case "context":
		var p []string
		for i, c := range m.cfg.Contexts {
			p = append(p, fmt.Sprintf("%d %s", i+1, c))
		}
		q = []string{m.accent.Render("Context? ") + strings.Join(p, " · ")}
	case "who":
		q = []string{m.accent.Render("Who? ") + m.ti.View()}
	case "due":
		q = []string{m.accent.Render("Due? ") + m.ti.View()}
		if !m.hintErr {
			hint = m.muted.Render("today · tom · fri · +3 · 12-24 · enter skip")
		}
	}
	return title, q, hint
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
		add(m.row(it, i == m.sel, m.w), false)
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

func (m *model) row(it *Item, selected bool, w int) string {
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
	tw := w - 6 - metaW
	if tw < 12 {
		metaS, tw = "", w-6
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
	type row struct{ keys, desc string }
	var rows []row
	for _, b := range listKeys {
		if n := len(rows); n > 0 && rows[n-1].desc == b.desc {
			rows[n-1].keys += " " + b.label()
			continue
		}
		rows = append(rows, row{b.label(), b.desc})
	}
	out := []string{m.accent.Render("Keys"), ""}
	for _, r := range rows {
		out = append(out, "  "+m.accent.Render(fmt.Sprintf("%-10s", r.keys))+" "+r.desc)
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
	var hs []plugin.KeyHelp
	for _, b := range listKeys {
		if b.short != "" {
			hs = append(hs, plugin.KeyHelp{Key: b.label(), Desc: b.short})
		}
	}
	return hs
}

func (m *model) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "add todo", Desc: "capture something now", Msg: addMsg{}},
		{Name: "sort inbox", Desc: "triage unsorted todos", Msg: triageMsg{}},
		{Name: "show done", Desc: "show or hide done todos", Msg: showDoneMsg{}},
	}
}

// ---- grid view ----

// gridLines draws the four quadrants as a 2x2 matrix of bordered panes
// (do now, schedule / delegate, drop) of equal size with the inbox as a strip
// on top, in a body bh rows tall. The caller has checked gridFits.
func (m *model) gridLines(bh int) []string {
	focus := m.fq
	strip := 2 + min(max(m.count(QInbox), 1), 3)
	// the four quadrants share what is left equally (an odd row goes to the top band)
	rest := bh - strip
	bottom := rest / 2
	top := rest - bottom
	lw, rw := (m.w+1)/2, m.w/2
	out := m.pane1(QInbox, m.w, strip, focus == QInbox)
	for _, band := range []struct{ l, r, h int }{{QDo, QSchedule, top}, {QDelegate, QDrop, bottom}} {
		left := m.pane1(band.l, lw, band.h, focus == band.l)
		right := m.pane1(band.r, rw, band.h, focus == band.r)
		for i := range left {
			out = append(out, left[i]+right[i])
		}
	}
	return out
}

// count is how many visible notes are in quadrant q.
func (m *model) count(q int) int {
	lo, hi := m.span(q)
	return hi - lo
}

// pane1 draws quadrant q's notes in a bordered box w wide and h tall,
// scrolled so the selected note shows when the pane is focused.
func (m *model) pane1(q, w, h int, focused bool) []string {
	bs := m.muted
	if focused {
		bs = m.accent
	}
	lo, hi := m.span(q)
	n := hi - lo
	if n == 0 {
		lo, hi = 0, 0
	}
	inner := h - 2
	off := min(m.poff[q], max(n-inner, 0))
	if focused && n > 0 {
		sel := m.sel - lo
		off = min(max(off, sel-inner+1), sel)
	}
	off = max(off, 0)
	m.poff[q] = off

	title := m.muted.Render(paneKeys[q]+" ") + m.accent.Render(quadrantNames[q])
	if hint := quadrantHints[q]; hint != "" && w >= 44 {
		title += m.muted.Render(" · " + hint)
	}
	tw := ansi.StringWidth(ansi.Strip(title))
	title = ansi.Truncate(title, max(w-5, 1), "…")
	tw = min(tw, max(w-5, 1))
	dashes := func(k int) string { return strings.Repeat("─", max(k, 0)) }
	out := []string{bs.Render("╭─ ") + title + bs.Render(" "+dashes(w-tw-5)+"╮")}

	var rows []string
	for i := off; i < n && len(rows) < inner; i++ {
		it := m.vis[lo+i]
		rows = append(rows, m.row(it, focused && lo+i == m.sel, w-4))
	}
	if n == 0 {
		rows = []string{m.muted.Render("  —")}
	}
	for len(rows) < inner {
		rows = append(rows, "")
	}
	for _, r := range rows {
		r = ansi.Truncate(r, w-4, "…")
		out = append(out, bs.Render("│")+" "+r+strings.Repeat(" ", max(w-4-ansi.StringWidth(r), 0))+" "+bs.Render("│"))
	}
	foot, fw := "", 0
	if below := n - off - inner; below > 0 {
		foot = fmt.Sprintf(" ↓%d ", below)
		fw = ansi.StringWidth(foot)
	}
	return append(out, bs.Render("╰"+dashes(w-2-fw))+m.muted.Render(foot)+bs.Render("╯"))
}

// ---- add modal ----

// modalWidth is the width of the modal boxes.
func (m *model) modalWidth() int { return min(m.w-4, 60) }

// box draws a bordered modal w wide with a title in its top edge.
func (m *model) box(w int, title string, rows ...string) []string {
	dashes := func(n int) string { return strings.Repeat("─", max(n, 0)) }
	line := func(s string) string {
		s = ansi.Truncate(s, w-4, "…")
		return m.accent.Render("│") + " " + s + strings.Repeat(" ", max(w-4-ansi.StringWidth(s), 0)) + " " + m.accent.Render("│")
	}
	out := []string{m.accent.Render("╭─ " + title + " " + dashes(w-5-ansi.StringWidth(title)) + "╮")}
	for _, r := range rows {
		out = append(out, line(r))
	}
	return append(out, m.accent.Render("╰"+dashes(w-2)+"╯"))
}

// addModal is the box the new-todo prompt is typed into.
func (m *model) addModal() []string {
	w := m.modalWidth()
	m.ti.Width = max(w-5, 1)
	hint := m.hint
	if hint == "" {
		hint = "enter save · tab add details · esc done"
	}
	return m.box(w, "New todo", m.ti.View(), m.muted.Render(hint))
}

// askModal is the box for sorting a todo (triage) and asking its details.
func (m *model) askModal() []string {
	w := m.modalWidth()
	m.ti.Width = max(w-14, 1)
	title := "Details"
	if m.triage != nil {
		title = fmt.Sprintf("Sort %d/%d", m.tpos+1, len(m.triage))
	}
	_, q, hint := m.askRows()
	return m.box(w, title, append(append([]string{m.accent.Render(clip(m.draft.Text, w-4)), ""}, q...), hint)...)
}

// overlay draws box centred over the body lines (padded with blanks as needed).
func (m *model) overlay(lines, box []string) []string {
	bw := ansi.StringWidth(box[0])
	x, y := (m.w-bw)/2, max((len(lines)-len(box))/2, 0)
	out := append([]string(nil), lines...)
	for len(out) < y+len(box) {
		out = append(out, "")
	}
	for i, b := range box {
		bg := out[y+i]
		pad := strings.Repeat(" ", max(x-ansi.StringWidth(bg), 0))
		out[y+i] = ansi.Truncate(bg+pad, x, "") + b + ansi.TruncateLeft(bg, x+bw, "")
	}
	return out
}

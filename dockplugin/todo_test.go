package dockplugin

import (
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dewald-L/dock/plugin"
)

var day = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC) // a Friday

func TestParseDue(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "today": "2026-11-13", "tom": "2026-11-14", "fri": "2026-11-20",
		"mon": "2026-11-16", "+3": "2026-11-16", "+1w": "2026-11-20",
		"12-24": "2026-12-24", "01-05": "2027-01-05", "2027-02-03": "2027-02-03",
	} {
		got, err := ParseDue(in, day)
		if err != nil || got != want {
			t.Errorf("ParseDue(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseDue("soonish", day); err == nil {
		t.Error("expected error")
	}
}

func press(m *model, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m.Update(msg)
	}
}

func typ(m *model, s string) { press(m, strings.Split(s, "")...) }

func newTest(t *testing.T) (*model, string) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(st, DefaultConfig(), plugin.DefaultTheme())
	m.now = func() time.Time { return day }
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 16})
	return m, dir
}

func TestBrainDumpAndTriage(t *testing.T) {
	m, dir := newTest(t)
	press(m, "a")
	typ(m, "fix login bug")
	press(m, "enter")
	typ(m, "1:1 prep for Ana")
	press(m, "enter", "esc")
	if len(m.st.Items) != 2 || m.mode != mList {
		t.Fatalf("items=%d mode=%v", len(m.st.Items), m.mode)
	}
	press(m, "t", "1", "3") // first -> Do, second -> Delegate
	if m.mode != mList || m.st.Items[0].Quadrant != QDo || m.st.Items[1].Quadrant != QDelegate {
		t.Fatalf("triage failed: %+v %+v mode=%v", m.st.Items[0], m.st.Items[1], m.mode)
	}
	// persisted
	st2, _ := OpenStore(dir)
	if len(st2.Items) != 2 || st2.Items[0].Quadrant != QDo {
		t.Fatal("not persisted")
	}
}

func TestGuidedAdd(t *testing.T) {
	m, _ := newTest(t)
	press(m, "a")
	typ(m, "review PR")
	press(m, "tab", "2", "1") // schedule, code
	typ(m, "@ana")
	press(m, "enter")
	typ(m, "tom")
	press(m, "enter", "esc")
	it := m.st.Items[0]
	if it.Quadrant != QSchedule || it.Context != "code" || it.Who != "ana" || it.Due != "2026-11-14" {
		t.Fatalf("%+v", it)
	}
	if v := m.View(); strings.Count(v, "\n") != 15 {
		t.Fatalf("view height %d", strings.Count(v, "\n")+1)
	}
}

func TestDoneDeleteSummary(t *testing.T) {
	m, _ := newTest(t)
	m.st.Add(Item{Text: "a", Quadrant: QDo, Due: "2026-11-01"})
	m.st.Add(Item{Text: "b"})
	m.refresh(0)
	s := summarize(m.st, day)
	if s.Level != plugin.LevelWarn || !strings.Contains(s.Lines[0], "1 overdue") {
		t.Fatalf("%+v", s)
	}
	press(m, " ") // done on first (Do) item
	if len(m.vis) != 1 {
		t.Fatalf("done item still visible: %d", len(m.vis))
	}
	press(m, "d", "y")
	if len(m.st.Items) != 1 {
		t.Fatal("not deleted")
	}
}

func TestSummaryMatrix(t *testing.T) {
	m, _ := newTest(t)
	add := func(text string, q int, due string) { m.st.Add(Item{Text: text, Quadrant: q, Due: due}) }
	add("late", QDo, "2026-11-01")
	add("soon", QDo, "2026-11-20")
	add("old undated", QDo, "")
	add("dated first", QDo, "2026-11-14")
	add("plan", QSchedule, "")
	add("sort me", QInbox, "")
	add("later", QDrop, "2026-12-01")
	m.st.Add(Item{Text: "finished", Quadrant: QDo, Done: true})

	s := summarize(m.st, day)
	if s.Level != plugin.LevelWarn || len(s.Widgets) != 3 {
		t.Fatalf("%+v", s)
	}
	top, bottom := s.Widgets[0], s.Widgets[1]
	do, sched, del, drop := top.Cells[0], top.Cells[1], bottom.Cells[0], bottom.Cells[1]
	if do.Title != "Do now" || sched.Title != "Schedule" || del.Title != "Delegate" || drop.Title != "Drop" {
		t.Fatalf("quadrant order: %v %v %v %v", do.Title, sched.Title, del.Title, drop.Title)
	}
	// the list's order: due date first (the overdue one is marked), undated last, done left out
	if got := strings.Join(do.Lines, "|"); got != "! late|dated first|soon|old undated" || do.Count != 4 || do.Level != plugin.LevelWarn {
		t.Errorf("do now: %q count %d level %v", got, do.Count, do.Level)
	}
	if sched.Count != 1 || sched.Level != plugin.LevelNone || del.Count != 0 || len(del.Lines) != 0 || drop.Lines[0] != "later" {
		t.Errorf("cells: %+v %+v %+v", sched, del, drop)
	}
	if tail := s.Widgets[2]; tail.Kind != plugin.KindText || tail.Text != "1 to sort · 1 overdue" || tail.Level != plugin.LevelWarn {
		t.Errorf("tail: %+v", tail)
	}
	want := []string{"4 do now · 1 to sort · 1 overdue", "1 schedule · 0 delegate · 1 drop", "next: ! late"}
	if strings.Join(s.Lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("fallback lines: %q", s.Lines)
	}
	// what the host sees: note text is user data, so Clean must keep it safe
	m.st.Add(Item{Text: "\x1b[31mred\x07" + strings.Repeat("x", 500), Quadrant: QDelegate})
	c := summarize(m.st, day).Clean()
	if n := c.Widgets[1].Cells[0].Lines[0]; strings.ContainsAny(n, "\x1b\x07") || len([]rune(n)) != plugin.MaxWidgetLabel {
		t.Errorf("note not cleaned: %q", n)
	}
	rows := 0
	for _, w := range c.Widgets {
		rows += w.Rows()
	}
	if rows != plugin.MaxSummaryRows || len(c.Widgets) != 3 {
		t.Errorf("%d rows", rows)
	}
}

func TestSummaryEmptyAndCalm(t *testing.T) {
	m, _ := newTest(t)
	s := summarize(m.st, day)
	if s.Level != plugin.LevelNone || len(s.Widgets) != 2 || s.Lines[0] != "0 do now · 0 to sort" || len(s.Lines) != 2 {
		t.Fatalf("empty store: %+v", s)
	}
}

func gridModel(t *testing.T, w, h int) *model {
	m, _ := newTest(t)
	for _, s := range []struct {
		t string
		q int
	}{{"do1", QDo}, {"do2", QDo}, {"do3", QDo}, {"do4", QDo}, {"do5", QDo}, {"sch1", QSchedule}, {"del1", QDelegate}, {"in1", QInbox}, {"in2", QInbox}} {
		m.st.Add(Item{Text: s.t, Quadrant: s.q})
	}
	m.refresh(0)
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func frame(t *testing.T, m *model) string {
	t.Helper()
	v := ansi.Strip(m.View())
	ls := strings.Split(v, "\n")
	if len(ls) != m.h {
		t.Fatalf("%d lines, want %d", len(ls), m.h)
	}
	for i, l := range ls {
		if w := ansi.StringWidth(l); w > m.w {
			t.Fatalf("line %d is %d wide (max %d): %q", i, w, m.w, l)
		}
	}
	return v
}

func TestGridFrame(t *testing.T) {
	m := gridModel(t, 50, 16)
	want := `Todo · 9 open · 2 to sort (t)
╭─ Do now 5 ────────────╮╭─ Schedule 1 ──────────╮
│ ▸ [ ] do1             ││   [ ] sch1            │
│   [ ] do2             ││                       │
│   [ ] do3             ││                       │
│   [ ] do4             ││                       │
│   [ ] do5             ││                       │
│                       ││                       │
╰───────────────────────╯╰───────────────────────╯
╭─ Delegate 1 ──────────╮╭─ Drop 0 ──────────────╮
│   [ ] del1            ││   —                   │
╰───────────────────────╯╰───────────────────────╯
╭─ Inbox 2 ──────────────────────────────────────╮
│   [ ] in1                                      │
│   [ ] in2                                      │
╰────────────────────────────────────────────────╯
`
	got := frame(t, m)
	if strings.TrimRight(got, " \n") != strings.TrimRight(want, " \n") {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestGridNavigation(t *testing.T) {
	m := gridModel(t, 80, 24)
	cur := func() string { return m.cur().Text }
	press(m, "j", "j")
	if cur() != "do3" {
		t.Fatalf("j moves within the pane: %s", cur())
	}
	press(m, "G", "j")
	if cur() != "do5" {
		t.Fatalf("j stops at the pane's end: %s", cur())
	}
	press(m, "g")
	if cur() != "do1" {
		t.Fatalf("g is the pane's first: %s", cur())
	}
	press(m, "j", "l")
	if cur() != "sch1" { // row 1 is past the pane's end: clamped
		t.Fatalf("l goes to the pane beside: %s", cur())
	}
	press(m, "l")
	if cur() != "sch1" {
		t.Fatalf("l at the right edge does nothing: %s", cur())
	}
	press(m, "h")
	if cur() != "do1" { // the row was clamped to Schedule's only note
		t.Fatalf("h goes back: %s", cur())
	}
	press(m, "tab")
	if cur() != "sch1" {
		t.Fatalf("tab: %s", cur())
	}
	press(m, "tab")
	if cur() != "del1" {
		t.Fatalf("tab: %s", cur())
	}
	press(m, "tab")
	if cur() != "in1" {
		t.Fatalf("tab skips the empty drop pane: %s", cur())
	}
	press(m, "tab")
	if cur() != "do1" {
		t.Fatalf("tab wraps round: %s", cur())
	}
	press(m, "shift+tab")
	if cur() != "in1" {
		t.Fatalf("shift+tab: %s", cur())
	}
	// editing keys act on the selected note as before; H toggles done
	press(m, "2")
	if m.cur() == nil || m.cur().Quadrant != QSchedule {
		t.Fatalf("priority keys still work: %+v", m.cur())
	}
	press(m, "H")
	if !m.showDone {
		t.Fatal("H shows done items")
	}
}

func TestGridFallsBackToList(t *testing.T) {
	for _, sz := range [][2]int{{40, 24}, {80, 9}} { // too narrow, too short
		m := gridModel(t, sz[0], sz[1])
		if v := frame(t, m); strings.Contains(v, "╭") || !strings.Contains(v, "Do now 5") {
			t.Fatalf("%v should be the grouped list:\n%s", sz, v)
		}
	}
	// in the list h/l and tab step between groups (do now, inbox, schedule...)
	m := gridModel(t, 40, 24)
	press(m, "l")
	if m.cur().Text != "in1" {
		t.Fatalf("l steps to the next group in list order: %s", m.cur().Text)
	}
	press(m, "G", "j")
	if m.cur().Text != "del1" {
		t.Fatalf("j walks the whole list: %s", m.cur().Text)
	}
}

func TestGridStates(t *testing.T) {
	// empty store and no filter match keep the friendly list messages
	m, _ := newTest(t)
	if v := frame(t, m); !strings.Contains(v, "Nothing to do") || strings.Contains(v, "╭") {
		t.Fatalf("empty store:\n%s", v)
	}
	m = gridModel(t, 60, 20)
	typ(m, "/zzz")
	if v := frame(t, m); !strings.Contains(v, "nothing matches") {
		t.Fatalf("no match:\n%s", v)
	}
	press(m, "esc")
	// typing and confirming keep the frame the right size, with a long hostile note
	m = gridModel(t, 60, 20)
	m.st.Add(Item{Text: strings.Repeat("日本語", 40), Quadrant: QDo, Who: "ana", Context: "code", Due: "2026-11-01"})
	m.refresh(0)
	press(m, "a")
	frame(t, m)
	press(m, "esc", "d")
	if v := frame(t, m); !strings.Contains(v, "Delete") {
		t.Fatalf("confirm footer:\n%s", v)
	}
	// scroll: the selection stays visible in a pane that overflows
	m = gridModel(t, 60, 12)
	press(m, "G")
	if v := frame(t, m); !strings.Contains(v, "▸ [ ] do5") {
		t.Fatalf("selected note scrolled into view:\n%s", v)
	}
}

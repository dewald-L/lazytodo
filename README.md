# lazytodo

A small todo plugin for [dock](https://github.com/dewald-L/dock), built so it works for you
and not the other way round. Capturing is one line of text. Everything else is optional.

```bash
dock plugin install --path ~/Projects/lazytodo github.com/dewald-L/lazytodo/dockplugin
go run . -dir ./tmp   # or run it standalone
```

## Flow

- `a` brain-dump: type, `enter` saves and the prompt stays open for the next one. `esc` stops.
- `tab` while typing asks optional questions for that item: priority (1 do now = urgent +
  important, 2 schedule = important, 3 delegate = urgent, 4 drop = neither), context
  (code / people / other), who, due. `enter` skips a question, `esc` keeps what you answered.
- `t` triage: walks through unsorted items, one keypress each (`s` skips).
- Items you never sort stay in **Inbox**; nothing is forced.

## Keys

`j k g G` move · `h l` / `tab` switch quadrant · `space` done · `[ ]` move todo between quadrants · `0-4` jump to quadrant (0 inbox) · `e` edit text · `E` edit all details ·
`d` delete · `/` filter (text, context, person) · `H` show done · `?` help · `q` close.

The open plugin shows the same 2x2 matrix as bordered panes (Do now / Schedule on top,
Delegate / Drop below) with the Inbox as a strip underneath. `j k g G` stay inside the
focused pane (the one holding the selected note), `h l` jump to the pane beside it,
`tab` / `shift+tab` cycle through all of them, and empty panes are skipped. A pane that
overflows scrolls and shows `↓N` for the notes below. On a terminal under 48 columns or
10 rows, or with nothing to show, it is the grouped list as before, where `h l` / `tab`
step between groups.

Due dates: `today`, `tom`, `fri`, `+3`, `+1w`, `12-24`, `2026-12-24`.

## Data and config

Todos: `$XDG_DATA_HOME/dock/lazytodo/todos.json`. Optional settings in
`$XDG_CONFIG_HOME/dock/plugins/lazytodo/config.json`:

```json
{ "ask": ["quadrant", "who", "due"], "contexts": ["code", "people", "admin"] }
```

The home screen shows your notes as a 2x2 matrix: **Do now** and **Schedule** on top,
**Delegate** and **Drop** below, each with its count and top notes in list order (due
date, then age; overdue ones start with `!` and turn the cell amber), plus a line for
the unsorted inbox. On a narrow or short terminal it shows text instead:
`N do now · M to sort · K overdue`, the other counts and the next note.

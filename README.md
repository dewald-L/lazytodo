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

`j k g G` move · `space` done · `0-4` set priority · `e` edit text · `E` edit all details ·
`d` delete · `/` filter (text, context, person) · `h` show done · `?` help · `q` close.

Due dates: `today`, `tom`, `fri`, `+3`, `+1w`, `12-24`, `2026-12-24`.

## Data and config

Todos: `$XDG_DATA_HOME/dock/lazytodo/todos.json`. Optional settings in
`$XDG_CONFIG_HOME/dock/plugins/lazytodo/config.json`:

```json
{ "ask": ["quadrant", "who", "due"], "contexts": ["code", "people", "admin"] }
```

The home screen shows `N do now · M to sort · K overdue`.

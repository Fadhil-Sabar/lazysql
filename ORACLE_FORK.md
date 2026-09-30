# LazySQL + Oracle (local fork)

This is a local fork of [`jorgerojas26/lazysql`](https://github.com/jorgerojas26/lazysql)
v0.5.9 that adds an **Oracle** driver, which upstream does not support.

## What was added

| File | Change |
| --- | --- |
| `drivers/oracle.go` | New `Oracle` driver implementing the full `drivers.Driver` interface on top of [`sijms/go-ora`](https://github.com/sijms/go-ora) (pure Go, **thin mode — no Oracle Instant Client needed**). |
| `drivers/constants.go` | `DriverOracle = "oracle"`. |
| `components/connection_selection.go` | Provider factory case + error message. |
| `components/connection_form.go` | Provider case for the `F2` "test connection" path. |
| `components/arg_connection.go` | Provider case for `lazysql <url>` CLI arguments. |
| `go.mod` / `go.sum` | `github.com/sijms/go-ora/v2 v2.9.0`. |

## Design notes

- Oracle has no "database" per connection; objects live in **schemas**. LazySQL's
  per-connection database argument is therefore treated as a schema name and
  `GetDatabases()` returns `SELECT username FROM all_users`.
- Oracle connections in `config.toml` must keep the **service name in the URL**
  (`oracle://user:pass@host:1521/SERVICE`) and should leave `DBName` **empty**,
  otherwise the sidebar pins to a service name that is not a schema.
- Metadata is read from the data dictionary: `all_users`, `all_objects`,
  `all_tab_columns`, `all_col_comments`, `all_constraints`, `all_cons_columns`,
  `all_indexes`, `all_ind_columns`, `all_tables`.
- Paging uses `OFFSET :1 ROWS FETCH NEXT :2 ROWS ONLY` (Oracle 12c+).
- `SupportsProgramming()` and `UseSchemas()` return `false`; functions,
  procedures and views are not listed yet (`GetFunctions`/`GetProcedures`/
  `GetViews` return "not implemented").

## Build / install

```bash
cd /home/diru/src/lazysql
go build -o /home/diru/go/bin/lazysql .
```

The stock v0.5.9 binary removed by this install is kept at
`/home/diru/go/bin/lazysql.v0.5.9.bak`.

## Tested

- `go build ./...`, `go vet ./drivers ./components`, `go test ./drivers ./components` pass.
- Driver registration verified: connecting to a bogus Oracle URL yields a TCP
  dial error from go-ora instead of "unsupported database provider".
- Live metadata queries were not exercised (they need VPN access to the Oracle
  hosts).

---

## Editor enhancement: in-query search

The custom SQL editor (`components/sql_editor.go`) now supports vim-style
search, since upstream had no find function at all.

| Key | Action |
| --- | --- |
| `/` (normal mode) | Forward search prompt |
| `?` (normal mode) | Backward search prompt |
| `Ctrl+F` (any mode) | Forward search prompt |
| `Enter` | Accept the pattern and keep matches highlighted |
| `Esc` (prompt) | Cancel search and clear the pattern |
| `Esc` (normal mode, no prompt) | Clear the active pattern first, then unfocus |
| `n` | Jump to next match |
| `N` | Jump to previous match |
| `*` | Next: search for the word under the cursor, or repeat the active pattern forward |
| `#` | Previous: same, but backward |

Details:

- Live incremental search: the cursor jumps to the first match while you type.
- Case-insensitive.
- All matches are highlighted (`app.Styles.EditorSelectionColor`); the cursor
  marks the active one.
- Wraps around the buffer in both directions.
- The status bar shows `/pattern` while typing and `/pattern [N matches]` once set.
- Implemented in `findMatches`, `searchJump`, `startSearch`,
  `handleSearchInput`, `drawSearchHighlights`; covered by
  `components/sql_editor_search_test.go`.

`*` / `#` follow vim semantics: with no active pattern they pick up the word
under the cursor (identifier characters `A-Za-z0-9_`, falling back to the word
just before the cursor when it sits on whitespace); with an active pattern they
repeat it in the forward/backward direction, exactly like `n`/`N`. Either way
the direction is remembered, so `n` keeps going the way you last searched.

## Panel navigation

Three focusable panels: **schema** (left column), **SQL editor** (right, top)
and **results** (right, bottom). Implemented in `components/home.go`
(`panelForFocus`, `focusSchemaPanel`, `focusEditorPanel`, `focusResultsPanel`,
`panelMoveLeft/Right/Up/Down`); keymap entries live in the `home` group
(`app/keymap.go`).

| Key | Action |
| --- | --- |
| `Ctrl+H` | focus schema panel |
| `Ctrl+L` | back to the right panel in use (editor or results) |
| `Ctrl+K` | up: results -> editor |
| `Ctrl+J` | down: editor -> results |
| `1` | focus schema panel |
| `2` | focus SQL editor (opens the editor tab if needed) |
| `3` | focus results |

Notes:

- Digits act as panel shortcuts only when a text field is not active; they are
  never stolen while typing in the editor (insert mode), a tree/table filter or
  the sidebar (`panelNumberAllowed`).
- `Ctrl+H` shares its byte with Backspace (0x08); terminals that send `0x7F`
  for Backspace are unaffected.
- The results metadata menu moved from `1`-`5` to `F1`-`F5` so the digits could
  be reused for panels; the on-screen hints now read `[F1]` ... `[F5]`.
- Tests: `components/panel_nav_test.go`, `app/keymap_test.go`.

### Panel labels, resize and collapse

Each panel border is titled with the digit that focuses it:

- `[1] Databases` (schema/tree)
- `[2] Editor` (SQL editor; the editor now has a border)
- `[3] Results` (result grid, or the table container in table tabs)

The focused panel can be resized and collapsed with the same keys:

| Key | Action |
| --- | --- |
| `+` / `=` | grow the focused panel (schema width, editor/results height) |
| `-` | shrink the focused panel |
| `T` | collapse/expand the focused panel (schema, editor or results) |

Details:

- `T` used to always toggle the tree; it is now panel-aware. Focus the schema
  panel (`1` or `Ctrl+H`) to collapse the tree.
- Resizing the editor/results is per-tab state (`editorHeight`,
  `editorCollapsed`, `resultsCollapsed` on `ResultsTable`); `nextEditorHeight`
  keeps both panes at least `minPanelHeight` rows.
- Growing/shrinking or focusing a collapsed pane re-expands it. A tab without
  an editor (a plain table) ignores editor/results resize and collapse.
- The panel shortcuts only apply when you are not typing (`canUsePanelShortcuts`).
- Tests: `components/panel_nav_test.go`, `app/keymap_test.go`.

## Clipboard and query execution

### Paste + yank

- `SQLEditor` implements `PasteHandler`, so bracketed paste (terminal
  `Ctrl+Shift+V`, `Cmd+V`, middle-click in most terminals) inserts the text at
  the cursor, including multi-line SQL (`insertText`).
- `Ctrl+V` also pastes from the system clipboard via `lib.Clipboard.Read`.
- Every yank (`v`-selection `y`, `yy`, `dd`, and deleting a selection) now also
  writes to the system clipboard through `setYankText`, not just the internal
  `yankText` register.

### Execute with `;` (DBeaver-style)

Running a query (`Ctrl+R`, or `Ctrl+E` while the editor already has focus)
executes only what the cursor is on, instead of blindly sending the whole
buffer to the driver:

1. If there is a visual selection, that selection is executed.
2. Otherwise the statement under the cursor is executed: from the previous `;`
   to the next `;`, or to the end of the buffer when there is none.
3. A trailing `;` is stripped before the driver sees the query, so statements
   copied from DBeaver work unchanged.

Semicolons inside string literals (single/double/backtick quotes, with doubled
quotes) and inside `--` line / `/* */` block comments are ignored
(`statementRanges`, `statementAtOffset`, `stripTrailingSemicolon`).

`Ctrl+E` behavior: opens/focuses the SQL editor when focus is elsewhere;
when the editor already has focus it runs the query (the old Ctrl+E binding is
still used for "open editor").

Tests: `components/sql_editor_query_test.go`.

## Visual mode fixes

- `drawSelection` never recorded the end column unless the selection reached the
  end of the line, so a normal `v` + `h`/`l`/`b`/`e` selection was invisible. The
  column loop now walks to and past the inclusive end.
- **`e` (word end) is implemented** (`wordEnd`) and wired into both normal and
  visual mode. `w` was also corrected to Vim semantics (start of the next word
  instead of the end of the current one).
- The selection is now **inclusive of the character under the cursor**, matching
  Vim: `getSelectedText`, `deleteSelection` and `drawSelection` all include the
  cursor cell, so yanking/deleting/executing a selection grabs the same text Vim
  would.

Tests: `components/sql_editor_visual_test.go` (same-line, backward, word and
multi-line selection rendering, inclusive `getSelectedText`, `w`/`b`/`e`).

## Connection picker

`components/connections_table.go` was reworked from a flat name list into a
sortable, searchable table with a type column:

- **Sorted by name ascending** (case-insensitive) by default; `s` toggles
  ascending/descending and the header shows `Name ▲` / `Name ▼`.
- **Type column** shows the database type (`PostgreSQL`, `Oracle`, `MySQL`,
  `SQLite`, `SQL Server`, `ClickHouse`) via `providerLabel`, falling back to the
  URL scheme when `Provider` is unset.
- **`/` search** focuses a filter input at the bottom (`/…` prompt, like the
  editor's search). Typing filters live, matching against name, provider, type
  and URL; `Esc` clears the filter, `Enter` keeps it and returns focus to the
  list.
- A fixed header row is used, so `SelectedConnection()` / `SelectedConnection`
  map the table row to the displayed connection. Delete and edit now act on the
  selected connection by identity, so they stay correct under sorting/filtering.

Keymap (`app/keymap.go`, `connection` group): `/` → `Search`, `s` →
`ToggleSort`. Tests: `components/connections_table_test.go`.

### Connection picker: layout and focus

- The `/` filter input sits **below** the New/Connect/Edit/Delete/Quit buttons.
- Key flow:
  - `/` focuses the filter and filters live as you type.
  - `Enter` or `Esc` **while in the filter** returns focus to the list and
    **keeps** the filter, so the narrowed rows can still be selected.
  - `Esc` **on the list** clears the filter (restores the full list).
  - `s` toggles the name sort order.
- Do **not** call `App.ForceDraw()` / `App.QueueUpdateDraw()` from inside a
  `SetChangedFunc` / `SetDoneFunc` / input-capture callback: tview invokes those
  from its event loop and the redraw re-enters the loop, deadlocking the UI
  (this is what made the search field unresponsive after one keystroke). The
  event loop redraws automatically after each key, so no explicit draw is
  needed there.

### Visual-line selection

`V` (visual line) highlights whole lines, but yank / delete / execute used to
take only the cursor-column slice of the line (`getSelectionRange`), so a cursor
mid-line truncated the text — e.g. executing
`DELETE ... WHERE value = '000020';` sent `... value = '000020` and Postgres
rejected it with `pq: unterminated quoted string at or near "'"`. All of
`getSelectedText`, `deleteSelection` and `QueryToExecute` now go through
`effectiveSelectionRange`, which expands to whole lines in visual-line mode, and
visual-line delete removes the lines entirely (vim `V` + `d`). Tests in
`components/sql_editor_visual_test.go`.

### DML affected-rows feedback

Non-SELECT statements returned their status (`N rows affected`) via
`SetResultsInfo`, which lives on the hidden "results info" page, while the visible
pagination status was cleared — so running a DELETE/UPDATE looked like nothing
happened. `runEditorDMLQuery` now also calls `SetQueryStatus(result)`, so the
count shows in the status line under the results grid.

<a name="readme-top"></a>

<div align="center">
  <h3 align="center">LAZYSQL</h3>
  <p align="center">
    A cross-platform TUI database management tool written in Go.<br/>
    <b>Fork of <a href="https://github.com/jorgerojas26/lazysql">jorgerojas26/lazysql</a>, based on v0.5.9.</b>
  </p>
</div>

> **What this fork adds:** a pure-Go **Oracle** driver, a much better **SQL editor**
> (vim-style search, clipboard, DBeaver-style execution), **panel navigation and
> resizing**, and a **sortable, searchable connection picker** with a DB-type
> column. Everything else is upstream LazySQL.
>
> - Detailed fork notes: [`ORACLE_FORK.md`](ORACLE_FORK.md)
> - Full upstream docs (config reference, themes, CLI/test tooling, all
>   keybindings): [upstream README](https://github.com/jorgerojas26/lazysql#readme)

---

## Enhancements

### Oracle support

- New driver on pure-Go `github.com/sijms/go-ora/v2` (thin mode) — **no Oracle
  Instant Client required**.
- Use the `oracle` provider: `oracle://user:pass@host:1521/SERVICE`.
- Oracle has no per-connection database, only schemas, so LazySQL lists
  **schemas as the top-level nodes**. Leave `DBName` empty to list every schema
  the user can access.
- Not implemented yet in the Oracle driver: functions / procedures / views
  listing and definitions (use DBeaver as a fallback for those).

### SQL editor

Vim-style **search** (also see the status bar for the match count):

| Key | Action |
| --- | --- |
| `/` | forward search prompt |
| `?` | backward search prompt |
| `Ctrl+F` | search prompt (any mode) |
| `n` / `N` | next / previous match |
| `*` / `#` | next / previous match of the word under the cursor |
| `Enter` | accept the pattern and keep matches highlighted |
| `Esc` | cancel the prompt; on the buffer, clear the highlight |

- Case-insensitive, live incremental (cursor jumps as you type), wraps around,
  highlights every match.

**Clipboard**

- Bracketed paste works (terminal `Ctrl+Shift+V` / middle click), and `Ctrl+V`
  reads the system clipboard. Multi-line SQL inserts at the cursor.
- **Yanks copy to the system clipboard**: `y` (selection), `yy`, `dd`, and
  deleting a selection. `p` / `P` paste.

**Execution (DBeaver-style)**

`Ctrl+R` (or `Ctrl+E` while the editor has focus) runs **only what the cursor is
on**, not the whole buffer:

1. an active visual selection, otherwise
2. the statement under the cursor — from the previous `;` to the next one, or to
   the end of the buffer,
3. with the trailing `;` stripped before it reaches the driver.

Semicolons inside `'…'`, `"…"`, `` `…` ``, `--` comments and `/* … */` blocks are
ignored. Visual mode selection is inclusive of the cursor character and supports
the `w` / `b` / `e` word motions.

### Transactions (explicit COMMIT / ROLLBACK)

`BEGIN` (or `START TRANSACTION`) opens a **pinned session**: from that statement
on, every `Ctrl+R` in the tab runs on the same connection, so the transaction
survives across runs until `COMMIT` / `ROLLBACK`. Statements inside it see your
own uncommitted writes; other sessions do not.

Every execution is listed in the **transaction panel** (`[4] Transaction`) beside
the editor with its row count and state (`autocommit`, `pending`, `committed`,
`rolled back`, `failed`).

| Key | Action |
| --- | --- |
| `c` | commit the open transaction |
| `r` | roll back the open transaction |
| `x` | drop finished history entries |
| `y` | copy the highlighted statement |
| `j` / `k` | walk the history |
| `q` / `Esc` | back to the editor |

The same `c` / `r` work on the results grid. Both ask for confirmation first: the
dialog lists the pending statements, the tables affected, the total rows and when
the transaction started. `Esc` / `No` cancels it.

#### Manual commit mode

Typing `BEGIN` every time is easy to forget, so the commit mode itself is
switchable:

| Key | Where |
| --- | --- |
| `Ctrl+B` | anywhere on the connection |
| `m` | transaction panel |

- **AUTO** (default): statements are written immediately, exactly as before.
- **MANUAL**: the first `INSERT` / `UPDATE` / `DELETE` in a tab opens a
  transaction for you (the panel shows a `BEGIN (manual commit)` row), and
  nothing is written until you press `c`. Other sessions do not see the change,
  your own `SELECT`s do, and `r` throws it away.

The panel title carries the mode (`[4] Transaction · ACTIVE · 2 pending · 1 rows · manual`)
and the footer shows `m manual: ON/OFF`. The mode is per connection and lasts for
the session. Reads (`SELECT`) never open a transaction, and DDL keeps its
immediate behaviour. Drivers without transactions (ClickHouse) report that
manual mode is unavailable instead of silently accepting it.

Worth knowing:

- Closing the tab rolls an open transaction back, so a forgotten transaction
  cannot keep holding locks.
- `Ctrl+S` (execute grid pending changes) applies queued cell changes **inside**
  the open transaction, so they commit together with `c` (in manual mode it opens
  the transaction first). Tabs without an editor have no session and no panel, so
  they keep the immediate behaviour.
- A `SELECT` inside a transaction is read-your-writes but is not streamed, so it
  uses the configured query row cap.
- Drivers without interactive transactions (ClickHouse) report that `BEGIN` is
  unsupported and keep working as before.

### Panels

Four focusable panels: **schema** (left), **SQL editor** (right, top),
**results** (right, bottom) and **transaction** (right, beside the editor).
Border titles show their focus digit (`[1] Databases`, `[2] Editor`,
`[3] Results`, `[4] Transaction`).

| Key | Action |
| --- | --- |
| `Ctrl+H` | focus schema panel |
| `Ctrl+L` | move right: schema → editor → transaction panel |
| `Ctrl+K` / `Ctrl+J` | up: results/transaction → editor / down: editor/transaction → results |
| `1` / `2` / `3` / `4` | focus schema / editor / results / transaction |
| `+`, `=` / `-` | grow / shrink the focused panel |
| `T` | collapse / expand the focused panel |

- Digits are ignored while typing (editor insert mode, filters, sidebar).
- The results metadata menu moved from `1`–`5` to **`F1`–`F5`** to free the digits.

### Connection picker

- **Sorted by name ascending** (case-insensitive); `s` toggles ascending/descending.
- **Type column** showing `PostgreSQL`, `Oracle`, `MySQL`, `SQLite`, `SQL Server`, `ClickHouse`.
- **`/` filter** below the buttons: live filter on name, type or URL.
  `Enter`/`Esc` in the field returns to the list **keeping** the filter; `Esc` on
  the list clears it.
- Edit/delete follow the selected connection even after sorting/filtering.

---

## Install

Requires **Go 1.23+**. The module path is still upstream's, so build from source:

```bash
git clone https://github.com/Fadhil-Sabar/lazysql.git
cd lazysql
go build -o lazysql .
# or: go install .
```

Install to your `PATH`:

```bash
install -m755 lazysql ~/go/bin/lazysql
```

## Configuration

Config lives at `${XDG_CONFIG_HOME}/lazysql/config.toml`, or
`~/.config/lazysql/config.toml` on Linux, `~/Library/Application Support/lazysql/config.toml`
on macOS and `%APPDATA%\lazysql\config.toml` on Windows.

```toml
[[database]]
Name = 'Prod (Oracle)'
Provider = 'oracle'
URL = 'oracle://user:password@host:1521/SERVICE'

[[database]]
Name = 'Dev (Postgres)'
Provider = 'postgres'
DBName = 'app'            # omit to list every database in the instance
URL = 'postgres://${env:DB_USER}:${env:DB_PASSWORD}@localhost:5432/app?sslmode=disable'
ReadOnly = true

[application]
DefaultPageSize = 300
DisableSidebar = false
SidebarOverlay = false
max_query_rows = 1000

[theme]
Preset = "dracula"
```

- `${env:VAR}` expands environment variables (keep passwords out of the file).
- `Commands = [{ Command = 'ssh -tt bastion -L ${port}:localhost:5432', WaitForPort = '${port}' }]`
  runs a command before connecting (bastion / tunnel / port-forward).
- Keybindings are remappable per group, e.g.:

```toml
[keymap.Home]
ToggleQueryHistory = "F2"
```

See the [upstream README](https://github.com/jorgerojas26/lazysql#readme) for the
full configuration reference, themes, and the manual DB test environment.

## Keybindings (quick reference)

| Context | Keys |
| --- | --- |
| Connection picker | `/` filter · `s` sort · `c`/`Enter` connect · `n` new · `e` edit · `d` delete · `q` quit |
| Panels | `Ctrl+H/J/K/L` navigate · `1/2/3/4` focus · `+`/`-` resize · `T` collapse · `Ctrl+B` manual/auto commit |
| Editor | `/` `?` `Ctrl+F` search · `n`/`N` `*`/`#` matches · `Ctrl+R`/`Ctrl+E` run · `Ctrl+V` paste · `Ctrl+Space` external editor |
| Results grid | `c` commit · `r` rollback · `i` change cell · `d` delete row · `Ctrl+S` run pending changes |
| Transaction panel | `c` commit · `r` rollback · `m` manual/auto commit · `x` clear · `y` copy · `j`/`k` history · `q` back |
| Home | `Ctrl+E` editor · `Ctrl+S` run pending changes · `Ctrl+P` global search · `Ctrl+_` query history · `Backspace` connections · `?` help · `q` quit |

Full tables (Home, Tree, Table, Sidebar, JSON viewer, …) are in the
[upstream README](https://github.com/jorgerojas26/lazysql#readme).

## Clipboard

Uses [`atotto/clipboard`](https://github.com/atotto/clipboard). On Linux, install
`xclip` or `xsel` (Wayland: `wl-clipboard`).

## Database support

MySQL, PostgreSQL, SQLite, MSSQL, ClickHouse, and **Oracle** (this fork). MongoDB
is not supported.

## License

Distributed under the **MIT License**. See [`LICENSE.txt`](LICENSE.txt).

Original project: [jorgerojas26/lazysql](https://github.com/jorgerojas26/lazysql)
— Copyright (c) 2023 Jorge Rojas. This fork keeps the upstream license and
attribution; the enhancements above are additions on top of v0.5.9.

<p align="right">(<a href="#readme-top">back to top</a>)</p>

package components

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/jorgerojas26/lazysql/drivers"
	"github.com/jorgerojas26/lazysql/models"
)

func newTransactionTestTable() *ResultsTable {
	changes := []models.DBDMLChange{}
	tree := NewTree("db", &drivers.Postgres{}, nil)
	return NewResultsTable(&changes, tree, &drivers.Postgres{}, nil, "id", "postgres://example", false).WithEditor()
}

func TestWithEditorBuildsTransactionPanelBesideEditor(t *testing.T) {
	table := newTransactionTestTable()

	if table.TxPanel == nil {
		t.Fatal("WithEditor did not build the transaction panel")
	}
	if table.EditorRow == nil {
		t.Fatal("WithEditor did not build the editor row")
	}
	if table.txState == nil {
		t.Fatal("WithEditor did not build the transaction state")
	}
	if table.txPanelWidth != defaultTxPanelWidth {
		t.Fatalf("txPanelWidth = %d, want %d", table.txPanelWidth, defaultTxPanelWidth)
	}

	// The panel must sit beside the editor: editor first, panel second.
	if got := table.EditorRow.GetItemCount(); got != 2 {
		t.Fatalf("editor row has %d items, want 2 (editor + transaction panel)", got)
	}
	if !table.transactionPanelVisible() {
		t.Fatal("transaction panel should start visible")
	}
	if table.hasActiveTransaction() {
		t.Fatal("a fresh editor tab must not report an active transaction")
	}
}

func TestEditorRouteUsesSessionForTransactions(t *testing.T) {
	table := &ResultsTable{txState: NewTransactionState()}

	cases := []struct {
		name  string
		query string
		verb  string
		want  editorExecutionRoute
	}{
		{"plain select stays on the pool", "SELECT 1", "SELECT", editorRoutePool},
		{"plain dml stays on the pool", "DELETE FROM t", "DELETE", editorRoutePool},
		{"begin opens a session", "BEGIN;", "BEGIN", editorRouteSession},
		{"start transaction opens a session", "START TRANSACTION", "START", editorRouteSession},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := table.editorRoute(tc.query, tc.verb); got != tc.want {
				t.Errorf("editorRoute(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}

	table.txState.Begin()
	if got := table.editorRoute("DELETE FROM t", "DELETE"); got != editorRouteSession {
		t.Errorf("statements inside a transaction must run on the session, got %v", got)
	}
	if got := table.editorRoute("SELECT 1", "SELECT"); got != editorRouteSession {
		t.Errorf("reads inside a transaction must run on the session, got %v", got)
	}

	table.txState.Resolve(txOutcomeCommitted)
	if got := table.editorRoute("DELETE FROM t", "DELETE"); got != editorRoutePool {
		t.Errorf("statements after the transaction resolve must use the pool, got %v", got)
	}
}

func TestNextTxPanelWidth(t *testing.T) {
	cases := []struct {
		name    string
		current int
		delta   int
		total   int
		want    int
	}{
		{"grows", 34, 2, 120, 36},
		{"shrinks", 34, -2, 120, 32},
		{"floors at min", 18, -10, 120, minTxPanelWidth},
		{"caps at half of the row", 58, 10, 120, 60},
		{"narrow terminal keeps it usable", 20, 5, 10, minTxPanelWidth},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextTxPanelWidth(tc.current, tc.delta, tc.total); got != tc.want {
				t.Errorf("nextTxPanelWidth(%d, %d, %d) = %d, want %d",
					tc.current, tc.delta, tc.total, got, tc.want)
			}
		})
	}
}

func TestTransactionPanelKeysTriggerHandlers(t *testing.T) {
	panel := NewTransactionPanel(NewTransactionState())

	commits, rollbacks, clears, unfocuses, toggles := 0, 0, 0, 0, 0
	panel.SetHandlers(
		func() { commits++ },
		func() { rollbacks++ },
		func() { clears++ },
		func() { unfocuses++ },
		func() { toggles++ },
	)

	press := func(event *tcell.EventKey) {
		t.Helper()
		if remaining := panel.inputCapture(event); remaining != nil {
			t.Fatalf("panel did not consume %q", event.Name())
		}
	}

	press(tcell.NewEventKey(tcell.KeyRune, 'c', 0))
	press(tcell.NewEventKey(tcell.KeyRune, 'r', 0))
	press(tcell.NewEventKey(tcell.KeyRune, 'x', 0))
	press(tcell.NewEventKey(tcell.KeyRune, 'm', 0))
	press(tcell.NewEventKey(tcell.KeyEscape, 0, 0))

	if commits != 1 || rollbacks != 1 || clears != 1 || unfocuses != 1 || toggles != 1 {
		t.Fatalf("handler counts = commit:%d rollback:%d clear:%d unfocus:%d toggle:%d, want 1 each",
			commits, rollbacks, clears, unfocuses, toggles)
	}

	// Unbound keys must pass through so the panel never swallows editor input.
	if remaining := panel.inputCapture(tcell.NewEventKey(tcell.KeyRune, 'z', 0)); remaining == nil {
		t.Fatal("unbound key should not be consumed by the transaction panel")
	}
}

func TestTransactionPanelTitleReflectsState(t *testing.T) {
	state := NewTransactionState()
	panel := NewTransactionPanel(state)

	if title := panel.Wrapper.GetTitle(); !strings.Contains(title, "Transaction") {
		t.Fatalf("panel title = %q, want it to mention Transaction", title)
	}

	entryID := state.Record("DELETE FROM public.orders WHERE id = 1;", "DELETE", true)
	state.Complete(entryID, txKindAffected, 2, nil)
	state.Begin()

	panel.Render()
	title := panel.Wrapper.GetTitle()

	if !strings.Contains(title, "ACTIVE") {
		t.Errorf("title = %q, want it to report an active transaction", title)
	}
	if !strings.Contains(title, "2 rows") {
		t.Errorf("title = %q, want it to report the pending row count", title)
	}

	// The history list renders one row per statement.
	if got := panel.List.GetRowCount(); got != 1 {
		t.Errorf("history rows = %d, want 1", got)
	}
}

func TestTransactionPanelFocusAndClear(t *testing.T) {
	table := newTransactionTestTable()

	// Clear on an empty history is a no-op, not a crash.
	table.ClearTransactionHistory()

	// A transaction cannot be ended without one being open.
	if table.hasActiveTransaction() {
		t.Fatal("tab should not report an active transaction yet")
	}
}

func TestHomeOwnsFrontPageDialogRule(t *testing.T) {
	pages := tview.NewPages()
	home := &Home{Flex: tview.NewFlex()}

	pages.AddPage("home", home.Flex, true, true)
	if !homeOwnsFrontPage(pages, home) {
		t.Fatal("home should own the keyboard while it is the front page")
	}

	// A dialog above the connection page keeps the keyboard: tview can still
	// route a key to home through a stale focus flag, and home shortcuts must
	// not steal focus back from the dialog.
	pages.AddPage("dialog", tview.NewModal(), true, true)
	if homeOwnsFrontPage(pages, home) {
		t.Fatal("an open dialog must own the keyboard")
	}

	pages.RemovePage("dialog")
	if !homeOwnsFrontPage(pages, home) {
		t.Fatal("home should own the keyboard again once the dialog closes")
	}

	if !homeOwnsFrontPage(nil, home) {
		t.Fatal("without a page container home keeps its shortcuts working")
	}
}

func TestHomePageItemMayBeTheBareFlex(t *testing.T) {
	// Connections opened from a URL argument add home.Flex instead of *Home.
	pages := tview.NewPages()
	home := &Home{Flex: tview.NewFlex()}
	pages.AddPage("home", home, true, true)
	if !homeOwnsFrontPage(pages, home) {
		t.Fatal("home should own the keyboard when the page item is *Home")
	}
}

func withTransactionInfoPage(t *testing.T, fn func(table *ResultsTable)) {
	t.Helper()

	original := mainPages
	mainPages = tview.NewPages()
	t.Cleanup(func() { mainPages = original })

	table := newTransactionTestTable()
	fn(table)
}

func TestCommitWithoutTransactionExplainsInsteadOfActing(t *testing.T) {
	withTransactionInfoPage(t, func(table *ResultsTable) {
		table.CommitTransaction()

		name, _ := mainPages.GetFrontPage()
		if name != pageNameTransactionInfo {
			t.Fatalf("front page = %q, want the transaction info dialog", name)
		}
		if table.hasActiveTransaction() {
			t.Fatal("commit must not open a transaction")
		}
	})
}

func TestRollbackWithoutTransactionExplainsInsteadOfActing(t *testing.T) {
	withTransactionInfoPage(t, func(table *ResultsTable) {
		table.RollbackTransaction()

		name, _ := mainPages.GetFrontPage()
		if name != pageNameTransactionInfo {
			t.Fatalf("front page = %q, want the transaction info dialog", name)
		}
	})
}

func TestClearHistoryRefusedWhileTransactionIsOpen(t *testing.T) {
	withTransactionInfoPage(t, func(table *ResultsTable) {
		state := table.txState
		state.Begin()

		table.ClearTransactionHistory()

		name, _ := mainPages.GetFrontPage()
		if name != pageNameTransactionInfo {
			t.Fatalf("front page = %q, want the transaction info dialog", name)
		}
	})
}

func TestEditorRouteManualCommitWrapsMutations(t *testing.T) {
	table := &ResultsTable{txState: NewTransactionState()}

	// Auto commit: only BEGIN/START TRANSACTION open a session.
	if got := table.editorRoute("DELETE FROM public.crud_lab", "DELETE"); got != editorRoutePool {
		t.Errorf("auto commit DELETE route = %v, want the pool", got)
	}

	table.Home = &Home{manualCommit: true}

	if got := table.editorRoute("DELETE FROM public.crud_lab", "DELETE"); got != editorRouteSession {
		t.Errorf("manual commit DELETE route = %v, want the pinned session", got)
	}
	if got := table.editorRoute("UPDATE public.crud_lab SET qty = 1", "UPDATE"); got != editorRouteSession {
		t.Errorf("manual commit UPDATE route = %v, want the pinned session", got)
	}
	if got := table.editorRoute("INSERT INTO public.crud_lab (code) VALUES ('x')", "INSERT"); got != editorRouteSession {
		t.Errorf("manual commit INSERT route = %v, want the pinned session", got)
	}
	// Reads must not open a transaction on their own.
	if got := table.editorRoute("SELECT * FROM public.crud_lab", "SELECT"); got != editorRoutePool {
		t.Errorf("manual commit SELECT route = %v, want the pool", got)
	}
	// DDL keeps its immediate behaviour: the schema refresh expects it applied.
	if got := table.editorRoute("DROP TABLE public.nope", "DROP"); got != editorRoutePool {
		t.Errorf("manual commit DROP route = %v, want the pool", got)
	}
}

func TestToggleCommitModeFlipsModeAndReportsIt(t *testing.T) {
	table := newTransactionTestTable()
	home := &Home{}
	table.Home = home

	if table.manualCommitEnabled() {
		t.Fatal("manual commit should start disabled")
	}

	table.ToggleCommitMode()

	if !home.manualCommit {
		t.Fatal("ToggleCommitMode did not enable manual commit")
	}
	status := table.Pagination.GetResultStatus()
	if !strings.Contains(status, "MANUAL") {
		t.Errorf("status after enabling manual commit = %q, want it to mention MANUAL", status)
	}
	if title := table.TxPanel.Wrapper.GetTitle(); !strings.Contains(title, "manual") {
		t.Errorf("panel title = %q, want it to mention manual", title)
	}

	table.ToggleCommitMode()

	if home.manualCommit {
		t.Fatal("ToggleCommitMode did not restore auto commit")
	}
	if status := table.Pagination.GetResultStatus(); !strings.Contains(status, "AUTO") {
		t.Errorf("status after disabling manual commit = %q, want it to mention AUTO", status)
	}
}

func TestToggleCommitModeWithoutConnectionIsNoop(t *testing.T) {
	table := newTransactionTestTable()
	table.Home = nil

	table.ToggleCommitMode() // must not panic

	if table.manualCommitEnabled() {
		t.Fatal("standalone tables have no manual commit mode")
	}
}

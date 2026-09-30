package components

import (
	"context"
	"errors"
	"fmt"

	"github.com/rivo/tview"

	"github.com/jorgerojas26/lazysql/app"
	"github.com/jorgerojas26/lazysql/drivers"
)

// This file wires explicit transactions into the editor tab. The rule is
// simple: a statement typed in the editor runs on the pool (today's behaviour)
// until it is a BEGIN/START TRANSACTION or a transaction is already open; from
// then on every statement runs on one pinned session so COMMIT/ROLLBACK can
// really end the transaction, possibly many editor runs later.

// editorExecutionRoute decides where an editor statement runs.
type editorExecutionRoute int

const (
	editorRoutePool editorExecutionRoute = iota
	editorRouteSession
)

func (table *ResultsTable) editorRoute(query, verb string) editorExecutionRoute {
	if table.txState != nil && table.txState.Active() {
		return editorRouteSession
	}
	if isTransactionBeginQuery(query, verb) {
		return editorRouteSession
	}
	return editorRoutePool
}

// driverSupportsTransactions reports whether the connected driver can pin a
// session. Drivers without that capability (ClickHouse) keep working exactly as
// before; BEGIN simply reports an error.
func (table *ResultsTable) driverSupportsTransactions() bool {
	_, ok := table.DBDriver.(drivers.SessionDriver)
	return ok
}

// openTransactionSession pins a connection for the tab's explicit transaction.
func (table *ResultsTable) openTransactionSession(ctx context.Context, database string) error {
	sessionDriver, ok := table.DBDriver.(drivers.SessionDriver)
	if !ok {
		return fmt.Errorf("%s connections do not support explicit transactions", table.DBDriver.GetProvider())
	}

	session, err := sessionDriver.OpenSession(ctx, database)
	if err != nil {
		return err
	}

	table.txMu.Lock()
	if table.txSession != nil {
		table.txMu.Unlock()
		_ = session.Close()
		return nil
	}
	table.txSession = session
	table.txDatabase = database
	table.txMu.Unlock()

	return nil
}

func (table *ResultsTable) txSessionSnapshot() drivers.Session {
	table.txMu.Lock()
	defer table.txMu.Unlock()
	return table.txSession
}

// closeTransactionSession releases the pinned connection. If the database
// still has the transaction open, dropping the connection rolls it back.
func (table *ResultsTable) closeTransactionSession() {
	table.txMu.Lock()
	session := table.txSession
	table.txSession = nil
	table.txDatabase = ""
	table.txMu.Unlock()

	if session != nil {
		_ = session.Close()
	}
}

// CloseTransaction rolls back and releases any transaction still open. It is
// called when the tab closes so a forgotten transaction cannot hold locks.
func (table *ResultsTable) CloseTransaction() {
	if table.txState == nil {
		return
	}

	if table.txState.Active() {
		if session := table.txSessionSnapshot(); session != nil {
			entryID := table.txState.Record("ROLLBACK;", "ROLLBACK", false)
			affected, err := session.Exec(context.Background(), "ROLLBACK")
			table.txState.Complete(entryID, txKindControl, affected, err)
		}
		table.txState.Resolve(txOutcomeRolledBack)
	}
	table.closeTransactionSession()
}

func (table *ResultsTable) renderTransactionPanel() {
	if table.TxPanel != nil {
		table.TxPanel.Render()
	}
}

// runEditorTransactionStatement executes a statement on the pinned session.
func (table *ResultsTable) runEditorTransactionStatement(ctx context.Context, run *editorQueryRun, query, verb string) {
	generation := run.generation
	if run.cancel != nil {
		defer run.cancel()
	}
	if ctx == nil || ctx.Err() != nil || !table.isCurrentLoad(ctx, generation) {
		return
	}

	table.addEditorQueryToHistory(query)

	inTx := table.txState.Active()
	entryID := table.txState.Record(query, verb, inTx)

	var (
		err         error
		affected    int64
		kind        txKind
		isQuery     = isResultProducingQuery(query)
		result      string
		streamed    drivers.QueryStreamResult
		openedHere  bool
		shouldBegin bool
	)

	if isTransactionBeginQuery(query, verb) {
		shouldBegin = true
	}

	if shouldBegin {
		if err = table.openTransactionSession(ctx, run.database); err == nil {
			openedHere = true
		}
	}

	if err == nil {
		session := table.txSessionSnapshot()
		switch {
		case session == nil:
			err = errors.New("no transaction session is available")
		case isQuery:
			kind = txKindRows
			onBatch := func(batch drivers.QueryBatch) error {
				return table.renderEditorQueryBatch(ctx, run, batch)
			}
			streamed, err = session.StreamQuery(ctx, query, table.maxInteractiveQueryRows(), onBatch)
			affected = int64(streamed.Rows)
		default:
			kind = txKindControl
			if txMutatingVerb(verb) {
				kind = txKindAffected
			}
			affected, err = session.Exec(ctx, query)
		}
	}

	if err == nil && !isQuery {
		result = txResultLabel(kind, affected)
	}

	if ctx.Err() != nil {
		return
	}

	table.txState.Complete(entryID, kind, affected, err)

	switch {
	case err != nil:
		// Keep the session open. On PostgreSQL the transaction is now aborted
		// and only ROLLBACK can end it, which the panel surfaces.
	case openedHere:
		table.txState.Begin()
	case isTransactionEndQuery(query, verb):
		if isTransactionRollbackQuery(query, verb) {
			table.txState.Resolve(txOutcomeRolledBack)
		} else {
			table.txState.Resolve(txOutcomeCommitted)
		}
		table.closeTransactionSession()
	}

	App.QueueUpdateDraw(func() {
		if !table.isCurrentLoad(ctx, generation) {
			return
		}

		table.finishEditorQuery(run)
		table.SetLoading(false)

		if err != nil {
			table.SetQueryStatus(fmt.Sprintf("Query failed: %s", err.Error()))
			table.SetError(err.Error(), nil)
			table.renderTransactionPanel()
			return
		}

		if isQuery {
			table.SetResultsInfo(fmt.Sprintf("%d rows", streamed.Rows))
			if streamed.Rows == 0 && len(streamed.Columns) > 0 {
				table.appendEditorQueryBatch(drivers.QueryBatch{Columns: streamed.Columns})
			}
			table.Pagination.SetLimit(table.editorQueryRowCount())
			if streamed.Truncated {
				table.Pagination.SetPageInfo(table.editorQueryRowCount(), true)
				table.SetQueryStatus(fmt.Sprintf("%d rows shown — result truncated (maximum %d)",
					table.editorQueryRowCount(), table.maxInteractiveQueryRows()))
			} else {
				table.Pagination.SetPageInfo(table.editorQueryRowCount(), false)
				table.SetQueryStatus(fmt.Sprintf("%d rows", streamed.Rows))
			}
		} else {
			table.SetResultsInfo(result)
			table.SetQueryStatus(result)
			// A control/DML statement produces no grid rows; clear the previous
			// result set so it cannot be mistaken for this statement's output.
			table.SetRecords([][]string{})
			table.Pagination.SetPageInfo(0, true)
			table.EditorPages.SwitchToPage(pageNameTableEditorTable)
		}

		table.renderTransactionPanel()
		table.keepEditorFocus()
	})
}

// keepEditorFocus leaves the cursor in the editor after a transaction
// statement so the user can keep typing without leaving the transaction flow.
//
// It only reclaims focus when the tab still owns it: a completion must never
// drag the cursor back out of the transaction panel (or another panel) that the
// user moved to while the statement was running.
func (table *ResultsTable) keepEditorFocus() {
	if table.Editor == nil {
		return
	}

	switch App.GetFocus() {
	case tview.Primitive(table.Editor), tview.Primitive(table):
		// Still inside this tab's editor/result flow: put the cursor back in the
		// editor. The transaction panel is deliberately excluded so a completion
		// cannot yank focus out of it.
	default:
		return
	}

	// For editor tabs SetIsFiltering is the "the editor owns focus" flag that
	// Home uses to resolve the current panel; it must stay true here or the
	// panel shortcuts would target the results grid instead.
	table.SetIsFiltering(true)
	table.HighlightAll()
	table.RemoveHighlightTable()
	App.SetFocus(table.Editor)
	table.Editor.Highlight()
}

// transactionEntryCount reports how many statements the panel is showing.
func (table *ResultsTable) transactionEntryCount() int {
	if table.txState == nil {
		return 0
	}
	return len(table.txState.Entries())
}

// txPanelList returns the focusable primitive of the transaction panel, or nil.
func (table *ResultsTable) txPanelList() tview.Primitive {
	if table.TxPanel == nil {
		return nil
	}
	return table.TxPanel.List
}

// txResultLabel renders the status line for a statement run on a session.
func txResultLabel(kind txKind, affected int64) string {
	switch kind {
	case txKindControl:
		// BEGIN/COMMIT/SAVEPOINT have no meaningful row count, and drivers
		// report 0 for them rather than an unknown count.
		return "Statement executed"
	case txKindRows:
		if affected == 1 {
			return "1 row"
		}
		return fmt.Sprintf("%d rows", affected)
	default:
		if affected < 0 {
			return "Statement executed"
		}
		if affected == 1 {
			return "1 row affected"
		}
		return fmt.Sprintf("%d rows affected", affected)
	}
}

// ---------------------------------------------------------------------------
// Commit / rollback
// ---------------------------------------------------------------------------

// CommitTransaction asks for confirmation and commits the open transaction.
func (table *ResultsTable) CommitTransaction() {
	table.confirmTransactionEnd("commit")
}

// RollbackTransaction asks for confirmation and discards the open transaction.
func (table *ResultsTable) RollbackTransaction() {
	table.confirmTransactionEnd("rollback")
}

// ClearTransactionHistory drops resolved history entries.
func (table *ResultsTable) ClearTransactionHistory() {
	if table.txState == nil {
		return
	}
	if !table.txState.Clear() {
		table.showTransactionInfo("Cannot clear the history while a transaction is active.\n\nCommit or roll back first.")
		return
	}
	table.renderTransactionPanel()
}

func (table *ResultsTable) confirmTransactionEnd(action string) {
	if table.txState == nil || !table.txState.Active() {
		table.txOwner = App.GetFocus()
		table.showTransactionInfo("No active transaction.\n\nRun BEGIN (or START TRANSACTION) in the editor to open one.")
		return
	}

	session := table.txSessionSnapshot()
	if session == nil {
		table.showTransactionInfo("The transaction session is no longer available.\n\nThe transaction was rolled back.")
		table.txState.Resolve(txOutcomeRolledBack)
		table.renderTransactionPanel()
		return
	}

	pending := table.txState.Pending()
	summary := table.txState.SummarizePending()

	// Remember who asked for this so focus returns to the same panel.
	table.txOwner = App.GetFocus()

	modal := NewConfirmationModal(txConfirmationText(action, pending, summary))
	modal.SetDoneFunc(func(_ int, buttonLabel string) {
		mainPages.RemovePage(pageNameTransactionConfirm)
		if buttonLabel != confirmationYes {
			table.focusTransactionOwner()
			return
		}
		table.finishTransaction(action, summary)
	})
	mainPages.AddPage(pageNameTransactionConfirm, modal, true, true)
	App.SetFocus(modal)
}

// finishTransaction executes COMMIT/ROLLBACK on the pinned session.
func (table *ResultsTable) finishTransaction(action string, summary PendingSummary) {
	verb := "COMMIT"
	outcome := txOutcomeCommitted
	if action == "rollback" {
		verb = "ROLLBACK"
		outcome = txOutcomeRolledBack
	}

	session := table.txSessionSnapshot()
	if session == nil {
		table.showTransactionInfo("The transaction session is no longer available.")
		return
	}

	entryID := table.txState.Record(verb+";", verb, false)
	affected, err := session.Exec(app.App.Context(), verb)
	table.txState.Complete(entryID, txKindControl, affected, err)

	if err != nil {
		// Leave the session in place so the user can retry or roll back.
		table.SetQueryStatus(fmt.Sprintf("%s failed: %s", verb, err.Error()))
		table.SetError(fmt.Sprintf("%s failed:\n\n%s", verb, err.Error()), func() {
			table.focusTransactionOwner()
		})
		table.renderTransactionPanel()
		return
	}

	table.txState.Resolve(outcome)
	table.closeTransactionSession()
	table.renderTransactionPanel()

	label := "Committed"
	if action == "rollback" {
		label = "Rolled back"
	}
	status := fmt.Sprintf("%s %d statement(s), %d rows", label, summary.Statements, summary.Rows)
	table.SetResultsInfo(status)
	table.SetQueryStatus(status)
	table.focusTransactionOwner()
}

// focusTransactionOwner returns focus to the panel that asked for the action
// (transaction panel, results grid or editor) instead of always jumping to the
// editor.
func (table *ResultsTable) focusTransactionOwner() {
	owner := table.txOwner
	table.txOwner = nil

	if owner != nil {
		App.SetFocus(owner)
		switch owner {
		case tview.Primitive(table.Editor):
			table.Editor.Highlight()
		case tview.Primitive(table.txPanelList()):
			if table.TxPanel != nil {
				table.TxPanel.Highlight()
			}
		}
		App.ForceDraw()
		return
	}

	if table.TxPanel != nil && table.TxPanel.HasFocus() {
		table.TxPanel.Focus()
		return
	}
	if table.Editor != nil {
		App.SetFocus(table.Editor)
		table.Editor.Highlight()
		return
	}
	App.SetFocus(table)
}

func (table *ResultsTable) showTransactionInfo(text string) {
	modal := tview.NewModal().
		SetText(text).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(_ int, _ string) {
			mainPages.RemovePage(pageNameTransactionInfo)
			table.focusTransactionOwner()
		})
	modal.SetBackgroundColor(app.Styles.PrimitiveBackgroundColor)
	modal.SetTextColor(app.Styles.PrimaryTextColor)

	mainPages.AddPage(pageNameTransactionInfo, modal, true, true)
	App.SetFocus(modal)
}

// focusTransactionPanel moves focus into the transaction panel.
func (table *ResultsTable) focusTransactionPanel() {
	if table.TxPanel == nil {
		return
	}
	if table.txPanelCollapsed {
		table.txPanelCollapsed = false
		table.applyTxPanelWidth()
	}
	table.TxPanel.Render()
	table.TxPanel.Focus()
	App.ForceDraw()
}

// focusEditorFromTransactionPanel leaves the panel and returns to the editor.
func (table *ResultsTable) focusEditorFromTransactionPanel() {
	if table.Editor == nil {
		return
	}
	table.TxPanel.Blur()
	App.SetFocus(table.Editor)
	table.Editor.Highlight()
	App.ForceDraw()
}

// transactionPanelVisible reports whether the panel takes horizontal space.
func (table *ResultsTable) transactionPanelVisible() bool {
	return table.TxPanel != nil && !table.txPanelCollapsed
}

// blurTransactionPanel removes the panel's active border without moving focus.
func (table *ResultsTable) blurTransactionPanel() {
	if table.TxPanel != nil {
		table.TxPanel.Blur()
	}
}

// hasActiveTransaction reports whether an explicit transaction is open in this
// tab's editor.
func (table *ResultsTable) hasActiveTransaction() bool {
	return table.txState != nil && table.txState.Active()
}

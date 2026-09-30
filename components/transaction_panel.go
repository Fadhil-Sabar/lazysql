package components

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/jorgerojas26/lazysql/app"
	"github.com/jorgerojas26/lazysql/commands"
	"github.com/jorgerojas26/lazysql/helpers/logger"
	"github.com/jorgerojas26/lazysql/lib"
)

// TransactionPanel sits beside the editor and shows every statement executed
// from that editor tab together with the state of the explicit transaction
// they belong to. It offers the same commit/rollback actions as the results
// grid, and both go through the shared confirmation dialog.
type TransactionPanel struct {
	Wrapper *tview.Flex
	List    *tview.Table
	footer  *tview.TextView

	state *TransactionState

	onCommit       func()
	onRollback     func()
	onClear        func()
	onUnfocus      func()
	onToggleCommit func()

	// manualCommit reports whether writes wait for an explicit commit.
	manualCommit func() bool

	focused bool
}

// NewTransactionPanel builds the panel rendering state.
func NewTransactionPanel(state *TransactionState) *TransactionPanel {
	if state == nil {
		state = NewTransactionState()
	}

	list := tview.NewTable()
	list.SetSelectable(true, false)
	list.SetBorders(false)
	list.SetWrapSelection(true, false)

	footer := tview.NewTextView()
	footer.SetDynamicColors(true)
	footer.SetWrap(false)

	panel := &TransactionPanel{
		List:   list,
		footer: footer,
		state:  state,
	}

	wrapper := tview.NewFlex().SetDirection(tview.FlexRow)
	wrapper.SetBorder(true)
	wrapper.SetBorderColor(app.Styles.InverseTextColor)
	wrapper.SetTitleColor(app.Styles.PrimaryTextColor)
	wrapper.SetTitle(" [4] Transaction ")
	wrapper.AddItem(list, 0, 1, true)
	wrapper.AddItem(footer, 1, 0, false)
	panel.Wrapper = wrapper

	list.SetInputCapture(panel.inputCapture)
	list.SetFocusFunc(func() {
		panel.focused = true
		panel.applyFocusStyle()
	})
	list.SetBlurFunc(func() {
		panel.focused = false
		panel.applyFocusStyle()
	})

	panel.Render()
	return panel
}

// State exposes the shared transaction state (used by ResultsTable).
func (panel *TransactionPanel) State() *TransactionState {
	return panel.state
}

// SetHandlers wires the panel actions to the owning ResultsTable.
func (panel *TransactionPanel) SetHandlers(onCommit, onRollback, onClear, onUnfocus, onToggleCommit func()) {
	panel.onCommit = onCommit
	panel.onRollback = onRollback
	panel.onClear = onClear
	panel.onUnfocus = onUnfocus
	panel.onToggleCommit = onToggleCommit
}

// SetManualCommitFunc lets the panel show the connection's commit mode.
func (panel *TransactionPanel) SetManualCommitFunc(manualCommit func() bool) {
	panel.manualCommit = manualCommit
}

// manualCommitEnabled reports the mode, defaulting to auto commit.
func (panel *TransactionPanel) manualCommitEnabled() bool {
	return panel.manualCommit != nil && panel.manualCommit()
}

// Focus moves keyboard focus into the history list.
func (panel *TransactionPanel) Focus() {
	app.App.SetFocus(panel.List)
}

// HasFocus reports whether the history list currently owns keyboard focus.
func (panel *TransactionPanel) HasFocus() bool {
	return app.App.GetFocus() == panel.List
}

// Highlight paints the panel border as the active panel.
func (panel *TransactionPanel) Highlight() {
	panel.Wrapper.SetBorderColor(app.Styles.PrimaryTextColor)
	panel.Wrapper.SetTitleColor(app.Styles.PrimaryTextColor)
}

// Blur paints the panel border as inactive.
func (panel *TransactionPanel) Blur() {
	panel.Wrapper.SetBorderColor(app.Styles.InverseTextColor)
	panel.Wrapper.SetTitleColor(app.Styles.PrimaryTextColor)
}

func (panel *TransactionPanel) applyFocusStyle() {
	if panel.focused {
		panel.Highlight()
		return
	}
	panel.Blur()
}

// Render redraws the history from the transaction state.
func (panel *TransactionPanel) Render() {
	entries := panel.state.Entries()
	selected, _ := panel.List.GetSelection()

	panel.List.Clear()

	if len(entries) == 0 {
		panel.List.SetCell(0, 0, tview.NewTableCell(" no statements yet").
			SetTextColor(app.Styles.TertiaryTextColor))
		panel.List.Select(0, 0)
	} else {
		for i, entry := range entries {
			color := txOutcomeColor(entry)
			panel.List.SetCell(i, 0, tview.NewTableCell(fmt.Sprintf("%2d", i+1)).
				SetTextColor(app.Styles.TertiaryTextColor))
			panel.List.SetCell(i, 1, tview.NewTableCell(entry.At.Format("15:04:05")).
				SetTextColor(app.Styles.TertiaryTextColor))
			panel.List.SetCell(i, 2, tview.NewTableCell(txStatementLabel(entry, 200)).
				SetTextColor(color).
				SetExpansion(1))
			panel.List.SetCell(i, 3, tview.NewTableCell(txRowLabel(entry)).
				SetTextColor(color).
				SetAlign(tview.AlignRight))
		}

		switch {
		case panel.focused && selected >= 0 && selected < len(entries):
			panel.List.Select(selected, 0)
		default:
			panel.List.Select(len(entries)-1, 0)
			panel.List.ScrollToEnd()
		}
	}

	panel.updateTitle(entries)
	panel.updateFooter()
}

func (panel *TransactionPanel) updateTitle(entries []txStatement) {
	mode := ""
	if panel.manualCommitEnabled() {
		mode = " · manual"
	}

	title := "[4] Transaction" + mode

	switch {
	case panel.state.Active():
		summary := panel.state.SummarizePending()
		title = fmt.Sprintf("[4] Transaction · ACTIVE · %d pending · %d rows%s", summary.Statements, summary.Rows, mode)
		if panel.state.Failed() {
			title = fmt.Sprintf("[4] Transaction · FAILED · %d pending · rollback required%s", summary.Statements, mode)
		}
	case len(entries) > 0:
		title = fmt.Sprintf("[4] Transaction · %d statement(s)%s", len(entries), mode)
	}

	panel.Wrapper.SetTitle(" " + title + " ")
}

func (panel *TransactionPanel) updateFooter() {
	dim := app.Styles.SecondaryTextColor

	if panel.state.Active() {
		panel.footer.SetText(fmt.Sprintf("[%s] c[-] commit  [%s]r[-] rollback  [%s]m[-] mode  [%s]x[-] clear",
			dim, dim, dim, dim))
		return
	}

	mode := "manual: off"
	if panel.manualCommitEnabled() {
		mode = "manual: ON"
	}

	panel.footer.SetText(fmt.Sprintf("[%s] m[-] %s  [%s]x[-] clear  [%s]y[-] copy",
		dim, mode, dim, dim))
}

func (panel *TransactionPanel) inputCapture(event *tcell.EventKey) *tcell.EventKey {
	command := app.Keymaps.Group(app.TransactionGroup).Resolve(event)

	switch command {
	case commands.CommitTransaction:
		panel.trigger(panel.onCommit)
		return nil
	case commands.RollbackTransaction:
		panel.trigger(panel.onRollback)
		return nil
	case commands.ClearTransactionHistory:
		panel.trigger(panel.onClear)
		return nil
	case commands.ToggleCommitMode:
		panel.trigger(panel.onToggleCommit)
		return nil
	case commands.Copy:
		panel.copySelected()
		return nil
	case commands.Quit:
		panel.trigger(panel.onUnfocus)
		return nil
	case commands.MoveDown:
		panel.moveSelection(1)
		return nil
	case commands.MoveUp:
		panel.moveSelection(-1)
		return nil
	}

	return event
}

func (panel *TransactionPanel) trigger(action func()) {
	if action != nil {
		action()
	}
}

func (panel *TransactionPanel) moveSelection(delta int) {
	count := panel.List.GetRowCount()
	if count == 0 {
		return
	}

	row, _ := panel.List.GetSelection()
	row += delta
	if row < 0 {
		row = 0
	}
	if row >= count {
		row = count - 1
	}
	panel.List.Select(row, 0)
}

// SelectedStatement returns the query of the highlighted history entry.
func (panel *TransactionPanel) SelectedStatement() string {
	row, _ := panel.List.GetSelection()
	entries := panel.state.Entries()
	if row < 0 || row >= len(entries) {
		return ""
	}
	return entries[row].Query
}

func (panel *TransactionPanel) copySelected() {
	query := panel.SelectedStatement()
	if query == "" {
		return
	}
	if err := lib.NewClipboard().Write(query); err != nil {
		logger.Info("Error copying transaction statement", map[string]any{"error": err.Error()})
	}
}

// txOutcomeColor maps a statement outcome to a readable text color.
func txOutcomeColor(entry txStatement) tcell.Color {
	switch {
	case entry.Err != "":
		return app.Styles.ErrorColor
	case entry.Outcome == txOutcomeRunning:
		return app.Styles.PrimaryTextColor
	case entry.Outcome == txOutcomePending:
		return app.Styles.PrimaryTextColor
	case entry.Outcome == txOutcomeRolledBack:
		return app.Styles.TertiaryTextColor
	case entry.Outcome == txOutcomeFailed:
		return app.Styles.ErrorColor
	default:
		return app.Styles.SecondaryTextColor
	}
}

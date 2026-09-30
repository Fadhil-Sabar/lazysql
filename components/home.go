package components

import (
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/jorgerojas26/lazysql/app"
	"github.com/jorgerojas26/lazysql/commands"
	"github.com/jorgerojas26/lazysql/drivers"
	"github.com/jorgerojas26/lazysql/helpers/logger"
	"github.com/jorgerojas26/lazysql/internal/history"
	"github.com/jorgerojas26/lazysql/models"
)

type Home struct {
	*tview.Flex
	Tree                 *Tree
	TabbedPane           *TabbedPane
	LeftWrapper          *tview.Flex
	RightWrapper         *tview.Flex
	MainContent          *tview.Flex
	leftWrapperVisible   bool
	treePinned           bool
	treeWidth            int
	HelpStatus           HelpStatus
	HelpModal            *HelpModal
	QueryHistoryModal    *QueryHistoryModal
	DBDriver             drivers.Driver
	FocusedWrapper       string
	ListOfDBChanges      []models.DBDMLChange
	ConnectionIdentifier string
	ConnectionURL        string
	ReadOnly             bool
	metadataCache        *metadataCache
	metadataCacheMu      sync.Mutex
	schemaLoader         *schemaLoader
	// manualCommit makes writes wait for an explicit commit (c) instead of
	// committing them immediately. It applies to every editor tab of this
	// connection and is toggled from the transaction panel (m) or with Ctrl+B.
	manualCommit bool
	// focusIntent counts explicit "focus this panel" requests. focusTab
	// restores editor focus from a goroutine and must skip that restore when a
	// newer intent (for example the transaction panel) has taken over.
	focusIntent uint64
}

func NewHomePage(connection models.Connection, dbdriver drivers.Driver) *Home {
	metadataCache := newMetadataCache()
	schemaLoader := newSchemaLoader(dbdriver, metadataCache)
	tree := NewTree(connection.DBName, dbdriver, connection.Schemas)
	tree.schemaLoader = schemaLoader
	leftWrapper := tview.NewFlex()
	rightWrapper := tview.NewFlex()

	maincontent := tview.NewFlex()

	connectionIdentifier := connection.Name
	if connectionIdentifier == "" {
		parsedURL, err := url.Parse(connection.URL)
		if err == nil {
			connectionIdentifier = history.SanitizeFilename(parsedURL.Host + strings.ReplaceAll(parsedURL.Path, "/", "_"))
		} else {
			connectionIdentifier = "unnamed_or_invalid_url_connection"
		}
	}

	home := &Home{
		Flex:               tview.NewFlex().SetDirection(tview.FlexRow),
		Tree:               tree,
		LeftWrapper:        leftWrapper,
		RightWrapper:       rightWrapper,
		MainContent:        maincontent,
		leftWrapperVisible: true,
		treePinned:         true,
		treeWidth:          app.App.Config().TreeWidth,
		HelpStatus:         NewHelpStatus(),
		HelpModal:          NewHelpModal(),

		DBDriver:             dbdriver,
		ListOfDBChanges:      []models.DBDMLChange{},
		ConnectionIdentifier: connectionIdentifier,
		ConnectionURL:        connection.URL,
		ReadOnly:             connection.ReadOnly,
		metadataCache:        metadataCache,
		schemaLoader:         schemaLoader,
	}

	tabbedPane := NewTabbedPane()

	home.TabbedPane = tabbedPane

	qhm := NewQueryHistoryModal(connectionIdentifier, func(selectedQuery string) {
		home.createOrFocusEditorTab()

		currentTab := home.TabbedPane.GetCurrentTab()
		if currentTab != nil {
			table := currentTab.Content.(*ResultsTable)
			table.Editor.SetText(selectedQuery, true)
		}
	})

	home.QueryHistoryModal = qhm

	go home.subscribeToTreeChanges()

	leftWrapper.SetBorderColor(app.Styles.InverseTextColor)
	leftWrapper.AddItem(tree.Wrapper, 0, 1, true)

	if connection.ReadOnly {
		leftWrapper.SetTitle(" [READ-ONLY] ")
		leftWrapper.SetTitleColor(app.Styles.ReadOnlyColor)
		leftWrapper.SetBorder(true)
	}

	rightWrapper.SetBorderColor(app.Styles.InverseTextColor)
	rightWrapper.SetBorder(true)
	rightWrapper.SetDirection(tview.FlexColumnCSS)
	rightWrapper.SetInputCapture(home.rightWrapperInputCapture)
	rightWrapper.AddItem(tabbedPane.HeaderContainer, 1, 0, false)
	rightWrapper.AddItem(tabbedPane.Pages, 0, 1, false)

	maincontent.AddItem(leftWrapper, home.treeWidth, 1, false)
	maincontent.AddItem(rightWrapper, 0, 5, false)

	home.AddItem(maincontent, 0, 1, false)
	// home.AddItem(home.HelpStatus, 1, 1, false)

	home.SetInputCapture(home.homeInputCapture)

	home.SetFocusFunc(func() {
		if home.FocusedWrapper == focusedWrapperLeft || home.FocusedWrapper == "" {
			home.focusLeftWrapper()
		} else {
			home.focusRightWrapper()
		}
	})

	mainPages.AddPage(connection.URL, home, true, false)
	return home
}

func (home *Home) subscribeToTreeChanges() {
	ch := home.Tree.Subscribe()

	for stateChange := range ch {
		switch stateChange.Key {
		case eventTreeSelectedTable:
			databaseName := home.Tree.GetSelectedDatabase()
			tableName := stateChange.Value.(string)

			home.showTable(databaseName, tableName)
		case eventTreeIsFiltering:
			isFiltering := stateChange.Value.(bool)
			if isFiltering {
				home.SetInputCapture(nil)
			} else {
				home.SetInputCapture(home.homeInputCapture)
			}
		case eventTreeSelectedFunction:
			home.createOrFocusEditorTab()
			currentTab := home.TabbedPane.GetCurrentTab()
			if currentTab != nil {
				table := currentTab.Content.(*ResultsTable)
				databaseName := home.Tree.GetSelectedDatabase()
				functionName := stateChange.Value.(string)
				functionDefinition, err := home.Tree.DBDriver.GetFunctionDefinition(app.App.Context(), databaseName, functionName)
				if err != nil {
					logger.Error(err.Error(), nil)
					continue
				}
				table.Editor.SetText(functionDefinition, false)
				App.ForceDraw()
			}
		case eventTreeSelectedProcedure:
			home.createOrFocusEditorTab()
			currentTab := home.TabbedPane.GetCurrentTab()
			if currentTab != nil {
				table := currentTab.Content.(*ResultsTable)
				databaseName := home.Tree.GetSelectedDatabase()
				procedureName := stateChange.Value.(string)
				procedureDefinition, err := home.Tree.DBDriver.GetProcedureDefinition(app.App.Context(), databaseName, procedureName)
				if err != nil {
					logger.Error(err.Error(), nil)
					continue
				}
				table.Editor.SetText(procedureDefinition, false)
				App.ForceDraw()
			}
		case eventTreeSelectedView:
			home.createOrFocusEditorTab()
			currentTab := home.TabbedPane.GetCurrentTab()
			if currentTab != nil {
				table := currentTab.Content.(*ResultsTable)
				databaseName := home.Tree.GetSelectedDatabase()
				viewName := stateChange.Value.(string)
				viewDefinition, err := home.Tree.DBDriver.GetViewDefinition(app.App.Context(), databaseName, viewName)
				if err != nil {
					logger.Error(err.Error(), nil)
					continue
				}
				table.Editor.SetText(viewDefinition, false)
				App.ForceDraw()
			}
		}
	}
}

// refreshSchemaAfterDDL invalidates the connection-scoped schema cache and
// starts a tree rebuild without putting catalog work on the SQL completion
// path. The tree's own loader applies tables before programming objects.
func (home *Home) refreshSchemaAfterDDL(database string) {
	if home == nil {
		return
	}

	home.metadataCacheForConnection().invalidateAll()
	if home.Tree != nil {
		home.Tree.RefreshAsync(database)
	}
}

// refreshKnownRecordTables refreshes only Records tabs that are named by a
// committed Records-originated change. Arbitrary editor SQL has no reliable
// table identity and never calls this helper.
func (home *Home) refreshKnownRecordTables(changes []models.DBDMLChange) {
	if home == nil || home.TabbedPane == nil {
		return
	}

	seen := make(map[string]struct{}, len(changes))
	for _, change := range changes {
		if change.Database == "" || change.Table == "" {
			continue
		}
		reference := fmt.Sprintf("%s.%s", change.Database, change.Table)
		if _, ok := seen[reference]; ok {
			continue
		}
		seen[reference] = struct{}{}

		tab := home.TabbedPane.GetTabByReference(reference)
		if tab == nil {
			continue
		}
		if table, ok := tab.Content.(*ResultsTable); ok {
			table.RefreshRecords()
		}
	}
}

func (home *Home) showTable(databaseName, tableName string) {
	if tableName == "" {
		return
	}
	tabReference := fmt.Sprintf("%s.%s", databaseName, tableName)

	tab := home.TabbedPane.GetTabByReference(tabReference)

	var table *ResultsTable

	if tab != nil {
		table = tab.Content.(*ResultsTable)
		home.TabbedPane.SwitchToTabByReference(tab.Reference)
	} else {
		table = NewResultsTable(&home.ListOfDBChanges, home.Tree, home.DBDriver, home, home.ConnectionIdentifier, home.ConnectionURL, home.ReadOnly).WithFilter()
		table.SetDatabaseName(databaseName)
		table.SetTableName(tableName)

		home.TabbedPane.AppendTab(tableName, table, tabReference)
	}

	table.FetchRecords(func() {
		if !quitConfirmationVisible() {
			home.focusLeftWrapper()
		}
		keepQuitConfirmationFocused()
	}, func() {
		if !app.App.Config().DisableSidebar && !table.GetShowSidebar() {
			records := table.GetRecords()
			if len(records) > 1 {
				table.ShowSidebar(true)
			}
		}

		if table.state.error == "" {
			// toggleLeftWrapper moves focus to the table; skip it while the quit
			// dialog is up so a load finishing after 'q' can't steal focus from
			// the dialog and leave it undismissable.
			if !home.treePinned && home.leftWrapperVisible && !quitConfirmationVisible() {
				home.toggleLeftWrapper()
			}
		}

		App.ForceDraw()
		keepQuitConfirmationFocused()
	})

	// showTable runs on the tree-subscription goroutine, so a bare focus call
	// here races with a 'q' pressed while the table is opening. Marshal it onto
	// the UI loop so the "is the quit dialog up?" check is atomic with the
	// dialog being shown; if it is up, leave focus on it so it stays dismissable.
	App.QueueUpdateDraw(func() {
		if !quitConfirmationVisible() {
			home.focusRightWrapper()
		}
		keepQuitConfirmationFocused()
	})
}

// ShowTableWithFilter opens the table filtered by the given WHERE clause. An
// already open tab for the table is reused unless doing so would replace a
// filter the user is looking at: the tab is the current one (self referencing
// foreign keys) or it already carries a different filter. In that case the
// table opens in a tab of its own.
func (home *Home) ShowTableWithFilter(databaseName, tableName, where string) {
	if tableName == "" {
		return
	}

	tabName := tableName
	tabReference := fmt.Sprintf("%s.%s", databaseName, tableName)
	tab := home.TabbedPane.GetTabByReference(tabReference)

	if tab != nil && !canReuseTabForFilter(tab, home.TabbedPane.GetCurrentTab(), where) {
		newReference := home.TabbedPane.NextAvailableReference(tabReference)
		tabName += strings.TrimPrefix(newReference, tabReference)
		tabReference = newReference
		tab = nil
	}

	var table *ResultsTable
	if tab != nil {
		table = tab.Content.(*ResultsTable)
		home.TabbedPane.SwitchToTabByReference(tab.Reference)
	} else {
		table = NewResultsTable(&home.ListOfDBChanges, home.Tree, home.DBDriver, home, home.ConnectionIdentifier, home.ConnectionURL, home.ReadOnly).WithFilter()
		table.SetDatabaseName(databaseName)
		table.SetTableName(tableName)
		home.TabbedPane.AppendTab(tabName, table, tabReference)
	}

	if table.Filter != nil {
		table.Filter.SetCurrentFilterUnsafe(where)
	}

	table.FetchRecords(func() {
		if !quitConfirmationVisible() {
			home.focusLeftWrapper()
		}
		keepQuitConfirmationFocused()
	}, func() {
		if table.state.error != "" {
			App.ForceDraw()
			keepQuitConfirmationFocused()
			return
		}

		if !app.App.Config().DisableSidebar && !table.GetShowSidebar() {
			records := table.GetRecords()
			if len(records) > 1 {
				table.ShowSidebar(true)
			}
		}

		if !home.treePinned && home.leftWrapperVisible && !quitConfirmationVisible() {
			home.toggleLeftWrapper()
		}
		App.ForceDraw()
		keepQuitConfirmationFocused()
	})

	// See showTable: this runs on the tree-subscription goroutine and races
	// with a 'q' pressed while the table is opening.
	if !quitConfirmationVisible() {
		home.focusRightWrapper()
	}
	keepQuitConfirmationFocused()
}

// canReuseTabForFilter reports whether applying where to tab keeps whatever the
// user currently sees intact.
func canReuseTabForFilter(tab, currentTab *Tab, where string) bool {
	if tab == currentTab {
		return false
	}

	table, ok := tab.Content.(*ResultsTable)
	if !ok || table.Filter == nil {
		return true
	}

	currentFilter := strings.TrimSpace(table.Filter.GetCurrentFilter())

	return currentFilter == "" || currentFilter == strings.TrimSpace(where)
}

func (home *Home) focusRightWrapper() {
	home.Tree.RemoveHighlight()

	home.RightWrapper.SetBorderColor(app.Styles.PrimaryTextColor)
	home.LeftWrapper.SetBorderColor(app.Styles.InverseTextColor)
	home.TabbedPane.Highlight()
	tab := home.TabbedPane.GetCurrentTab()

	if tab != nil {
		home.focusTab(tab)
	}

	home.FocusedWrapper = focusedWrapperRight
}

func (home *Home) focusTab(tab *Tab) {
	if tab != nil {
		table := tab.Content.(*ResultsTable)
		table.HighlightAll()
		table.blurTransactionPanel()

		if table.GetIsFiltering() {
			intent := home.focusIntent
			go func() {
				// This runs asynchronously. Skip the restore when a newer
				// focus request (another panel, the transaction panel) has
				// been made in the meantime, so it cannot steal focus.
				if home.focusIntent != intent {
					return
				}

				if table.Filter != nil {
					app.App.SetFocus(table.Filter.Input)
					table.Filter.HighlightLocal()
				} else if table.Editor != nil {
					app.App.SetFocus(table.Editor)
					table.Editor.Highlight()
				}

				table.RemoveHighlightTable()
				app.App.Draw()
			}()
		} else {
			table.SetInputCapture(table.tableInputCapture)
			app.App.SetFocus(table)
		}

		if tab.Name == tabNameEditor {
			home.HelpStatus.SetStatusOnEditorView()
		} else {
			home.HelpStatus.SetStatusOnTableView()
		}
	}
}

func (home *Home) focusLeftWrapper() {
	home.Tree.Highlight()

	home.RightWrapper.SetBorderColor(app.Styles.InverseTextColor)
	home.LeftWrapper.SetBorderColor(app.Styles.PrimaryTextColor)

	tab := home.TabbedPane.GetCurrentTab()

	if tab != nil {
		table := tab.Content.(*ResultsTable)

		table.RemoveHighlightAll()
		table.blurTransactionPanel()

	}

	home.TabbedPane.SetBlur()

	app.App.SetFocus(home.Tree)

	home.FocusedWrapper = focusedWrapperLeft
}

// ---------------------------------------------------------------------------
// Panel navigation (schema | editor / results)
// ---------------------------------------------------------------------------

type panel int

const (
	panelSchema panel = iota
	panelEditor
	panelResults
	panelTransaction
)

// panelForFocus maps the wrapper/editor state to the panel that owns focus.
func panelForFocus(focusedWrapper string, hasTab, editorFocused bool) panel {
	if focusedWrapper == focusedWrapperLeft {
		return panelSchema
	}
	if hasTab && editorFocused {
		return panelEditor
	}
	return panelResults
}

// panelNumberAllowed reports whether a bare digit should focus a panel instead
// of being typed into the focused widget.
func panelNumberAllowed(editorFocused, editorInsert, tableFocused, treeFocused, treeFiltering bool) bool {
	switch {
	case editorFocused:
		return !editorInsert
	case tableFocused:
		return true
	case treeFocused:
		return !treeFiltering
	default:
		return false
	}
}

// currentPanel reports which of the four panels currently owns focus.
func (home *Home) currentPanel() panel {
	if table := home.currentResultsTable(); table != nil && table.TxPanel != nil && table.TxPanel.HasFocus() {
		return panelTransaction
	}

	hasTab := false
	editorFocused := false

	if tab := home.TabbedPane.GetCurrentTab(); tab != nil {
		hasTab = true
		if table, ok := tab.Content.(*ResultsTable); ok {
			editorFocused = table.GetIsFiltering()
		}
	}

	return panelForFocus(home.FocusedWrapper, hasTab, editorFocused)
}

// canUsePanelShortcuts reports whether panel shortcuts (digits, resize,
// collapse) should act instead of being text input.
func (home *Home) canUsePanelShortcuts() bool {
	focus := app.App.GetFocus()
	if focus == nil {
		return false
	}

	if table := home.currentResultsTable(); table != nil && table.TxPanel != nil && table.TxPanel.HasFocus() {
		return true
	}

	editorFocused, editorInsert := false, false
	tableFocused := false

	if tab := home.TabbedPane.GetCurrentTab(); tab != nil {
		if table, ok := tab.Content.(*ResultsTable); ok {
			if table.Editor != nil && focus == table.Editor {
				editorFocused = true
				editorInsert = table.Editor.vimMode == VimModeInsert || table.Editor.searchMode
			}
			if focus == table {
				tableFocused = true
			}
		}
	}

	treeFocused := focus == home.Tree

	return panelNumberAllowed(editorFocused, editorInsert, tableFocused, treeFocused, home.Tree.GetIsFiltering())
}

func (home *Home) focusSchemaPanel() {
	if !home.leftWrapperVisible {
		home.toggleLeftWrapper()
		return
	}

	home.focusIntent++
	home.focusLeftWrapper()
	app.App.ForceDraw()
}

func (home *Home) focusEditorPanel() {
	home.createOrFocusEditorTab()
	if table := home.currentResultsTable(); table != nil {
		table.blurTransactionPanel()
		table.expandEditorPanel()
	}
}

// focusTransactionPanel focuses the transaction history panel beside the
// editor, creating an editor tab first when the connection has none.
func (home *Home) focusTransactionPanel() {
	// ensureEditorTab (not createOrFocusEditorTab) so no asynchronous editor
	// focus restore is scheduled behind us.
	table := home.ensureEditorTab()
	if table == nil || table.TxPanel == nil {
		return
	}

	home.focusIntent++

	home.Tree.RemoveHighlight()
	home.RightWrapper.SetBorderColor(app.Styles.PrimaryTextColor)
	home.LeftWrapper.SetBorderColor(app.Styles.InverseTextColor)
	home.TabbedPane.Highlight()
	table.HighlightAll()
	table.RemoveHighlightTable()
	if table.Editor != nil {
		table.Editor.SetBlur()
	}

	home.FocusedWrapper = focusedWrapperRight
	home.HelpStatus.SetStatusOnEditorView()
	table.focusTransactionPanel()
}

// focusResultsPanel focuses the result grid of the current tab while keeping
// the tab (and, for the editor tab, the editor buffer) exactly where it is.
func (home *Home) focusResultsPanel() {
	if home.TabbedPane.GetLength() == 0 {
		home.focusEditorPanel()
	}

	tab := home.TabbedPane.GetCurrentTab()
	if tab == nil {
		return
	}

	table, ok := tab.Content.(*ResultsTable)
	if !ok {
		return
	}

	home.Tree.RemoveHighlight()
	home.RightWrapper.SetBorderColor(app.Styles.PrimaryTextColor)
	home.LeftWrapper.SetBorderColor(app.Styles.InverseTextColor)
	home.TabbedPane.Highlight()
	table.HighlightAll()
	table.SetIsFiltering(false)
	if table.resultsCollapsed {
		table.resultsCollapsed = false
		table.applyEditorSplit()
	}
	if table.Editor != nil {
		table.Editor.SetBlur()
	}
	table.SetInputCapture(table.tableInputCapture)
	home.focusIntent++
	app.App.SetFocus(table)

	home.FocusedWrapper = focusedWrapperRight
	home.HelpStatus.SetStatusOnTableView()
	app.App.ForceDraw()
}

// focusLastRightPanel returns to the right-hand panel that was in use before
// the schema panel took focus.
func (home *Home) focusLastRightPanel() {
	if tab := home.TabbedPane.GetCurrentTab(); tab != nil {
		if table, ok := tab.Content.(*ResultsTable); ok && table.GetIsFiltering() {
			home.focusEditorPanel()
			return
		}
	}

	home.focusResultsPanel()
}

func (home *Home) panelMoveLeft() {
	switch home.currentPanel() {
	case panelSchema:
		return
	case panelTransaction:
		home.focusEditorPanel()
	default:
		home.focusSchemaPanel()
	}
}

func (home *Home) panelMoveRight() {
	switch home.currentPanel() {
	case panelSchema:
		home.focusLastRightPanel()
	case panelEditor:
		if table := home.currentResultsTable(); table != nil && table.transactionPanelVisible() {
			home.focusTransactionPanel()
		}
	}
}

func (home *Home) panelMoveUp() {
	switch home.currentPanel() {
	case panelResults, panelTransaction:
		home.focusEditorPanel()
	}
}

func (home *Home) panelMoveDown() {
	switch home.currentPanel() {
	case panelEditor, panelTransaction:
		home.focusResultsPanel()
	}
}

// currentResultsTable returns the ResultsTable of the current tab, if any.
func (home *Home) currentResultsTable() *ResultsTable {
	tab := home.TabbedPane.GetCurrentTab()
	if tab == nil {
		return nil
	}
	table, _ := tab.Content.(*ResultsTable)
	return table
}

// ownsFocus reports whether this connection page is the front page. Any modal
// dialog added above it (confirmation, help, error, connection list, ...) takes
// the keyboard for as long as it is visible.
func (home *Home) ownsFocus() bool {
	return homeOwnsFrontPage(mainPages, home)
}

// homeOwnsFrontPage reports whether home is the front page of pages. It is a
// separate function so the dialog-focus rule can be tested without an
// application.
func homeOwnsFrontPage(pages *tview.Pages, home *Home) bool {
	if pages == nil {
		return true
	}

	_, front := pages.GetFrontPage()
	if front == nil {
		return false
	}

	// The page item is the Home when it is opened interactively, and the bare
	// Flex when it is opened from a connection URL argument.
	return front == tview.Primitive(home) || front == tview.Primitive(home.Flex)
}

// panelGrow makes the focused panel larger along its natural axis.
func (home *Home) panelGrow() {
	switch home.currentPanel() {
	case panelSchema:
		if home.leftWrapperVisible {
			home.adjustTreeWidth(panelResizeStep)
		}
	case panelEditor:
		if table := home.currentResultsTable(); table != nil {
			table.resizeEditorPanel(panelResizeStep)
		}
	case panelResults:
		if table := home.currentResultsTable(); table != nil {
			table.resizeEditorPanel(-panelResizeStep)
		}
	case panelTransaction:
		if table := home.currentResultsTable(); table != nil {
			table.resizeTxPanel(panelResizeStep)
		}
	}
}

// panelShrink makes the focused panel smaller along its natural axis.
func (home *Home) panelShrink() {
	switch home.currentPanel() {
	case panelSchema:
		if home.leftWrapperVisible {
			home.adjustTreeWidth(-panelResizeStep)
		}
	case panelEditor:
		if table := home.currentResultsTable(); table != nil {
			table.resizeEditorPanel(-panelResizeStep)
		}
	case panelResults:
		if table := home.currentResultsTable(); table != nil {
			table.resizeEditorPanel(panelResizeStep)
		}
	case panelTransaction:
		if table := home.currentResultsTable(); table != nil {
			table.resizeTxPanel(-panelResizeStep)
		}
	}
}

// panelToggleCollapse collapses or restores the focused panel.
func (home *Home) panelToggleCollapse() {
	switch home.currentPanel() {
	case panelSchema:
		home.toggleLeftWrapper()
	case panelEditor:
		if table := home.currentResultsTable(); table != nil {
			table.toggleEditorPanel()
		}
	case panelResults:
		if table := home.currentResultsTable(); table != nil {
			table.toggleResultsPanel()
		}
	case panelTransaction:
		if table := home.currentResultsTable(); table != nil {
			table.toggleTxPanel()
		}
	}
}

func (home *Home) isCurrentTabFiltering() bool {
	tab := home.TabbedPane.GetCurrentTab()

	if tab != nil {
		table := tab.Content.(*ResultsTable)
		return table.GetIsFiltering()
	}

	return false
}

func (home *Home) rightWrapperInputCapture(event *tcell.EventKey) *tcell.EventKey {
	var tab *Tab

	// The transaction panel owns the keyboard while it is focused: its own
	// group handles commit/rollback/navigation, and the tab shortcuts must not
	// fire underneath it (X would close the tab and silently roll back).
	if table := home.currentResultsTable(); table != nil && table.TxPanel != nil && table.TxPanel.HasFocus() {
		return event
	}

	command := app.Keymaps.Group(app.TableGroup).Resolve(event)

	switch command {
	case commands.TabPrev:

		tab := home.TabbedPane.GetCurrentTab()

		if tab != nil {
			table := tab.Content.(*ResultsTable)
			if tab.Name == tabNameEditor {
				if table.Editor != nil && table.Editor.vimMode == VimModeInsert {
					return event
				}
				home.TabbedPane.SwitchToPreviousTab()
				return nil
			}
			if !table.GetIsEditing() && !table.GetIsFiltering() {
				home.TabbedPane.SwitchToPreviousTab()
				// home.focusTab(home.TabbedPane.SwitchToPreviousTab())
				return nil
			}

		}

		return event
	case commands.TabNext:
		tab := home.TabbedPane.GetCurrentTab()

		if tab != nil {
			table := tab.Content.(*ResultsTable)
			if tab.Name == tabNameEditor {
				if table.Editor != nil && table.Editor.vimMode == VimModeInsert {
					return event
				}
				home.TabbedPane.SwitchToNextTab()
				return nil
			}
			if !table.GetIsEditing() && !table.GetIsFiltering() {
				home.TabbedPane.SwitchToNextTab()
				// home.focusTab(home.TabbedPane.SwitchToNextTab())
				return nil
			}
		}

		return event
	case commands.TabFirst:
		if home.isCurrentTabFiltering() {
			return event
		}

		home.TabbedPane.SwitchToFirstTab()
		// home.focusTab(home.TabbedPane.SwitchToFirstTab())
		return nil
	case commands.TabLast:
		if home.isCurrentTabFiltering() {
			return event
		}

		home.TabbedPane.SwitchToLastTab()
		// home.focusTab(home.TabbedPane.SwitchToLastTab())
		return nil
	case commands.TabClose:
		tab = home.TabbedPane.GetCurrentTab()

		if tab != nil {
			table := tab.Content.(*ResultsTable)

			if !table.GetIsFiltering() && !table.GetIsEditing() {
				home.TabbedPane.RemoveCurrentTab()

				if home.TabbedPane.GetLength() == 0 {
					home.focusLeftWrapper()
					return nil
				}
			}
		}
	case commands.PagePrev:
		if home.isCurrentTabFiltering() {
			return event
		}

		tab = home.TabbedPane.GetCurrentTab()

		if tab != nil {
			table := tab.Content.(*ResultsTable)

			if ((table.Menu != nil && table.Menu.GetSelectedOption() == 1) ||
				table.Menu == nil) && !table.Pagination.GetIsFirstPage() && !table.GetIsLoading() {
				table.Pagination.SetOffset(table.Pagination.GetOffset() - table.Pagination.GetLimit())
				App.ForceDraw()
				table.FetchRecords(nil, nil)
			}
		}

	case commands.PageNext:
		if home.isCurrentTabFiltering() {
			return event
		}

		tab = home.TabbedPane.GetCurrentTab()

		if tab != nil {
			table := tab.Content.(*ResultsTable)

			if ((table.Menu != nil && table.Menu.GetSelectedOption() == 1) ||
				table.Menu == nil) && !table.Pagination.GetIsLastPage() && !table.GetIsLoading() {
				table.Pagination.SetOffset(table.Pagination.GetOffset() + table.Pagination.GetLimit())
				App.ForceDraw()
				table.FetchRecords(nil, nil)
			}
		}
	}

	return event
}

func (home *Home) homeInputCapture(event *tcell.EventKey) *tcell.EventKey {
	// While a dialog (confirmation, help, error, ...) is above this connection
	// page it owns the keyboard. tview can still route a key here through a
	// stale focus flag, and acting on it would steal focus from the dialog and
	// leave it undismissable.
	if !home.ownsFocus() {
		return event
	}
	if t := home.currentResultsTable(); t != nil {
	}
	if t := home.currentResultsTable(); t != nil {
	}

	tab := home.TabbedPane.GetCurrentTab()

	var table *ResultsTable

	if tab != nil {
		table = tab.Content.(*ResultsTable)
	}

	command := app.Keymaps.Group(app.HomeGroup).Resolve(event)

	switch command {
	case commands.MoveLeft:
		if table != nil && !table.GetIsEditing() && !table.GetIsFiltering() && home.FocusedWrapper == focusedWrapperRight {
			if !home.leftWrapperVisible {
				home.toggleLeftWrapper()
			}

			home.focusLeftWrapper()
		}
	case commands.MoveRight:
		if table != nil && !table.GetIsEditing() && !table.GetIsFiltering() && home.FocusedWrapper == focusedWrapperLeft {
			if home.leftWrapperVisible && !home.treePinned {
				home.toggleLeftWrapper()
			}

			home.focusRightWrapper()
		}
	case commands.PanelLeft:
		home.panelMoveLeft()
		return nil
	case commands.PanelRight:
		home.panelMoveRight()
		return nil
	case commands.PanelUp:
		home.panelMoveUp()
		return nil
	case commands.PanelDown:
		home.panelMoveDown()
		return nil
	case commands.FocusSchemaPanel:
		if home.canUsePanelShortcuts() {
			home.panelMoveLeft()
			return nil
		}
		return event
	case commands.FocusEditorPanel:
		if home.canUsePanelShortcuts() {
			home.focusEditorPanel()
			return nil
		}
		return event
	case commands.FocusResultsPanel:
		if home.canUsePanelShortcuts() {
			home.focusResultsPanel()
			return nil
		}
		return event
	case commands.FocusTransactionPanel:
		if home.canUsePanelShortcuts() {
			home.focusTransactionPanel()
			return nil
		}
		return event
	case commands.ToggleCommitMode:
		if table != nil {
			table.ToggleCommitMode()
			return nil
		}
		return event
	case commands.GrowPanel:
		if home.canUsePanelShortcuts() {
			home.panelGrow()
			return nil
		}
		return event
	case commands.ShrinkPanel:
		if home.canUsePanelShortcuts() {
			home.panelShrink()
			return nil
		}
		return event
	case commands.TogglePanel:
		if home.canUsePanelShortcuts() {
			home.panelToggleCollapse()
			return nil
		}
		return event
	case commands.SwitchToEditorView:
		// Ctrl+E opens the editor; when the editor already has focus it runs the
		// statement under the cursor instead (DBeaver-style).
		if event.Key() == tcell.KeyCtrlE && home.currentPanel() == panelEditor {
			return event
		}
		home.createOrFocusEditorTab()
		return nil
	case commands.SwitchToConnectionsView:
		if (table != nil && !table.GetIsEditing() && !table.GetIsFiltering()) || table == nil {
			mainPages.SwitchToPage(pageNameConnections)
		}
	case commands.Quit:
		// Inside the transaction panel q means "leave the panel".
		if table != nil && table.TxPanel != nil && table.TxPanel.HasFocus() {
			return event
		}
		if tab == nil || (!table.GetIsEditing() && !table.GetIsFiltering()) {
			showQuitConfirmation()
			return nil
		}
	case commands.Save:
		if home.ReadOnly {
			errorModal := tview.NewModal().
				SetText("Cannot save changes: Connection is in read-only mode").
				AddButtons([]string{"OK"}).
				SetDoneFunc(func(_ int, _ string) {
					mainPages.RemovePage(pageNameReadOnlyError)
				})
			mainPages.AddPage(pageNameReadOnlyError, errorModal, true, true)
			return event
		}
		if (len(home.ListOfDBChanges) > 0) && (table == nil || !table.GetIsEditing()) {
			queryPreviewModal := NewQueryPreviewModal(&home.ListOfDBChanges, home.DBDriver, func() {
				changes := append([]models.DBDMLChange(nil), home.ListOfDBChanges...)
				for _, change := range changes {
					queryString, err := home.DBDriver.DMLChangeToQueryString(change)
					if err != nil {
						logger.Error("Failed to convert DML change to query string", map[string]any{"error": err})
						continue
					}
					err = history.AddQueryToHistory(home.ConnectionIdentifier, queryString)
					if err != nil {
						logger.Error("Failed to add query to history", map[string]any{"error": err})
					}
				}
				home.ListOfDBChanges = []models.DBDMLChange{}
				home.refreshKnownRecordTables(changes)
				home.Tree.ForceRemoveHighlight()
			})

			// In an editor tab with manual commit (or a transaction already
			// open), queued changes must join that transaction instead of
			// committing on their own. Tabs without an editor have no session
			// and no panel to commit them, so they keep the old behaviour.
			if table != nil && table.Editor != nil && (table.hasActiveTransaction() || table.manualCommitEnabled()) {
				queryPreviewModal.SetExecutor(table.ApplyPendingChangesOnSession)
			}

			mainPages.AddPage(pageNameDMLPreview, queryPreviewModal, true, true)
		}
	case commands.HelpPopup:
		if table != nil && (table.GetIsEditing() || table.GetIsFiltering()) {
			return event
		}

		mainPages.AddPage(pageNameHelp, home.HelpModal, true, true)
	case commands.SearchGlobal:
		// Don't intercept Ctrl+P when the SQL editor is in insert mode
		if table != nil && table.Editor != nil && app.App.GetFocus() == table.Editor && table.Editor.vimMode == VimModeInsert {
			return event
		}

		if !home.leftWrapperVisible {
			home.toggleLeftWrapper()
		}

		if table != nil && !table.GetIsEditing() && !table.GetIsFiltering() && home.FocusedWrapper == focusedWrapperRight {
			home.focusLeftWrapper()
		}

		home.Tree.ForceRemoveHighlight()
		home.Tree.ClearSearch()
		app.App.SetFocus(home.Tree.Filter)
		home.Tree.SetIsFiltering(true)
	case commands.ToggleQueryHistory:
		if mainPages.HasPage(pageNameQueryHistory) {
			mainPages.SwitchToPage(pageNameQueryHistory)
		} else {
			mainPages.AddPage(pageNameQueryHistory, home.QueryHistoryModal, true, true)
		}

		home.QueryHistoryModal.queryHistoryComponent.LoadHistory(home.ConnectionIdentifier)
		return nil
	case commands.ToggleTree:
		if table != nil && !table.GetIsEditing() && !table.GetIsFiltering() {
			home.toggleLeftWrapper()
			home.treePinned = home.leftWrapperVisible
			return nil
		}

		return event
	case commands.WidenTree, commands.NarrowTree:
		if home.leftWrapperVisible && table != nil && !table.GetIsEditing() && !table.GetIsFiltering() {
			delta := treeResizeStep
			if command == commands.NarrowTree {
				delta = -treeResizeStep
			}
			home.adjustTreeWidth(delta)
			return nil
		}

		return event
	}

	return event
}

// ensureEditorTab makes the editor tab the current tab (creating it when the
// connection has none) and returns it. It deliberately does not move keyboard
// focus: callers pick the panel, which keeps this safe to use from
// focusTransactionPanel without racing an asynchronous editor focus restore.
func (home *Home) ensureEditorTab() *ResultsTable {
	tab := home.TabbedPane.GetTabByName(tabNameEditor)
	dbName := home.Tree.GetSelectedDatabase()

	// Fallback: extract database name from connection URL
	if dbName == "" && home.ConnectionURL != "" {
		dbName = extractDatabaseName(home.ConnectionURL)
	}

	if tab != nil {
		home.TabbedPane.SwitchToTabByName(tabNameEditor)
		table := tab.Content.(*ResultsTable)
		table.SetIsFiltering(true)
		// Refresh schema from current database context
		if dbName != "" {
			table.SetDatabaseName(dbName)
			go table.loadEditorSchema()
		}
		return table
	}

	tableWithEditor := NewResultsTable(&home.ListOfDBChanges, home.Tree, home.DBDriver, home, home.ConnectionIdentifier, home.ConnectionURL, home.ReadOnly)
	// Set database name before WithEditor so the table is ready for schema loading
	if dbName != "" {
		tableWithEditor.SetDatabaseName(dbName)
	}
	tableWithEditor = tableWithEditor.WithEditor()

	// Kick off schema loading for autocomplete (async, non-blocking)
	if dbName != "" && tableWithEditor.DBDriver != nil {
		go tableWithEditor.loadEditorSchema()
	}

	home.TabbedPane.AppendTab(tabNameEditor, tableWithEditor, tabNameEditor)
	tableWithEditor.SetIsFiltering(true)
	home.TabbedPane.GetCurrentTab()
	return tableWithEditor
}

func (home *Home) createOrFocusEditorTab() {
	home.ensureEditorTab()
	home.HelpStatus.SetStatusOnEditorView()
	home.focusRightWrapper()
	App.ForceDraw()
}

// extractDatabaseName pulls the database name from a connection URL like
// "mysql://user:pass@host:3306/mydb" → "mydb". Returns "" if none is found.
func extractDatabaseName(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	name := strings.TrimPrefix(u.Path, "/")
	// Remove any trailing path segments after the db name
	if idx := strings.IndexAny(name, "/?"); idx >= 0 {
		name = name[:idx]
	}
	return name
}

func (home *Home) toggleLeftWrapper() {
	if home.leftWrapperVisible {
		home.MainContent.Clear()
		home.MainContent.AddItem(home.RightWrapper, 0, 5, false)
		home.leftWrapperVisible = false
		home.focusRightWrapper()
	} else {
		home.MainContent.Clear()
		home.MainContent.AddItem(home.LeftWrapper, home.treeWidth, 1, false)
		home.MainContent.AddItem(home.RightWrapper, 0, 5, false)
		home.leftWrapperVisible = true
		home.focusLeftWrapper()
	}
	app.App.ForceDraw()
}

const (
	minTreeWidth   = 24
	treeResizeStep = 2
)

// clampTreeWidth keeps the tree at least minTreeWidth wide (when it fits) and
// at most half the container.
func clampTreeWidth(width, containerWidth int) int {
	width = max(width, minTreeWidth)
	if containerWidth <= 0 {
		return width
	}
	return min(width, max(containerWidth/2, minTreeWidth), containerWidth)
}

func (home *Home) adjustTreeWidth(delta int) {
	_, _, containerWidth, _ := home.MainContent.GetInnerRect()
	home.treeWidth = clampTreeWidth(home.treeWidth+delta, containerWidth)
	home.MainContent.ResizeItem(home.LeftWrapper, home.treeWidth, 1)
	app.App.ForceDraw()
}

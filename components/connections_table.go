package components

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/jorgerojas26/lazysql/app"
	"github.com/jorgerojas26/lazysql/drivers"
	"github.com/jorgerojas26/lazysql/models"
)

type ConnectionsTable struct {
	*tview.Table
	Wrapper       *tview.Flex
	errorTextView *tview.TextView
	error         string

	all           []models.Connection
	displayed     []models.Connection
	searchQuery   string
	sortAscending bool
}

var connectionsTable *ConnectionsTable

const connectionsHeaderRow = 0

func NewConnectionsTable() *ConnectionsTable {
	wrapper := tview.NewFlex()

	errorTextView := tview.NewTextView()
	errorTextView.SetTextStyle(tcell.StyleDefault.Foreground(app.Styles.ErrorColor).Background(app.Styles.PrimitiveBackgroundColor))

	table := &ConnectionsTable{
		Table:         tview.NewTable().SetSelectable(true, false),
		Wrapper:       wrapper,
		errorTextView: errorTextView,
		sortAscending: true,
	}

	table.SetSelectedStyle(tcell.StyleDefault.Foreground(app.Styles.SecondaryTextColor).Background(tview.Styles.PrimitiveBackgroundColor))

	wrapper.AddItem(table, 0, 1, true)
	table.SetConnections(app.App.Connections())

	connectionsTable = table

	return connectionsTable
}

// providerLabel returns the human readable database type shown in the Type
// column. It falls back to the URL scheme when Provider is not set.
func providerLabel(connection models.Connection) string {
	provider := connection.Provider
	if provider == "" {
		if idx := strings.Index(connection.URL, "://"); idx > 0 {
			provider = strings.ToLower(connection.URL[:idx])
		}
	}

	switch provider {
	case drivers.DriverMySQL:
		return "MySQL"
	case drivers.DriverPostgres:
		return "PostgreSQL"
	case drivers.DriverSqlite:
		return "SQLite"
	case drivers.DriverMSSQL:
		return "SQL Server"
	case drivers.DriverClickHouse:
		return "ClickHouse"
	case drivers.DriverOracle:
		return "Oracle"
	}

	if provider == "" {
		return "-"
	}
	return provider
}

func (ct *ConnectionsTable) GetConnections() []models.Connection {
	return ct.displayed
}

// GetAllConnections returns the full, unfiltered list.
func (ct *ConnectionsTable) GetAllConnections() []models.Connection {
	return ct.all
}

// SelectedConnection returns the highlighted connection (if any) together with
// its index in the displayed list.
func (ct *ConnectionsTable) SelectedConnection() (models.Connection, int, bool) {
	row, _ := ct.GetSelection()
	index := row - 1 - connectionsHeaderRow
	if index < 0 || index >= len(ct.displayed) {
		return models.Connection{}, -1, false
	}
	return ct.displayed[index], index, true
}

func (ct *ConnectionsTable) GetError() string {
	return ct.error
}

func (ct *ConnectionsTable) SetConnections(connections []models.Connection) {
	ct.all = append([]models.Connection(nil), connections...)
	ct.sortAll()
	ct.render()
}

// AddConnection inserts a connection and refreshes the table.
func (ct *ConnectionsTable) AddConnection(connection models.Connection) {
	ct.all = append(ct.all, connection)
	ct.sortAll()
	ct.render()
}

// SetSearch filters the displayed connections by query (name, type or URL).
func (ct *ConnectionsTable) SetSearch(query string) {
	ct.searchQuery = query
	ct.render()
}

// SearchQuery returns the active filter.
func (ct *ConnectionsTable) SearchQuery() string {
	return ct.searchQuery
}

// ToggleSort flips the name sort between ascending and descending.
func (ct *ConnectionsTable) ToggleSort() {
	ct.sortAscending = !ct.sortAscending
	selected, _, ok := ct.SelectedConnection()
	ct.sortAll()
	ct.render()
	if ok {
		ct.selectConnection(selected)
	}
}

func (ct *ConnectionsTable) sortAll() {
	slices.SortStableFunc(ct.all, func(a, b models.Connection) int {
		cmp := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		if !ct.sortAscending {
			cmp = -cmp
		}
		return cmp
	})
}

func (ct *ConnectionsTable) matches(connection models.Connection) bool {
	query := strings.ToLower(strings.TrimSpace(ct.searchQuery))
	if query == "" {
		return true
	}

	haystack := strings.ToLower(strings.Join([]string{
		connection.Name,
		connection.Provider,
		providerLabel(connection),
		connection.URL,
	}, " "))

	return strings.Contains(haystack, query)
}

func (ct *ConnectionsTable) render() {
	ct.Clear()
	ct.displayed = ct.displayed[:0]

	headerColor := app.Styles.SecondaryTextColor
	sortArrow := "▲"
	if !ct.sortAscending {
		sortArrow = "▼"
	}

	ct.SetCell(connectionsHeaderRow, 0, tview.NewTableCell("Name "+sortArrow).
		SetTextColor(headerColor).
		SetSelectable(false).
		SetExpansion(1))
	ct.SetCell(connectionsHeaderRow, 1, tview.NewTableCell("Type").
		SetTextColor(headerColor).
		SetSelectable(false))
	ct.SetFixed(1, 0)

	row := connectionsHeaderRow + 1
	for _, connection := range ct.all {
		if !ct.matches(connection) {
			continue
		}

		displayName := connection.Name
		if connection.ReadOnly {
			displayName = fmt.Sprintf("[%s]READ[-] ", app.Styles.ReadOnlyColor) + connection.Name
		}

		ct.SetCell(row, 0, tview.NewTableCell(displayName).SetExpansion(1))
		ct.SetCell(row, 1, tview.NewTableCell(providerLabel(connection)).SetTextColor(app.Styles.TertiaryTextColor))
		ct.displayed = append(ct.displayed, connection)
		row++
	}

	if len(ct.displayed) == 0 {
		ct.SetCell(1, 0, tview.NewTableCell("No matching connections").
			SetTextColor(app.Styles.TertiaryTextColor).
			SetSelectable(false))
		ct.SetCell(1, 1, tview.NewTableCell("").SetSelectable(false))
		return
	}

	ct.Select(1, 0)
}

func (ct *ConnectionsTable) selectConnection(connection models.Connection) {
	for i, candidate := range ct.displayed {
		if candidate.Name == connection.Name && candidate.URL == connection.URL {
			ct.Select(i+1, 0)
			return
		}
	}
	ct.Select(1, 0)
}

func (ct *ConnectionsTable) SetError(err error) {
	ct.error = err.Error()
	ct.errorTextView.SetText(ct.error)
}

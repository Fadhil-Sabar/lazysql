package components

import (
	"testing"

	"github.com/rivo/tview"

	"github.com/jorgerojas26/lazysql/models"
)

func newTestConnectionsTable(connections ...models.Connection) *ConnectionsTable {
	ct := &ConnectionsTable{
		Table:         tview.NewTable().SetSelectable(true, false),
		Wrapper:       tview.NewFlex(),
		sortAscending: true,
	}
	ct.SetConnections(connections)
	return ct
}

func displayedNames(ct *ConnectionsTable) []string {
	names := make([]string, 0, len(ct.displayed))
	for _, c := range ct.displayed {
		names = append(names, c.Name)
	}
	return names
}

func TestConnectionsTableSortedByNameAscending(t *testing.T) {
	ct := newTestConnectionsTable(
		models.Connection{Name: "zeta", Provider: "mysql"},
		models.Connection{Name: "Alpha", Provider: "oracle"},
		models.Connection{Name: "beta", Provider: "postgres"},
	)

	got := displayedNames(ct)
	want := []string{"Alpha", "beta", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestConnectionsTableToggleSort(t *testing.T) {
	ct := newTestConnectionsTable(
		models.Connection{Name: "alpha"},
		models.Connection{Name: "beta"},
		models.Connection{Name: "gamma"},
	)

	ct.Select(1, 0)
	ct.ToggleSort()

	if want := []string{"gamma", "beta", "alpha"}; displayedNames(ct)[0] != want[0] {
		t.Fatalf("after toggle got %v, want %v", displayedNames(ct), want)
	}

	// Selection should follow the same connection.
	selected, _, ok := ct.SelectedConnection()
	if !ok || selected.Name != "alpha" {
		t.Fatalf("selection did not follow the connection: %+v ok=%v", selected, ok)
	}
}

func TestConnectionsTableSearchFiltersNameAndType(t *testing.T) {
	ct := newTestConnectionsTable(
		models.Connection{Name: "prod-db", Provider: "oracle", URL: "oracle://x"},
		models.Connection{Name: "dev-db", Provider: "postgres", URL: "postgres://y"},
		models.Connection{Name: "analytics", Provider: "mysql", URL: "mysql://z"},
	)

	ct.SetSearch("prod")
	if got := displayedNames(ct); len(got) != 1 || got[0] != "prod-db" {
		t.Fatalf("name search got %v", got)
	}

	ct.SetSearch("oracle")
	if got := displayedNames(ct); len(got) != 1 || got[0] != "prod-db" {
		t.Fatalf("type search got %v", got)
	}

	ct.SetSearch("")
	if got := displayedNames(ct); len(got) != 3 {
		t.Fatalf("cleared search got %v", got)
	}
}

func TestConnectionsTableSearchNoMatches(t *testing.T) {
	ct := newTestConnectionsTable(models.Connection{Name: "alpha", Provider: "mysql"})
	ct.SetSearch("does-not-exist")

	if len(ct.displayed) != 0 {
		t.Fatalf("expected no matches, got %v", displayedNames(ct))
	}
	if _, _, ok := ct.SelectedConnection(); ok {
		t.Fatal("SelectedConnection should report no selection when empty")
	}
}

func TestConnectionsTableSelectedConnectionMapping(t *testing.T) {
	ct := newTestConnectionsTable(
		models.Connection{Name: "alpha", URL: "mysql://a"},
		models.Connection{Name: "beta", URL: "mysql://b"},
	)

	selected, index, ok := ct.SelectedConnection()
	if !ok || index != 0 || selected.Name != "alpha" {
		t.Fatalf("initial selection = %+v index=%d ok=%v", selected, index, ok)
	}

	ct.Select(2, 0)
	selected, index, ok = ct.SelectedConnection()
	if !ok || index != 1 || selected.Name != "beta" {
		t.Fatalf("second selection = %+v index=%d ok=%v", selected, index, ok)
	}
}

func TestProviderLabel(t *testing.T) {
	cases := map[string]string{
		"mysql":      "MySQL",
		"postgres":   "PostgreSQL",
		"oracle":     "Oracle",
		"sqlite3":    "SQLite",
		"sqlserver":  "SQL Server",
		"clickhouse": "ClickHouse",
	}
	for provider, want := range cases {
		if got := providerLabel(models.Connection{Provider: provider}); got != want {
			t.Errorf("providerLabel(%q) = %q, want %q", provider, got, want)
		}
	}

	// Falls back to the URL scheme.
	if got := providerLabel(models.Connection{URL: "postgres://u@h/db"}); got != "PostgreSQL" {
		t.Errorf("URL fallback = %q, want PostgreSQL", got)
	}
	if got := providerLabel(models.Connection{}); got != "-" {
		t.Errorf("empty provider = %q, want -", got)
	}
}

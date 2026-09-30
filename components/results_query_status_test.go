package components

import (
	"strings"
	"testing"

	"github.com/jorgerojas26/lazysql/drivers"
	"github.com/jorgerojas26/lazysql/models"
)

func TestResultsTableQueryStatusShownInPagination(t *testing.T) {
	changes := []models.DBDMLChange{}
	tree := NewTree("db", &drivers.Postgres{}, nil)
	table := NewResultsTable(&changes, tree, &drivers.Postgres{}, nil, "id", "postgres://example", false).WithEditor()

	table.SetQueryStatus("3 rows affected")

	if got := table.Pagination.GetResultStatus(); got != "3 rows affected" {
		t.Fatalf("pagination status = %q, want %q", got, "3 rows affected")
	}
	if text := table.Pagination.GetText(); !strings.Contains(text, "3 rows affected") {
		t.Fatalf("pagination text %q does not contain the status", text)
	}
}

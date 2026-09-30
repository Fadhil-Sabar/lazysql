package components

import (
	"testing"

	"github.com/jorgerojas26/lazysql/drivers"
	"github.com/jorgerojas26/lazysql/models"
)

func TestPanelForFocus(t *testing.T) {
	cases := []struct {
		name           string
		focusedWrapper string
		hasTab         bool
		editorFocused  bool
		want           panel
	}{
		{"schema focused", focusedWrapperLeft, true, true, panelSchema},
		{"schema wins over stale editor flag", focusedWrapperLeft, true, false, panelSchema},
		{"editor tab in editor mode", focusedWrapperRight, true, true, panelEditor},
		{"editor tab on results", focusedWrapperRight, true, false, panelResults},
		{"table tab (no editor)", focusedWrapperRight, true, false, panelResults},
		{"no tab defaults to results", focusedWrapperRight, false, false, panelResults},
		{"empty wrapper with tab", "", true, false, panelResults},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := panelForFocus(tc.focusedWrapper, tc.hasTab, tc.editorFocused); got != tc.want {
				t.Errorf("panelForFocus(%q, %v, %v) = %v, want %v",
					tc.focusedWrapper, tc.hasTab, tc.editorFocused, got, tc.want)
			}
		})
	}
}

func TestPanelNumberAllowed(t *testing.T) {
	cases := []struct {
		name                                     string
		editorFocused, editorInsert              bool
		tableFocused, treeFocused, treeFiltering bool
		want                                     bool
	}{
		{"editor normal mode", true, false, false, false, false, true},
		{"editor insert mode must type digits", true, true, false, false, false, false},
		{"results grid", false, false, true, false, false, true},
		{"tree", false, false, false, true, false, true},
		{"tree filter is typing", false, false, false, true, true, false},
		{"unrelated widget (sidebar, form)", false, false, false, false, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := panelNumberAllowed(tc.editorFocused, tc.editorInsert, tc.tableFocused, tc.treeFocused, tc.treeFiltering)
			if got != tc.want {
				t.Errorf("panelNumberAllowed(...) = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNextEditorHeight(t *testing.T) {
	cases := []struct {
		name    string
		current int
		delta   int
		total   int
		want    int
	}{
		{"grows", 12, 2, 40, 14},
		{"shrinks", 12, -2, 40, 10},
		{"floors at min", 4, -10, 40, minPanelHeight},
		{"caps so results keep space", 36, 10, 40, 40 - minPanelHeight},
		{"tiny terminal still usable", 12, 5, 4, minPanelHeight},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextEditorHeight(tc.current, tc.delta, tc.total); got != tc.want {
				t.Errorf("nextEditorHeight(%d, %d, %d) = %d, want %d",
					tc.current, tc.delta, tc.total, got, tc.want)
			}
		})
	}
}

func TestResultsPanelResizeAndCollapse(t *testing.T) {
	changes := []models.DBDMLChange{}
	tree := NewTree("db", &drivers.Postgres{}, nil)
	table := NewResultsTable(&changes, tree, &drivers.Postgres{}, nil, "id", "postgres://example", false).WithEditor()

	if table.Editor == nil || table.EditorPages == nil {
		t.Fatal("WithEditor did not build the editor split")
	}
	if table.editorHeight != defaultEditorHeight {
		t.Fatalf("editorHeight = %d, want %d", table.editorHeight, defaultEditorHeight)
	}

	// Without a layout the wrapper reports zero size, so resizing clamps.
	table.resizeEditorPanel(panelResizeStep)
	if table.editorHeight < minPanelHeight {
		t.Fatalf("editorHeight = %d, want >= %d", table.editorHeight, minPanelHeight)
	}

	table.toggleEditorPanel()
	if !table.editorCollapsed {
		t.Fatal("editor should be collapsed")
	}
	table.toggleEditorPanel()
	if table.editorCollapsed {
		t.Fatal("editor should be expanded again")
	}

	table.toggleResultsPanel()
	if !table.resultsCollapsed {
		t.Fatal("results should be collapsed")
	}
	if table.editorCollapsed {
		t.Fatal("collapsing results must expand the editor")
	}
	table.toggleResultsPanel()
	if table.resultsCollapsed {
		t.Fatal("results should be expanded again")
	}

	// Growing while collapsed re-expands.
	table.toggleEditorPanel()
	table.resizeEditorPanel(panelResizeStep)
	if table.editorCollapsed || table.resultsCollapsed {
		t.Fatal("resizing should re-expand both panes")
	}
}

func TestResultsPanelResizeNoopWithoutEditor(t *testing.T) {
	changes := []models.DBDMLChange{}
	tree := NewTree("db", &drivers.Postgres{}, nil)
	table := NewResultsTable(&changes, tree, &drivers.Postgres{}, nil, "id", "postgres://example", false)

	table.resizeEditorPanel(panelResizeStep)
	table.toggleEditorPanel()
	table.toggleResultsPanel()

	if table.editorCollapsed || table.resultsCollapsed {
		t.Fatal("a tab without an editor must not change split state")
	}
}

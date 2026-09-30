package components

import (
	"testing"

	"github.com/rivo/tview"
)

func TestStripTrailingSemicolon(t *testing.T) {
	cases := map[string]string{
		"SELECT 1;":    "SELECT 1",
		"SELECT 1;  ":  "SELECT 1",
		"  SELECT 1  ": "SELECT 1",
		"SELECT 1":     "SELECT 1",
		"":             "",
		"SELECT ';';":  "SELECT ';'",
	}
	for in, want := range cases {
		if got := stripTrailingSemicolon(in); got != want {
			t.Errorf("stripTrailingSemicolon(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStatementRangesIgnoresQuotedSemicolons(t *testing.T) {
	ranges := statementRanges("SELECT ';' FROM t; SELECT 2;\n-- c;omment\nSELECT 3")
	if len(ranges) != 3 {
		t.Fatalf("got %d ranges, want 3: %v", len(ranges), ranges)
	}
}

func TestStatementAtOffset(t *testing.T) {
	text := "select 1;\nselect 2;\nselect 3"

	cases := []struct {
		name   string
		offset int
		want   string
	}{
		{"first statement", 3, "select 1"},
		{"cursor on first semicolon", 8, "select 1"},
		{"second statement", 12, "select 2"},
		{"last statement", 22, "select 3"},
		{"trailing whitespace falls back to last", len(text), "select 3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statementAtOffset(text, tc.offset); got != tc.want {
				t.Errorf("statementAtOffset(%q, %d) = %q, want %q", text, tc.offset, got, tc.want)
			}
		})
	}

	if got := statementAtOffset("select 1", 0); got != "select 1" {
		t.Errorf("semicolon-less query = %q, want %q", got, "select 1")
	}
	if got := statementAtOffset("select 1;\n\nselect 2", 11); got != "select 2" {
		t.Errorf("cursor in whitespace between statements = %q, want %q", got, "select 2")
	}
}

func TestQueryToExecuteUsesStatementUnderCursor(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"select 1;", "select 2;"}
	e.cy, e.cx = 1, 0

	if got := e.QueryToExecute(); got != "select 2" {
		t.Fatalf("QueryToExecute = %q, want %q", got, "select 2")
	}

	e.cy, e.cx = 0, 3
	if got := e.QueryToExecute(); got != "select 1" {
		t.Fatalf("QueryToExecute = %q, want %q", got, "select 1")
	}
}

func TestQueryToExecutePrefersSelection(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"SELECT a", "FROM t;"}
	e.selecting = true
	e.selCY, e.selCX = 0, 0
	e.cy, e.cx = 0, 8

	if got := e.QueryToExecute(); got != "SELECT a" {
		t.Fatalf("QueryToExecute = %q, want %q", got, "SELECT a")
	}
}

func TestSQLEditorInsertText(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"ab"}
	e.cx = 1

	e.insertText("X\nY")
	if len(e.lines) != 2 || e.lines[0] != "aX" || e.lines[1] != "Yb" {
		t.Fatalf("insertText lines = %v, want [aX Yb]", e.lines)
	}
	if e.cy != 1 || e.cx != 1 {
		t.Fatalf("cursor = (%d,%d), want (1,1)", e.cy, e.cx)
	}
}

func TestSQLEditorPasteHandler(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{""}

	handler := e.PasteHandler()
	if handler == nil {
		t.Fatal("PasteHandler returned nil")
	}
	handler("SELECT 1\nFROM dual;", func(p tview.Primitive) {})

	if got := e.GetText(); got != "SELECT 1\nFROM dual;" {
		t.Fatalf("pasted text = %q", got)
	}
}

func TestSQLEditorYankMirrorsYankText(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"hello"}

	e.setYankText("hello")
	if e.yankText != "hello" {
		t.Fatalf("yankText = %q, want %q", e.yankText, "hello")
	}
}

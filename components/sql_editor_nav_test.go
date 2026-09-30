package components

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func ctrlKey(key tcell.Key) *tcell.EventKey {
	return tcell.NewEventKey(key, 0, tcell.ModNone)
}

// Terminals that send DEL (0x7F) for the Backspace key must keep deleting.
func TestSQLEditorBackspaceDelStillDeletes(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"abc"}
	e.cy, e.cx = 0, 3

	e.handleInsertMode(ctrlKey(tcell.KeyBackspace2))
	if e.lines[0] != "ab" || e.cx != 2 {
		t.Fatalf("after Backspace(DEL): line=%q cx=%d, want %q and 2", e.lines[0], e.cx, "ab")
	}
}

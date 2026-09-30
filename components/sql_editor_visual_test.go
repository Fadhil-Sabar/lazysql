package components

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/jorgerojas26/lazysql/app"
)

func drawTestEditor(t *testing.T, e *SQLEditor, width, height int) tcell.Screen {
	t.Helper()

	originalStyles := app.Styles
	if err := app.ApplyTheme(app.ThemeConfig{Preset: "dracula"}); err != nil {
		t.Fatalf("apply theme: %v", err)
	}
	t.Cleanup(func() { app.Styles = originalStyles })

	e.SetRect(0, 0, width, height)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("init simulation screen: %v", err)
	}
	t.Cleanup(screen.Fini)
	e.Draw(screen)
	return screen
}

func cellBackground(screen tcell.Screen, x, y int) tcell.Color {
	_, _, style, _ := screen.GetContent(x, y)
	_, bg, _ := style.Decompose()
	return bg
}

func TestSQLEditorVisualSelectionHighlights(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"foo bar baz"}
	e.vimMode = VimModeNormal
	e.cy, e.cx = 0, 0

	// enter visual mode, then extend with l
	e.handleNormalMode(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModNone))
	if !e.selecting {
		t.Fatal("v did not enter visual mode")
	}
	e.handleVisualMode(tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone))
	e.handleVisualMode(tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone))

	screen := drawTestEditor(t, e, 40, 5)

	for x := 0; x < 3; x++ {
		if bg := cellBackground(screen, x, 0); bg != app.Styles.EditorSelectionColor {
			t.Errorf("cell %d background = %v, want selection %v", x, bg, app.Styles.EditorSelectionColor)
		}
	}
}

func TestSQLEditorVisualSelectionWords(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"foo bar baz"}
	e.vimMode = VimModeNormal
	e.cy, e.cx = 0, 0

	e.handleNormalMode(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModNone))
	e.handleVisualMode(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone))

	if !e.selecting {
		t.Fatal("selection dropped after w")
	}
	if e.cx != 4 {
		t.Fatalf("after w cx = %d, want 4", e.cx)
	}

	screen := drawTestEditor(t, e, 40, 5)
	if bg := cellBackground(screen, 0, 0); bg != app.Styles.EditorSelectionColor {
		t.Errorf("cell 0 not selected: bg=%v", bg)
	}
}

func TestSQLEditorVisualSelectionBackward(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"foo bar baz"}
	e.vimMode = VimModeNormal
	e.cy, e.cx = 0, 8 // on "baz"

	e.handleNormalMode(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModNone))
	e.handleVisualMode(tcell.NewEventKey(tcell.KeyRune, 'b', tcell.ModNone))

	if e.cx != 4 {
		t.Fatalf("after b cx = %d, want 4", e.cx)
	}

	screen := drawTestEditor(t, e, 40, 5)
	if bg := cellBackground(screen, 6, 0); bg != app.Styles.EditorSelectionColor {
		t.Errorf("cell 6 (inside b..anchor) not selected: bg=%v", bg)
	}
}

func TestSQLEditorWordMotions(t *testing.T) {
	newEditor := func(cx int) *SQLEditor {
		e := NewSQLEditor("")
		e.lines = []string{"foo bar baz"}
		e.vimMode = VimModeNormal
		e.cy, e.cx = 0, cx
		return e
	}

	// w: start of next word
	e := newEditor(0)
	e.wordForward()
	if e.cx != 4 {
		t.Errorf("w from 0 -> cx=%d, want 4", e.cx)
	}
	e.wordForward()
	if e.cx != 8 {
		t.Errorf("w from 4 -> cx=%d, want 8", e.cx)
	}

	// b: start of previous word
	e = newEditor(8)
	e.wordBackward()
	if e.cx != 4 {
		t.Errorf("b from 8 -> cx=%d, want 4", e.cx)
	}

	// e: end of current word, then end of next word
	e = newEditor(0)
	e.wordEnd()
	if e.cx != 2 {
		t.Errorf("e from 0 -> cx=%d, want 2", e.cx)
	}
	e.wordEnd()
	if e.cx != 6 {
		t.Errorf("e from 2 -> cx=%d, want 6", e.cx)
	}
}

func TestSQLEditorSelectionIsInclusive(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"foo bar baz"}
	e.selecting = true
	e.vimMode = VimModeVisual
	e.selCY, e.selCX = 0, 0
	e.cy, e.cx = 0, 2

	if got := e.getSelectedText(); got != "foo" {
		t.Fatalf("getSelectedText = %q, want %q", got, "foo")
	}
}

func TestSQLEditorVisualSelectionMultiLine(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"foo", "bar"}
	e.vimMode = VimModeNormal
	e.cy, e.cx = 0, 0

	e.handleNormalMode(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModNone))
	e.handleVisualMode(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone))

	if e.cy != 1 {
		t.Fatalf("after j cy = %d, want 1", e.cy)
	}

	screen := drawTestEditor(t, e, 40, 6)

	// Whole first line selected.
	for x := 0; x < 3; x++ {
		if bg := cellBackground(screen, x, 0); bg != app.Styles.EditorSelectionColor {
			t.Errorf("line0 cell %d bg = %v, want selection", x, bg)
		}
	}
	// Only the first character of the last line (cursor char).
	if bg := cellBackground(screen, 0, 1); bg != app.Styles.EditorSelectionColor {
		t.Errorf("line1 cell 0 bg = %v, want selection", bg)
	}
	if bg := cellBackground(screen, 1, 1); bg == app.Styles.EditorSelectionColor {
		t.Error("line1 cell 1 should not be selected")
	}
}

func TestSQLEditorVisualLineUsesWholeLine(t *testing.T) {
	line := "DELETE FROM reference.mst_activity_types WHERE value = '000020';"
	e := NewSQLEditor("")
	e.lines = []string{line}
	e.selecting = true
	e.vimMode = VimModeVisualLine
	e.selCY, e.selCX = 0, 0
	e.cy, e.cx = 0, 20 // cursor somewhere inside the line

	if got := e.getSelectedText(); got != line {
		t.Fatalf("getSelectedText = %q, want %q", got, line)
	}

	want := "DELETE FROM reference.mst_activity_types WHERE value = '000020'"
	if got := e.QueryToExecute(); got != want {
		t.Fatalf("QueryToExecute = %q, want %q", got, want)
	}
}

func TestSQLEditorVisualLineMultiLineText(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"a", "b", "c"}
	e.selecting = true
	e.vimMode = VimModeVisualLine
	e.selCY, e.selCX = 0, 0
	e.cy, e.cx = 2, 0

	if got := e.getSelectedText(); got != "a\nb\nc" {
		t.Fatalf("getSelectedText = %q, want %q", got, "a\nb\nc")
	}
}

func TestSQLEditorVisualLineDeleteRemovesLines(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"first", "second", "third"}
	e.selecting = true
	e.vimMode = VimModeVisualLine
	e.selCY, e.selCX = 1, 0
	e.cy, e.cx = 1, 2

	e.pushUndo()
	e.deleteSelection()

	if len(e.lines) != 2 || e.lines[0] != "first" || e.lines[1] != "third" {
		t.Fatalf("lines = %v, want [first third]", e.lines)
	}
	if e.cy != 1 || e.cx != 0 {
		t.Fatalf("cursor = (%d,%d), want (1,0)", e.cy, e.cx)
	}
	if e.yankText != "second" {
		t.Fatalf("yanked = %q, want %q", e.yankText, "second")
	}
}

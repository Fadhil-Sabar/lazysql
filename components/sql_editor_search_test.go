package components

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/jorgerojas26/lazysql/app"
)

func TestSQLEditorFindMatches(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"SELECT * FROM users", "select id from users", "WHERE users.id = 1"}
	e.searchPattern = "users"

	matches := e.findMatches()
	if len(matches) != 3 {
		t.Fatalf("got %d matches, want 3: %v", len(matches), matches)
	}

	cases := []struct {
		line       int
		start, end int
	}{
		{0, 14, 19},
		{1, 15, 20},
		{2, 6, 11},
	}
	for i, want := range cases {
		if matches[i] != [3]int{want.line, want.start, want.end} {
			t.Errorf("match %d = %v, want %v", i, matches[i], want)
		}
	}
}

func TestSQLEditorSearchJumpWraps(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"aXbXc", "X"}
	e.searchPattern = "x"

	steps := []struct {
		cy, cx int
	}{
		{0, 1}, {0, 3}, {1, 0}, {0, 1}, // forward, wrapping
	}
	e.cy, e.cx = 0, 0
	for i, want := range steps {
		if !e.searchJump(true) {
			t.Fatalf("step %d: searchJump returned false", i)
		}
		if e.cy != want.cy || e.cx != want.cx {
			t.Fatalf("step %d: cursor = (%d,%d), want (%d,%d)", i, e.cy, e.cx, want.cy, want.cx)
		}
	}

	// backward wraps from the first match to the last
	e.cy, e.cx = 0, 1
	if !e.searchJump(false) {
		t.Fatal("backward searchJump returned false")
	}
	if e.cy != 1 || e.cx != 0 {
		t.Fatalf("backward cursor = (%d,%d), want (1,0)", e.cy, e.cx)
	}
}

func TestSQLEditorSearchInputLiveJump(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"one two three"}
	e.cy, e.cx = 0, 0

	e.startSearch(false)
	if !e.searchMode || e.searchPattern != "" {
		t.Fatal("startSearch did not open an empty prompt")
	}

	e.handleSearchInput(tcell.NewEventKey(tcell.KeyRune, 't', tcell.ModNone))
	if e.searchPattern != "t" {
		t.Fatalf("pattern = %q, want %q", e.searchPattern, "t")
	}
	if e.cx != 4 { // first "t" of "two"
		t.Fatalf("cx = %d, want 4", e.cx)
	}

	// Enter finalizes the prompt but keeps the pattern highlighted.
	e.handleSearchInput(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if e.searchMode || e.searchPattern != "t" {
		t.Fatalf("after enter: mode=%v pattern=%q", e.searchMode, e.searchPattern)
	}
}

func TestSQLEditorSearchEscapeClears(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"abc"}
	e.searchPattern = "a"

	e.handleNormalMode(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if e.searchPattern != "" {
		t.Fatalf("escape did not clear search pattern (still %q)", e.searchPattern)
	}
}

func TestSQLEditorSearchHighlightDraws(t *testing.T) {
	originalStyles := app.Styles
	if err := app.ApplyTheme(app.ThemeConfig{Preset: "dracula"}); err != nil {
		t.Fatalf("apply theme: %v", err)
	}
	defer func() { app.Styles = originalStyles }()

	e := NewSQLEditor("")
	e.lines = []string{"hello world"}
	e.searchPattern = "world"
	e.SetRect(0, 0, 40, 5)

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("init simulation screen: %v", err)
	}
	defer screen.Fini()

	e.Draw(screen)

	_, _, style, _ := screen.GetContent(6, 0) // the 'w' of "world"
	_, bg, _ := style.Decompose()
	if bg != app.Styles.EditorSelectionColor {
		t.Fatalf("search match background = %v, want %v", bg, app.Styles.EditorSelectionColor)
	}
}

func TestSQLEditorWordUnderCursor(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"SELECT id FROM users"}

	cases := []struct {
		cx   int
		want string
	}{
		{0, "SELECT"}, // start of line
		{3, "SELECT"}, // inside word
		{6, "SELECT"}, // on trailing space -> previous word
		{7, "id"},     // inside "id"
		{9, "id"},     // on space after "id"
		{11, "FROM"},  // start of "FROM"
		{19, "users"}, // inside "users"
		{20, "users"}, // end of line -> last word
	}
	for _, tc := range cases {
		e.cx = tc.cx
		if got := e.wordUnderCursor(); got != tc.want {
			t.Errorf("wordUnderCursor at cx=%d = %q, want %q", tc.cx, got, tc.want)
		}
	}

	e.lines = []string{"   "}
	e.cx = 1
	if got := e.wordUnderCursor(); got != "" {
		t.Fatalf("wordUnderCursor on whitespace = %q, want empty", got)
	}
}

func TestSQLEditorStarAndHash(t *testing.T) {
	e := NewSQLEditor("")
	e.lines = []string{"alpha beta gamma", "beta delta"}

	// Cursor on the first "beta": '*' picks the word and jumps forward.
	e.cy, e.cx = 0, 6
	e.handleNormalMode(tcell.NewEventKey(tcell.KeyRune, '*', tcell.ModNone))
	if e.searchPattern != "beta" {
		t.Fatalf("'*' pattern = %q, want %q", e.searchPattern, "beta")
	}
	if e.cy != 1 || e.cx != 0 {
		t.Fatalf("'*' cursor = (%d,%d), want (1,0)", e.cy, e.cx)
	}

	// '#' walks back to the previous occurrence.
	e.handleNormalMode(tcell.NewEventKey(tcell.KeyRune, '#', tcell.ModNone))
	if e.cy != 0 || e.cx != 6 {
		t.Fatalf("'#' cursor = (%d,%d), want (0,6)", e.cy, e.cx)
	}
	if !e.searchBackward {
		t.Fatal("'#' did not switch the search direction to backward")
	}

	// With no active pattern and no word under the cursor, '*' is a no-op.
	e.searchPattern = ""
	e.lines = []string{"alpha (*) beta"}
	e.cy, e.cx = 0, 6 // on the '(' punctuation
	e.handleNormalMode(tcell.NewEventKey(tcell.KeyRune, '*', tcell.ModNone))
	if e.searchPattern != "" {
		t.Fatalf("'*' off-word set a pattern: %q", e.searchPattern)
	}
}

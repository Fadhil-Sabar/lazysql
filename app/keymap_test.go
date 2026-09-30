package app

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	cmd "github.com/jorgerojas26/lazysql/commands"
	"github.com/jorgerojas26/lazysql/models"
)

func TestParseKeyString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Key
		wantErr bool
	}{
		{
			name:  "single lowercase char",
			input: "q",
			want:  Key{Char: 'q'},
		},
		{
			name:  "single uppercase char",
			input: "G",
			want:  Key{Char: 'G'},
		},
		{
			name:  "single digit",
			input: "1",
			want:  Key{Char: '1'},
		},
		{
			name:  "special char slash",
			input: "/",
			want:  Key{Char: '/'},
		},
		{
			name:  "special key Ctrl+S",
			input: "Ctrl-S",
			want:  Key{Code: tcell.KeyCtrlS},
		},
		{
			name:  "special key Enter",
			input: "Enter",
			want:  Key{Code: tcell.KeyEnter},
		},
		{
			name:  "special key Esc",
			input: "Esc",
			want:  Key{Code: tcell.KeyEscape},
		},
		{
			name:    "unknown key returns error",
			input:   "NonExistentKey",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseKeyString(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseKeyString(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("parseKeyString(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSetBindings(t *testing.T) {
	t.Run("rebinds existing command", func(t *testing.T) {
		group := Map{
			{Key: Key{Char: 'q'}, Cmd: cmd.Quit},
			{Key: Key{Char: 'j'}, Cmd: cmd.MoveDown},
		}

		bindings := map[string]string{
			"Quit": "x",
		}

		updated, err := setBindings(bindings, group, "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if updated[0].Key.Char != 'x' {
			t.Errorf("expected Quit to be rebound to 'x', got %+v", updated[0].Key)
		}

		if updated[1].Key.Char != 'j' {
			t.Errorf("expected MoveDown to remain 'j', got %+v", updated[1].Key)
		}
	})

	t.Run("rebinds to special key", func(t *testing.T) {
		group := Map{
			{Key: Key{Char: 'q'}, Cmd: cmd.Quit},
		}

		bindings := map[string]string{
			"Quit": "Esc",
		}

		updated, err := setBindings(bindings, group, "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if updated[0].Key.Code != tcell.KeyEscape {
			t.Errorf("expected Quit to be rebound to Esc, got %+v", updated[0].Key)
		}
	})

	t.Run("unknown command returns error", func(t *testing.T) {
		group := Map{
			{Key: Key{Char: 'q'}, Cmd: cmd.Quit},
		}

		bindings := map[string]string{
			"FakeCommand": "x",
		}

		_, err := setBindings(bindings, group, "test")
		if err == nil {
			t.Fatal("expected error for unknown command, got nil")
		}
	})

	t.Run("invalid key returns error", func(t *testing.T) {
		group := Map{
			{Key: Key{Char: 'q'}, Cmd: cmd.Quit},
		}

		bindings := map[string]string{
			"Quit": "BadKey",
		}

		_, err := setBindings(bindings, group, "test")
		if err == nil {
			t.Fatal("expected error for invalid key, got nil")
		}
	})

	t.Run("multiple bindings", func(t *testing.T) {
		group := Map{
			{Key: Key{Char: 'q'}, Cmd: cmd.Quit},
			{Key: Key{Char: 'j'}, Cmd: cmd.MoveDown},
			{Key: Key{Char: 'k'}, Cmd: cmd.MoveUp},
		}

		bindings := map[string]string{
			"Quit":   "x",
			"MoveUp": "p",
		}

		updated, err := setBindings(bindings, group, "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if updated[0].Key.Char != 'x' {
			t.Errorf("expected Quit rebound to 'x', got %+v", updated[0].Key)
		}
		if updated[1].Key.Char != 'j' {
			t.Errorf("expected MoveDown unchanged at 'j', got %+v", updated[1].Key)
		}
		if updated[2].Key.Char != 'p' {
			t.Errorf("expected MoveUp rebound to 'p', got %+v", updated[2].Key)
		}
	})
}

func saveKeymaps() map[string]Map {
	saved := make(map[string]Map, len(Keymaps.Groups))
	for k, v := range Keymaps.Groups {
		cp := make(Map, len(v))
		copy(cp, v)
		saved[k] = cp
	}
	return saved
}

func restoreKeymaps(saved map[string]Map) {
	for k, v := range saved {
		Keymaps.Groups[k] = v
	}
}

func TestApplyKeymapConfig(t *testing.T) {
	t.Run("nil config is no-op", func(t *testing.T) {
		err := ApplyKeymapConfig(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("empty config is no-op", func(t *testing.T) {
		err := ApplyKeymapConfig(models.KeymapConfig{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("valid config rebinds key", func(t *testing.T) {
		saved := saveKeymaps()
		defer restoreKeymaps(saved)

		cfg := models.KeymapConfig{
			"home": {"Quit": "x"},
		}

		err := ApplyKeymapConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		group := Keymaps.Groups[HomeGroup]
		for _, bind := range group {
			if bind.Cmd == cmd.Quit {
				if bind.Key.Char != 'x' {
					t.Errorf("expected Quit rebound to 'x', got %+v", bind.Key)
				}
				return
			}
		}
		t.Error("Quit command not found in home group")
	})

	t.Run("global theme picker binding", func(t *testing.T) {
		saved := append(Map(nil), Keymaps.Global...)
		defer func() { Keymaps.Global = saved }()

		cfg := models.KeymapConfig{
			"GLOBAL": {"ThemePicker": "Ctrl-Y"},
		}

		if err := ApplyKeymapConfig(cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := Keymaps.Global[0].Key.Code; got != tcell.KeyCtrlY {
			t.Errorf("ThemePicker key = %v, want Ctrl-Y", got)
		}
	})

	t.Run("case insensitive group name", func(t *testing.T) {
		saved := saveKeymaps()
		defer restoreKeymaps(saved)

		cfg := models.KeymapConfig{
			"Home": {"Quit": "x"},
		}

		err := ApplyKeymapConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		group := Keymaps.Groups[HomeGroup]
		for _, bind := range group {
			if bind.Cmd == cmd.Quit {
				if bind.Key.Char != 'x' {
					t.Errorf("expected Quit rebound to 'x', got %+v", bind.Key)
				}
				return
			}
		}
		t.Error("Quit command not found in home group")
	})

	t.Run("unknown group returns error", func(t *testing.T) {
		cfg := models.KeymapConfig{
			"nonexistent": {"Quit": "x"},
		}

		err := ApplyKeymapConfig(cfg)
		if err == nil {
			t.Fatal("expected error for unknown group, got nil")
		}
	})

	t.Run("unknown command in group returns error", func(t *testing.T) {
		cfg := models.KeymapConfig{
			"home": {"FakeCommand": "x"},
		}

		err := ApplyKeymapConfig(cfg)
		if err == nil {
			t.Fatal("expected error for unknown command, got nil")
		}
	})

	t.Run("invalid key string returns error", func(t *testing.T) {
		cfg := models.KeymapConfig{
			"home": {"Quit": "SuperBadKey"},
		}

		err := ApplyKeymapConfig(cfg)
		if err == nil {
			t.Fatal("expected error for invalid key, got nil")
		}
	})

	t.Run("rebinds special key in table group", func(t *testing.T) {
		saved := saveKeymaps()
		defer restoreKeymaps(saved)

		cfg := models.KeymapConfig{
			"table": {"Search": "Ctrl-F"},
		}

		err := ApplyKeymapConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		group := Keymaps.Groups[TableGroup]
		for _, bind := range group {
			if bind.Cmd == cmd.Search {
				if bind.Key.Code != tcell.KeyCtrlF {
					t.Errorf("expected Search rebound to Ctrl-F, got %+v", bind.Key)
				}
				return
			}
		}
		t.Error("Search command not found in table group")
	})

	t.Run("rebinds foreign key jump in table group", func(t *testing.T) {
		saved := saveKeymaps()
		defer restoreKeymaps(saved)

		cfg := models.KeymapConfig{
			"table": {"ForeignKeyJump": "F"},
		}

		if err := ApplyKeymapConfig(cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		group := Keymaps.Groups[TableGroup]
		for _, bind := range group {
			if bind.Cmd == cmd.ForeignKeyJump {
				if bind.Key.Char != 'F' {
					t.Errorf("expected ForeignKeyJump rebound to 'F', got %+v", bind.Key)
				}
				return
			}
		}
		t.Error("ForeignKeyJump command not found in table group")
	})

	t.Run("multiple groups and bindings", func(t *testing.T) {
		saved := saveKeymaps()
		defer restoreKeymaps(saved)

		cfg := models.KeymapConfig{
			"home": {"Quit": "x"},
			"tree": {"GotoTop": "t"},
		}

		err := ApplyKeymapConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		homeGroup := Keymaps.Groups[HomeGroup]
		for _, bind := range homeGroup {
			if bind.Cmd == cmd.Quit {
				if bind.Key.Char != 'x' {
					t.Errorf("home: expected Quit rebound to 'x', got %+v", bind.Key)
				}
				break
			}
		}

		treeGroup := Keymaps.Groups[TreeGroup]
		for _, bind := range treeGroup {
			if bind.Cmd == cmd.GotoTop {
				if bind.Key.Char != 't' {
					t.Errorf("tree: expected GotoTop rebound to 't', got %+v", bind.Key)
				}
				break
			}
		}
	})
}

func TestForeignKeyJumpDefaultBinding(t *testing.T) {
	group := Keymaps.Groups[TableGroup]

	var bind *Bind
	for i := range group {
		if group[i].Cmd == cmd.ForeignKeyJump {
			bind = &group[i]
			break
		}
	}

	if bind == nil {
		t.Fatal("ForeignKeyJump command not found in table group")
	}

	if bind.Key.Code != tcell.KeyEnter {
		t.Errorf("expected ForeignKeyJump bound to Enter, got %+v", bind.Key)
	}

	if got := group.Resolve(tcell.NewEventKey(tcell.KeyEnter, 0, 0)); got != cmd.ForeignKeyJump {
		t.Errorf("expected Enter to resolve to ForeignKeyJump, got %s", got)
	}
}

func TestPanelNavigationDefaultBindings(t *testing.T) {
	home := Keymaps.Group(HomeGroup)

	cases := []struct {
		event *tcell.EventKey
		want  cmd.Command
	}{
		{tcell.NewEventKey(tcell.KeyCtrlH, 0, 0), cmd.PanelLeft},
		{tcell.NewEventKey(tcell.KeyCtrlJ, 0, 0), cmd.PanelDown},
		{tcell.NewEventKey(tcell.KeyCtrlK, 0, 0), cmd.PanelUp},
		{tcell.NewEventKey(tcell.KeyCtrlL, 0, 0), cmd.PanelRight},
		{tcell.NewEventKey(tcell.KeyRune, '1', 0), cmd.FocusSchemaPanel},
		{tcell.NewEventKey(tcell.KeyRune, '2', 0), cmd.FocusEditorPanel},
		{tcell.NewEventKey(tcell.KeyRune, '3', 0), cmd.FocusResultsPanel},
		{tcell.NewEventKey(tcell.KeyRune, '4', 0), cmd.FocusTransactionPanel},
	}

	for _, tc := range cases {
		if got := home.Resolve(tc.event); got != tc.want {
			t.Errorf("home group resolved %q to %s, want %s", tc.event.Name(), got, tc.want)
		}
	}
}

func TestResultsMenuMovedOffDigits(t *testing.T) {
	table := Keymaps.Group(TableGroup)

	menuKeys := []struct {
		key  tcell.Key
		want cmd.Command
	}{
		{tcell.KeyF1, cmd.RecordsMenu},
		{tcell.KeyF2, cmd.ColumnsMenu},
		{tcell.KeyF3, cmd.ConstraintsMenu},
		{tcell.KeyF4, cmd.ForeignKeysMenu},
		{tcell.KeyF5, cmd.IndexesMenu},
	}

	for _, tc := range menuKeys {
		if got := table.Resolve(tcell.NewEventKey(tc.key, 0, 0)); got != tc.want {
			t.Errorf("table group resolved %s to %s, want %s", tcell.KeyNames[tc.key], got, tc.want)
		}
	}

	for _, digit := range []rune{'1', '2', '3', '4', '5'} {
		if got := table.Resolve(tcell.NewEventKey(tcell.KeyRune, digit, 0)); got != cmd.Noop {
			t.Errorf("table group still resolves digit %q to %s", digit, got)
		}
	}
}

func TestTransactionDefaultBindings(t *testing.T) {
	table := Keymaps.Group(TableGroup)

	// The grid now uses c/r for the transaction and i for editing a cell.
	grid := []struct {
		r    rune
		want cmd.Command
	}{
		{'c', cmd.CommitTransaction},
		{'r', cmd.RollbackTransaction},
		{'i', cmd.Edit},
		{'d', cmd.Delete},
	}
	for _, tc := range grid {
		if got := table.Resolve(tcell.NewEventKey(tcell.KeyRune, tc.r, 0)); got != tc.want {
			t.Errorf("table group resolved %q to %s, want %s", tc.r, got, tc.want)
		}
	}

	transaction := Keymaps.Group(TransactionGroup)
	panel := []struct {
		event *tcell.EventKey
		want  cmd.Command
	}{
		{tcell.NewEventKey(tcell.KeyRune, 'c', 0), cmd.CommitTransaction},
		{tcell.NewEventKey(tcell.KeyRune, 'r', 0), cmd.RollbackTransaction},
		{tcell.NewEventKey(tcell.KeyRune, 'x', 0), cmd.ClearTransactionHistory},
		{tcell.NewEventKey(tcell.KeyRune, 'm', 0), cmd.ToggleCommitMode},
		{tcell.NewEventKey(tcell.KeyRune, 'y', 0), cmd.Copy},
		{tcell.NewEventKey(tcell.KeyRune, 'j', 0), cmd.MoveDown},
		{tcell.NewEventKey(tcell.KeyRune, 'k', 0), cmd.MoveUp},
		{tcell.NewEventKey(tcell.KeyRune, 'q', 0), cmd.Quit},
		{tcell.NewEventKey(tcell.KeyEscape, 0, 0), cmd.Quit},
	}
	for _, tc := range panel {
		if got := transaction.Resolve(tc.event); got != tc.want {
			t.Errorf("transaction group resolved %q to %s, want %s", tc.event.Name(), got, tc.want)
		}
	}
}

func TestCommitModeDefaultBindings(t *testing.T) {
	if got := Keymaps.Group(HomeGroup).Resolve(tcell.NewEventKey(tcell.KeyCtrlB, 0, 0)); got != cmd.ToggleCommitMode {
		t.Errorf("home group resolved Ctrl+B to %s, want ToggleCommitMode", got)
	}

	table := Keymaps.Group(TableGroup)
	for _, r := range []rune{'c', 'r', 'i'} {
		if got := table.Resolve(tcell.NewEventKey(tcell.KeyRune, r, 0)); got == cmd.ToggleCommitMode {
			t.Errorf("results grid must not toggle commit mode on %q", r)
		}
	}
}

func TestPanelResizeDefaultBindings(t *testing.T) {
	home := Keymaps.Group(HomeGroup)

	cases := []struct {
		event *tcell.EventKey
		want  cmd.Command
	}{
		{tcell.NewEventKey(tcell.KeyRune, '+', 0), cmd.GrowPanel},
		{tcell.NewEventKey(tcell.KeyRune, '=', 0), cmd.GrowPanel},
		{tcell.NewEventKey(tcell.KeyRune, '-', 0), cmd.ShrinkPanel},
		{tcell.NewEventKey(tcell.KeyRune, 'T', 0), cmd.TogglePanel},
	}

	for _, tc := range cases {
		if got := home.Resolve(tc.event); got != tc.want {
			t.Errorf("home group resolved %q to %s, want %s", tc.event.Name(), got, tc.want)
		}
	}
}

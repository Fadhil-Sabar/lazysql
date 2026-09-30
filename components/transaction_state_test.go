package components

import (
	"errors"
	"strings"
	"testing"
)

func TestTransactionBeginClassification(t *testing.T) {
	begin := []struct{ query, verb string }{
		{"BEGIN;", "BEGIN"},
		{"begin", "BEGIN"},
		{"BEGIN TRANSACTION", "BEGIN"},
		{"START TRANSACTION", "START"},
		{"START TRANSACTION ISOLATION LEVEL SERIALIZABLE", "START"},
	}
	for _, tc := range begin {
		if !isTransactionBeginQuery(tc.query, tc.verb) {
			t.Errorf("isTransactionBeginQuery(%q, %q) = false, want true", tc.query, tc.verb)
		}
	}

	notBegin := []struct{ query, verb string }{
		{"SELECT 1", "SELECT"},
		{"BEGINNING", "BEGINNING"},
		{"START", "START"},
		{"COMMIT", "COMMIT"},
	}
	for _, tc := range notBegin {
		if isTransactionBeginQuery(tc.query, tc.verb) {
			t.Errorf("isTransactionBeginQuery(%q, %q) = true, want false", tc.query, tc.verb)
		}
	}
}

func TestTransactionEndClassification(t *testing.T) {
	end := []struct{ query, verb string }{
		{"COMMIT;", "COMMIT"},
		{"COMMIT WORK", "COMMIT"},
		{"ROLLBACK;", "ROLLBACK"},
		{"END TRANSACTION", "END"},
	}
	for _, tc := range end {
		if !isTransactionEndQuery(tc.query, tc.verb) {
			t.Errorf("isTransactionEndQuery(%q, %q) = false, want true", tc.query, tc.verb)
		}
	}

	// A savepoint rollback does not end the transaction, and a bare END is a
	// PL/SQL block terminator rather than COMMIT.
	partial := []struct{ query, verb string }{
		{"ROLLBACK TO SAVEPOINT sp1", "ROLLBACK"},
		{"ROLLBACK TO sp1", "ROLLBACK"},
		{"END;", "END"},
		{"SAVEPOINT sp1", "SAVEPOINT"},
	}
	for _, tc := range partial {
		if isTransactionEndQuery(tc.query, tc.verb) {
			t.Errorf("isTransactionEndQuery(%q, %q) = true, want false", tc.query, tc.verb)
		}
	}

	if !isTransactionRollbackQuery("ROLLBACK;", "ROLLBACK") {
		t.Error("ROLLBACK should be treated as a discarding statement")
	}
	if isTransactionRollbackQuery("COMMIT;", "COMMIT") {
		t.Error("COMMIT must not be treated as a rollback")
	}
}

func TestTransactionTableNameExtraction(t *testing.T) {
	cases := []struct {
		query string
		verb  string
		want  string
	}{
		{"DELETE FROM reference.mst_activity_types WHERE value = '000020';", "DELETE", "reference.mst_activity_types"},
		{"update public.orders set total = 1", "UPDATE", "public.orders"},
		{"INSERT INTO public.users (email) VALUES ('a@b.c')", "INSERT", "public.users"},
		{"TRUNCATE TABLE public.numbers", "TRUNCATE", "public.numbers"},
		{"DELETE FROM \"Weird Name\" WHERE 1=1", "DELETE", "Weird Name"},
		{"SELECT * FROM public.users", "SELECT", ""},
		{"BEGIN", "BEGIN", ""},
	}

	for _, tc := range cases {
		if got := txTableName(tc.query, tc.verb); got != tc.want {
			t.Errorf("txTableName(%q, %q) = %q, want %q", tc.query, tc.verb, got, tc.want)
		}
	}
}

func TestTransactionStateLifecycle(t *testing.T) {
	state := NewTransactionState()

	beginID := state.Record("BEGIN;", "BEGIN", false)
	state.Complete(beginID, txKindControl, -1, nil)
	state.Begin()

	if !state.Active() {
		t.Fatal("transaction should be active after BEGIN")
	}

	deleteID := state.Record("DELETE FROM public.orders WHERE id = 1;", "DELETE", true)
	state.Complete(deleteID, txKindAffected, 1, nil)

	updateID := state.Record("UPDATE public.orders SET total = 2 WHERE id = 2;", "UPDATE", true)
	state.Complete(updateID, txKindAffected, 3, nil)

	pending := state.Pending()
	if len(pending) != 2 {
		t.Fatalf("pending statements = %d, want 2", len(pending))
	}

	summary := state.SummarizePending()
	if summary.Statements != 2 {
		t.Errorf("summary statements = %d, want 2", summary.Statements)
	}
	if summary.Rows != 4 {
		t.Errorf("summary rows = %d, want 4", summary.Rows)
	}
	if len(summary.Tables) != 1 || summary.Tables[0] != "public.orders" {
		t.Errorf("summary tables = %v, want [public.orders]", summary.Tables)
	}
	if summary.Failed != 0 {
		t.Errorf("summary failed = %d, want 0", summary.Failed)
	}

	text := txConfirmationText("commit", pending, summary)
	for _, want := range []string{
		"COMMIT 2 pending statement(s)?",
		"DELETE FROM public.orders WHERE id = 1;",
		"Tables affected:",
		"public.orders",
		"Total rows affected:",
		"These changes will be written to the database.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("commit confirmation is missing %q:\n%s", want, text)
		}
	}

	rollbackText := txConfirmationText("rollback", pending, summary)
	if !strings.Contains(rollbackText, "All of these changes will be discarded") {
		t.Errorf("rollback confirmation should warn about discarding:\n%s", rollbackText)
	}

	state.Resolve(txOutcomeCommitted)

	if state.Active() {
		t.Error("transaction should no longer be active after resolve")
	}
	if len(state.Pending()) != 0 {
		t.Error("no statements should remain pending after resolve")
	}

	entries := state.Entries()
	if len(entries) != 3 {
		t.Fatalf("history entries = %d, want 3", len(entries))
	}
	if entries[0].Outcome != txOutcomeAutocommit {
		t.Errorf("BEGIN outcome = %q, want %q", entries[0].Outcome, txOutcomeAutocommit)
	}
	if entries[1].Outcome != txOutcomeCommitted || entries[2].Outcome != txOutcomeCommitted {
		t.Errorf("pending statements were not marked committed: %q, %q", entries[1].Outcome, entries[2].Outcome)
	}
}

func TestTransactionStateTracksFailures(t *testing.T) {
	state := NewTransactionState()
	state.Begin()

	id := state.Record("DELETE FROM nope;", "DELETE", true)
	state.Complete(id, txKindAffected, 0, errors.New(`relation "nope" does not exist`))

	if !state.Failed() {
		t.Fatal("state should report a failure inside the transaction")
	}

	pending := state.Pending()
	if len(pending) != 1 {
		t.Fatalf("failed statement should stay pending, got %d entries", len(pending))
	}
	if pending[0].Err == "" {
		t.Error("failed statement should keep its error message")
	}
	if got := txRowLabel(pending[0]); got != "failed" {
		t.Errorf("txRowLabel for failed statement = %q, want failed", got)
	}

	summary := state.SummarizePending()
	if summary.Failed != 1 {
		t.Errorf("summary failed = %d, want 1", summary.Failed)
	}

	text := txConfirmationText("commit", pending, summary)
	if !strings.Contains(text, "1 statement(s) failed") {
		t.Errorf("commit confirmation should warn about failed statements:\n%s", text)
	}

	state.Resolve(txOutcomeRolledBack)
	if state.Failed() {
		t.Error("failure flag should be reset when the transaction resolves")
	}
}

func TestTransactionStateClearRequiresResolvedTransaction(t *testing.T) {
	state := NewTransactionState()

	id := state.Record("SELECT 1;", "SELECT", false)
	state.Complete(id, txKindRows, 1, nil)

	if !state.Clear() {
		t.Fatal("clear should succeed with no active transaction")
	}
	if len(state.Entries()) != 0 {
		t.Fatalf("history should be empty after clear, got %d entries", len(state.Entries()))
	}

	state.Begin()
	pendingID := state.Record("DELETE FROM public.orders WHERE id = 1;", "DELETE", true)
	state.Complete(pendingID, txKindAffected, 1, nil)

	if state.Clear() {
		t.Fatal("clear must refuse while a transaction is open")
	}
	if len(state.Entries()) != 1 {
		t.Fatalf("refused clear must not drop pending statements: %d entries", len(state.Entries()))
	}
}

func TestTransactionResultLabel(t *testing.T) {
	cases := []struct {
		name     string
		kind     txKind
		affected int64
		want     string
	}{
		{"single row", txKindAffected, 1, "1 row affected"},
		{"several rows", txKindAffected, 5, "5 rows affected"},
		{"zero rows", txKindAffected, 0, "0 rows affected"},
		{"unknown count", txKindControl, -1, "Statement executed"},
		{"control statement", txKindControl, 0, "Statement executed"},
		{"single read row", txKindRows, 1, "1 row"},
		{"read rows", txKindRows, 12, "12 rows"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := txResultLabel(tc.kind, tc.affected); got != tc.want {
				t.Errorf("txResultLabel(%q, %d) = %q, want %q", tc.kind, tc.affected, got, tc.want)
			}
		})
	}
}

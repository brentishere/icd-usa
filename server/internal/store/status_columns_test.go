package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/singhand-labs/AegisCrawler/internal/models"
)

// Regression: a worker's running update carries informational text
// ("Started rule <id>") which previously landed in error_message and then
// showed in the admin UI as an error on a task that finished done.
func TestStatusUpdatesNeverWriteErrorColumnsForRunning(t *testing.T) {
	s := newStatusColumnStore(t)
	ctx := context.Background()

	task := createStatusColumnTask(t, s, ctx)

	// running with a message: informational text must NOT touch error columns
	if err := s.UpdateTaskStatus(ctx, task, "worker-1", string(models.TaskStatusRunning), "Started rule ext-1", ""); err != nil {
		t.Fatalf("running update: %v", err)
	}
	got := fetchStatusTask(t, s, ctx, task)
	if got.ErrorMessage.String != "" || got.ErrorType.String != "" {
		t.Fatalf("running update leaked into error columns: type=%q message=%q", got.ErrorType.String, got.ErrorMessage.String)
	}
	if string(got.Status) != string(models.TaskStatusRunning) {
		t.Fatalf("status = %q, want running", got.Status)
	}

	// done with empty message: must stay clean (and clear any residue)
	if err := s.UpdateTaskStatus(ctx, task, "worker-1", string(models.TaskStatusDone), "", ""); err != nil {
		t.Fatalf("done update: %v", err)
	}
	got = fetchStatusTask(t, s, ctx, task)
	if got.ErrorMessage.String != "" || got.ErrorType.String != "" {
		t.Fatalf("done task carries error residue: type=%q message=%q", got.ErrorType.String, got.ErrorMessage.String)
	}
}

func TestStatusUpdateFailureStillRecordsMessage(t *testing.T) {
	s := newStatusColumnStore(t)
	ctx := context.Background()
	task := createStatusColumnTask(t, s, ctx)

	if err := s.UpdateTaskStatus(ctx, task, "worker-1", string(models.TaskStatusRunning), "Started rule ext-1", ""); err != nil {
		t.Fatal(err)
	}
	// terminal failure: message IS the failure evidence and must be recorded.
	if err := s.UpdateTaskStatus(ctx, task, "worker-1", string(models.TaskStatusFailed), "boom", "exec"); err != nil {
		t.Fatalf("failed update: %v", err)
	}
	got := fetchStatusTask(t, s, ctx, task)
	if got.ErrorMessage.String != "boom" || got.ErrorType.String != "exec" {
		t.Fatalf("failure evidence not recorded: %+v", got)
	}

	// After a retry (RetryTask clears columns) a successful run must end clean.
	if err := s.RetryTask(ctx, task); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := s.ClaimTask(ctx, "worker-2", time.Minute, 5); err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if err := s.UpdateTaskStatus(ctx, task, "worker-2", string(models.TaskStatusRunning), "Started rule ext-1", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTaskStatus(ctx, task, "worker-2", string(models.TaskStatusDone), "", ""); err != nil {
		t.Fatal(err)
	}
	got = fetchStatusTask(t, s, ctx, task)
	if string(got.Status) != string(models.TaskStatusDone) || got.ErrorMessage.String != "" || got.ErrorType.String != "" {
		t.Fatalf("post-retry success not clean: %+v", got)
	}
}

func newStatusColumnStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir()+"/status-columns.db", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func createStatusColumnTask(t *testing.T, s *Store, ctx context.Context) string {
	t.Helper()
	rule := workspaceRule("ext-1", time.Now().UTC())
	rule.Version = "1.0.0"
	rule.Output = executionOutputSchema
	if err := s.CreateRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	var version *models.RuleVersion
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		version, err = s.CreateRuleVersionWithContractTx(ctx, tx, rule, "", executionInputSchema, executionOutputSchema, "profile-a")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveRuleVersion(ctx, rule.ID, version.Version); err != nil {
		t.Fatal(err)
	}
	created := &models.Task{
		ID: "task-status-cols", RuleID: rule.ID, RuleVersion: rule.Version, RuleVersionNumber: version.Version,
		Status: models.TaskStatusPending, Priority: "normal", Variables: models.JSON(`{"query":"demo"}`),
	}
	if err := s.CreateTask(ctx, created); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimTask(ctx, "worker-1", time.Minute, 5)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	return claimed.ID
}

func fetchStatusTask(t *testing.T, s *Store, ctx context.Context, id string) *models.Task {
	t.Helper()
	task, err := s.GetTaskByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

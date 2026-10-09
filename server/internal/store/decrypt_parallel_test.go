package store

import (
	"sync"
	"testing"

	"github.com/singhand-labs/AegisCrawler/internal/crypto"
	"github.com/singhand-labs/AegisCrawler/internal/models"
)

// The serial list path spent ~90ms of PBKDF2 per row; a 20-row page cost
// ~1.8s. decryptVariablesParallel must produce identical results to the
// serial loop (same plaintext per row, first error by row order) while
// spreading the derivations across cores.
func TestDecryptVariablesParallelMatchesSerial(t *testing.T) {
	const key = "list-perf-test-key"
	const rows = 24
	serial := newTestStoreForDecrypt(t, key)
	parallel := newTestStoreForDecrypt(t, key)

	serialTasks := make([]*models.Task, rows)
	parallelTasks := make([]*models.Task, rows)
	for i := 0; i < rows; i++ {
		enc, err := crypto.EncryptVariables(key, map[string]any{"keyword": "k", "n": i})
		if err != nil {
			t.Fatal(err)
		}
		serialTasks[i] = &models.Task{Variables: models.JSON(enc)}
		parallelTasks[i] = &models.Task{Variables: models.JSON(enc)}
	}

	// Serial baseline (mirrors the pre-fix loop).
	for _, task := range serialTasks {
		plain, err := serial.decryptVariables(task.Variables)
		if err != nil {
			t.Fatal(err)
		}
		task.Variables = plain
	}
	if err := parallel.decryptVariablesParallel(parallelTasks); err != nil {
		t.Fatal(err)
	}
	for i := range serialTasks {
		if string(serialTasks[i].Variables) != string(parallelTasks[i].Variables) {
			t.Fatalf("row %d mismatch:\nserial   %s\nparallel %s", i, serialTasks[i].Variables, parallelTasks[i].Variables)
		}
		if len(parallelTasks[i].Variables) == 0 {
			t.Fatalf("row %d variables left empty", i)
		}
	}
}

func TestDecryptVariablesParallelEmptyAndPlain(t *testing.T) {
	// No encryption key configured: pure passthrough for every row.
	plainStore := newTestStoreForDecrypt(t, "")
	tasks := []*models.Task{
		{Variables: models.JSON(`{"a":1}`)},
		{Variables: nil},
	}
	if err := plainStore.decryptVariablesParallel(tasks); err != nil {
		t.Fatal(err)
	}
	if string(tasks[0].Variables) != `{"a":1}` {
		t.Fatalf("plain variables were modified: %s", tasks[0].Variables)
	}
	if err := plainStore.decryptVariablesParallel(nil); err != nil {
		t.Fatal(err)
	}
}

func TestDecryptVariablesParallelPropagatesFirstError(t *testing.T) {
	keyStore := newTestStoreForDecrypt(t, "list-perf-test-key")
	enc, err := crypto.EncryptVariables(keyStore.encryptionKey, map[string]any{"ok": true})
	if err != nil {
		t.Fatal(err)
	}
	tasks := []*models.Task{
		{Variables: models.JSON(enc)},
		{Variables: models.JSON("$pbkdf2$600000$!!!notbase64!!!$abc")},
		{Variables: models.JSON(enc)},
	}
	if err := keyStore.decryptVariablesParallel(tasks); err == nil {
		t.Fatal("expected the malformed row to surface an error")
	}
}

func newTestStoreForDecrypt(t *testing.T, key string) *Store {
	t.Helper()
	s, err := New(t.TempDir()+"/decrypt-perf.db", key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// Guards against accidental data races when rows decrypt concurrently.
func TestDecryptVariablesParallelRace(t *testing.T) {
	const key = "list-perf-race-key"
	s := newTestStoreForDecrypt(t, key)
	var once sync.Once
	once.Do(func() {}) // keep sync imported if rows change
	const rows = 40
	tasks := make([]*models.Task, rows)
	for i := range tasks {
		enc, err := crypto.EncryptVariables(key, map[string]any{"i": i})
		if err != nil {
			t.Fatal(err)
		}
		tasks[i] = &models.Task{Variables: models.JSON(enc)}
	}
	if err := s.decryptVariablesParallel(tasks); err != nil {
		t.Fatal(err)
	}
}

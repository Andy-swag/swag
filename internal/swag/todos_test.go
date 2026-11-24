package swag

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInboxNeedDeduplicatesFeishuReportsAndTracksAck(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWAG_HOME", tmp)

	now := time.Now()
	sp := &Super{
		dir: tmp,
		st: &State{
			Triggers: []*Trigger{
				{ID: "t1", Source: "worker", From: "feishu-todo-1830", Text: "report from feishu-todo-1830: started scan", Status: "done", At: now.Add(-20 * time.Minute)},
				{ID: "t2", Source: "worker", From: "feishu-todo-1830", Text: "report from feishu-todo-1830: final summary", Status: "done", At: now.Add(-10 * time.Minute)},
				{ID: "t3", Source: "worker", From: "worker-a", Text: "report from worker-a: waiting", Status: "queued", At: now.Add(-1 * time.Minute)},
			},
			Workers: map[string]*Worker{},
		},
	}

	reasons := sp.inboxNeedReasonsLocked(now)
	if len(reasons) != 1 {
		t.Fatalf("expected 1 inbox item needing human (latest feishu report only; non-paused queued belongs to Swag), got %d: %v", len(reasons), reasons)
	}
	if _, ok := reasons["t1"]; ok {
		t.Fatalf("older progress report t1 should be superseded by t2")
	}
	if _, ok := reasons["t3"]; ok {
		t.Fatalf("queued trigger t3 should not interrupt human while Swag is running")
	}
	if reasons["t2"] == "" {
		t.Fatalf("expected t2 to be flagged, got %v", reasons)
	}

	sp.st.Settings.Paused = true
	if pausedReasons := sp.inboxNeedReasonsLocked(now); len(pausedReasons) != 2 || pausedReasons["t3"] == "" {
		t.Fatalf("expected queued trigger t3 to surface when paused, got %v", pausedReasons)
	}
	sp.st.Settings.Paused = false

	if err := saveInboxAck(map[string]bool{"t2": true}); err != nil {
		t.Fatal(err)
	}
	if got := sp.inboxNeedReasonsLocked(now); len(got) != 0 {
		t.Fatalf("expected 0 after ack, got %v", got)
	}
}

func TestTodoOpenCountIncludesTasksAndTodos(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWAG_HOME", tmp)

	sp := &Super{dir: tmp, st: &State{}}
	tasksDir := filepath.Join(sp.memDir(), "tasks")
	doneDir := filepath.Join(tasksDir, "done")
	if err := os.MkdirAll(doneDir, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(tasksDir, "active.md"), []byte("# Active\n"), 0o600)
	_ = os.WriteFile(filepath.Join(doneDir, "old.md"), []byte("# Old\n"), 0o600)
	_ = saveTodos([]TodoItem{
		{ID: "td-1", Title: "Open item", Status: "open"},
		{ID: "td-2", Title: "Done item", Status: "done"},
	})

	if got := sp.todoOpenCountLocked(time.Now()); got != 2 {
		t.Fatalf("expected 2 open todos (1 todo + 1 active task), got %d", got)
	}
}

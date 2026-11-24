package swag

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
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

func TestTodoBatchAndMessageRecall(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWAG_HOME", tmp)

	sp := &Super{dir: tmp, st: &State{}, subs: map[chan struct{}]bool{}}
	s := &Server{sup: sp}

	_ = saveTodos([]TodoItem{
		{ID: "td-a", Title: "A", Status: "open"},
		{ID: "td-b", Title: "B", Status: "open"},
		{ID: "td-c", Title: "C", Status: "open"},
	})

	req := httptest.NewRequest("POST", "/api/super/todos/batch", bytes.NewReader([]byte(`{"ids":["td-a","td-b"],"action":"done"}`)))
	rec := httptest.NewRecorder()
	s.superTodoBatch(rec, req)
	if rec.Code != 200 {
		t.Fatalf("batch done failed: %d %s", rec.Code, rec.Body.String())
	}
	got := loadTodos()
	doneCount := 0
	for _, it := range got {
		if it.Status == "done" {
			doneCount++
		}
	}
	if doneCount != 2 {
		t.Fatalf("expected 2 done todos, got %d", doneCount)
	}

	req2 := httptest.NewRequest("POST", "/api/super/todos/batch", bytes.NewReader([]byte(`{"ids":["td-a","td-b"],"action":"delete"}`)))
	rec2 := httptest.NewRecorder()
	s.superTodoBatch(rec2, req2)
	if rec2.Code != 200 || len(loadTodos()) != 1 {
		t.Fatalf("batch delete failed: code=%d remaining=%d", rec2.Code, len(loadTodos()))
	}

	// Recall a queued user message: entry removed + queued trigger cancelled.
	sp.st.Entries = []*Entry{{ID: "e10", Role: "user", Thread: mainThread, Text: "misclicked", At: time.Now()}}
	sp.st.Triggers = []*Trigger{{ID: "t10", Source: "chat", Ref: "e10", Status: "queued", At: time.Now()}}
	req3 := httptest.NewRequest("POST", "/api/super/recall", bytes.NewReader([]byte(`{"id":"e10"}`)))
	rec3 := httptest.NewRecorder()
	s.superRecall(rec3, req3)
	if rec3.Code != 200 {
		t.Fatalf("recall failed: %d %s", rec3.Code, rec3.Body.String())
	}
	var res map[string]any
	_ = json.Unmarshal(rec3.Body.Bytes(), &res)
	if res["cancelled_queued"] != true || len(sp.st.Entries) != 0 || sp.st.Triggers[0].Status != "ignored" {
		t.Fatalf("unexpected recall state: res=%v entries=%d trig=%+v", res, len(sp.st.Entries), sp.st.Triggers[0])
	}
}

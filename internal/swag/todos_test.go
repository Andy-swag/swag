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

func TestDreamJournalParsesScriptsAndSummary(t *testing.T) {
	p := filepath.Join(t.TempDir(), "2026-11-25-0400.md")
	content := "# Dream\n更新了避坑记录并清理了 2 条过时主机信息。\n可做成 skill：日志自动归因\n可固化脚本/流水线：夜间巡检脚本（scripts/night-check.sh）\n- 详情\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dreamSummary(p); got != "更新了避坑记录并清理了 2 条过时主机信息。" {
		t.Fatalf("unexpected summary: %q", got)
	}
	if got := dreamLine(p, dreamScriptsPrefix); got != "夜间巡检脚本（scripts/night-check.sh）" {
		t.Fatalf("unexpected scripts line: %q", got)
	}
}

func TestDreamFailureBackoffPreventsAlertStorm(t *testing.T) {
	dreamRecordSuccess()
	t.Cleanup(dreamRecordSuccess)

	base := time.Date(2026, 11, 25, 4, 0, 0, 0, time.Local)
	if !dreamCanTry(base) {
		t.Fatalf("expected dreamCanTry true initially")
	}
	sup1, give1 := dreamRecordFailure(base, true)
	if sup1 || give1 {
		t.Fatalf("1st failure should alert once and retry later, got suppress=%v giveUp=%v", sup1, give1)
	}
	if dreamCanTry(base.Add(5 * time.Minute)) {
		t.Fatalf("expected cooldown to block retry 5m after 1st failure")
	}
	if !dreamCanTry(base.Add(16 * time.Minute)) {
		t.Fatalf("expected retry allowed after 15m cooldown")
	}
	sup2, give2 := dreamRecordFailure(base.Add(16*time.Minute), true)
	if !sup2 || give2 {
		t.Fatalf("2nd failure should suppress repeat alert and back off, got suppress=%v giveUp=%v", sup2, give2)
	}
	sup3, give3 := dreamRecordFailure(base.Add(70*time.Minute), true)
	if !sup3 || !give3 {
		t.Fatalf("3rd failure should suppress alert and give up tonight, got suppress=%v giveUp=%v", sup3, give3)
	}
}

func TestDreamProposalsAutoCreateTodos(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWAG_HOME", tmp)

	n := addDreamProposalTodos(
		"2026-11-25-1041",
		"SQL 巡检",
		"mipp（START 后立即通知）、harness（真卡预检）",
		"单卡预检（scripts/bench.sh）",
	)
	if n != 4 {
		t.Fatalf("expected 4 proposal todos, got %d", n)
	}
	// Running again deduplicates by source + title.
	addDreamProposalTodos("2026-11-25-1041", "SQL 巡检", "mipp（START 后立即通知）", "")
	got := loadTodos()
	if len(got) != 4 {
		t.Fatalf("expected 4 deduplicated todos, got %d: %+v", len(got), got)
	}
	wantTitles := map[string]bool{
		"沉淀 Skill：SQL 巡检": true,
		"改进 Skill：mipp":    true,
		"改进 Skill：harness": true,
		"固化脚本/流水线：单卡预检":    true,
	}
	for _, it := range got {
		if it.Source != "agent" || !wantTitles[it.Title] {
			t.Fatalf("unexpected todo item: %+v", it)
		}
	}
}

func TestNodeRouterMemDirPrefersDreamWork(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWAG_HOME", tmp)

	if got := nodeRouterMemDir(); got != filepath.Join(tmp, "router", "memory") {
		t.Fatalf("expected fallback to router/memory, got %s", got)
	}
	dw := filepath.Join(tmp, dreamWorkSubdir, "memory")
	if err := os.MkdirAll(dw, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := nodeRouterMemDir(); got != dw {
		t.Fatalf("expected dream-work/memory when present, got %s", got)
	}
}

func TestTodoDispatchedLifecycleAndNoDoubleCount(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWAG_HOME", tmp)

	sp := &Super{dir: tmp, st: &State{}, subs: map[chan struct{}]bool{}}
	s := &Server{sup: sp}

	_ = saveTodos([]TodoItem{
		{ID: "td-1", Title: "固化脚本/流水线：预检", Source: "agent", Status: "open"},
		{ID: "td-2", Title: "改进 Skill：mipp", Source: "agent", Status: "open"},
	})
	if got := sp.todoOpenCountLocked(time.Now()); got != 2 {
		t.Fatalf("expected 2 open initially, got %d", got)
	}

	// Dispatch td-1: leaves open count, sets DispatchedAt, and no longer double-counts with live task.
	req := httptest.NewRequest("POST", "/api/super/todos/batch", bytes.NewReader([]byte(`{"ids":["td-1"],"action":"dispatched"}`)))
	rec := httptest.NewRecorder()
	s.superTodoBatch(rec, req)
	if rec.Code != 200 {
		t.Fatalf("batch dispatched failed: %d %s", rec.Code, rec.Body.String())
	}
	if got := sp.todoOpenCountLocked(time.Now()); got != 1 {
		t.Fatalf("expected 1 open after dispatching td-1, got %d", got)
	}
	list := loadTodos()
	if list[0].Status != "dispatched" || list[0].DispatchedAt.IsZero() {
		t.Fatalf("expected td-1 dispatched with timestamp, got %+v", list[0])
	}

	// Upserting same proposal title again must not create a duplicate open todo while dispatched.
	upsertTodoInternal("固化脚本/流水线：预检", "updated note", "agent", "medium", "", "")
	if got := loadTodos(); len(got) != 2 || got[0].Status != "dispatched" {
		t.Fatalf("expected dispatched todo to stay deduplicated, got %+v", got)
	}

	// Swag finishing closes it via POST /api/super/todos/{id} status=done.
	reqDone := httptest.NewRequest("POST", "/api/super/todos/td-1", bytes.NewReader([]byte(`{"status":"done"}`)))
	reqDone.SetPathValue("id", "td-1")
	recDone := httptest.NewRecorder()
	s.superTodoAction(recDone, reqDone)
	if recDone.Code != 200 {
		t.Fatalf("todo done failed: %d %s", recDone.Code, recDone.Body.String())
	}
	if got := loadTodos()[0].Status; got != "done" {
		t.Fatalf("expected td-1 done, got %q", got)
	}
}

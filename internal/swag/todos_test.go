package swag

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInboxNeedSkipsRoutineFeishuReportsAndTracksRealAlerts(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWAG_HOME", tmp)

	now := time.Now()
	sp := &Super{
		dir: tmp,
		st: &State{
			Triggers: []*Trigger{
				{ID: "t1", Source: "worker", From: "feishu-todo-1830", Text: "report from feishu-todo-1830: 新增 0 条", Status: "done", At: now.Add(-10 * time.Minute)},
				{ID: "t2", Source: "webhook:github", From: "github", Text: "PR #12 opened", Status: "done", Action: "candidate", At: now.Add(-5 * time.Minute)},
				{ID: "t3", Source: "worker", From: "worker-a", Text: "report from worker-a: waiting", Status: "queued", At: now.Add(-1 * time.Minute)},
			},
			Workers: map[string]*Worker{},
		},
	}

	reasons := sp.inboxNeedReasonsLocked(now)
	if len(reasons) != 1 || reasons["t2"] == "" {
		t.Fatalf("expected only candidate t2 in inbox needs (routine feishu worker reports belong in Todos/Processed), got %v", reasons)
	}
	if _, ok := reasons["t1"]; ok {
		t.Fatalf("routine feishu worker report t1 should not alert Inbox")
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
		"deploy-flow（启动后立即通知、补状态核验）、harness（真卡预检）",
		"单卡预检（scripts/bench.sh）；镜像状态核对（scripts/check.sh）",
		"项目 Wiki（补最新架构图）",
	)
	if n != 6 {
		t.Fatalf("expected 6 proposal todos, got %d", n)
	}
	// Running again deduplicates by source + title.
	addDreamProposalTodos("2026-11-25-1041", "SQL 巡检", "deploy-flow（启动后立即通知）", "", "")
	got := loadTodos()
	if len(got) != 6 {
		t.Fatalf("expected 6 deduplicated todos, got %d: %+v", len(got), got)
	}
	wantTitles := map[string]bool{
		"沉淀 Skill：SQL 巡检":     true,
		"改进 Skill：deploy-flow": true,
		"改进 Skill：harness":     true,
		"固化脚本/流水线：单卡预检":        true,
		"固化脚本/流水线：镜像状态核对":      true,
		"更新文档/Wiki：项目 Wiki":    true,
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
		{ID: "td-2", Title: "改进 Skill：deploy-flow", Source: "agent", Status: "open"},
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

func TestIdleWorkerReaperSafetyAndManualReap(t *testing.T) {
	now := time.Now()
	sp := &Super{
		subs: map[chan struct{}]bool{},
		st: &State{
			Workers: map[string]*Worker{
				"old-done":   {Name: "old-done", Status: "done", Thread: mainThread, IdleSince: now.Add(-75 * time.Minute)},
				"fresh-done": {Name: "fresh-done", Status: "done", Thread: mainThread, IdleSince: now.Add(-15 * time.Minute)},
				"busy":       {Name: "busy", Status: "working", Thread: mainThread, CreatedAt: now.Add(-2 * time.Hour)},
				"blocked":    {Name: "blocked", Status: "blocked", Thread: mainThread, IdleSince: now.Add(-2 * time.Hour)},
				"subbed":     {Name: "subbed", Status: "done", Thread: "gh:pr#1", IdleSince: now.Add(-2 * time.Hour)},
				"queued":     {Name: "queued", Status: "done", Thread: mainThread, IdleSince: now.Add(-2 * time.Hour)},
				"timed":      {Name: "timed", Status: "done", Thread: mainThread, IdleSince: now.Add(-2 * time.Hour)},
			},
			Threads: map[string]*Thread{
				"gh:pr#1": {ID: "gh:pr#1", Subscribers: []string{"subbed"}},
			},
			Triggers: []*Trigger{
				{ID: "t1", Status: "queued", Targets: []string{"queued"}},
			},
			Timers: []*Timer{
				{ID: "m1", Enabled: true, Target: "timed"},
			},
		},
	}

	autoReaped := sp.reapIdleWorkers(t.Context(), now, idleWorkerReapAfter, "自动归档")
	if len(autoReaped) != 1 || autoReaped[0] != "old-done" {
		t.Fatalf("expected only old-done auto-reaped, got %v", autoReaped)
	}
	if !sp.st.Workers["old-done"].Ended || sp.st.Workers["old-done"].Status != "gone" {
		t.Fatalf("expected old-done marked ended/gone, got %+v", sp.st.Workers["old-done"])
	}
	if sp.st.Workers["fresh-done"].Ended {
		t.Fatalf("fresh-done should not be auto-reaped before 60m")
	}

	// Manual reap endpoint immediately archives fresh-done while protecting busy/blocked/subbed/queued/timed.
	s := &Server{sup: sp}
	req := httptest.NewRequest("POST", "/api/super/workers/reap", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	s.superWorkersReap(rec, req)
	if rec.Code != 200 {
		t.Fatalf("manual reap failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		OK     bool     `json:"ok"`
		Reaped []string `json:"reaped"`
		Count  int      `json:"count"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.OK || resp.Count != 1 || len(resp.Reaped) != 1 || resp.Reaped[0] != "fresh-done" {
		t.Fatalf("expected manual reap to archive only fresh-done, got %+v", resp)
	}
	for _, keep := range []string{"busy", "blocked", "subbed", "queued", "timed"} {
		if sp.st.Workers[keep].Ended {
			t.Fatalf("protected worker %s must not be reaped", keep)
		}
	}
}

func TestCodexUsagePerTurnModelAndWindowAttribution(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-multi.jsonl")
	day1 := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	lines := []string{
		`{"timestamp":"` + day1.Format(time.RFC3339) + `","type":"turn_context","payload":{"model":"gpt-6.1-sol"}}`,
		`{"timestamp":"` + day1.Add(time.Minute).Format(time.RFC3339) + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":900,"cached_input_tokens":100,"output_tokens":100,"total_tokens":1000}}}}`,
		`{"timestamp":"` + day1.Add(2*time.Minute).Format(time.RFC3339) + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":900,"cached_input_tokens":100,"output_tokens":100,"total_tokens":1000}}}}`,
		`{"timestamp":"` + day2.Format(time.RFC3339) + `","type":"turn_context","payload":{"model":"gpt-6.2-sol"}}`,
		`{"timestamp":"` + day2.Add(time.Minute).Format(time.RFC3339) + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1150,"cached_input_tokens":200,"output_tokens":150,"total_tokens":1300}}}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 1-day window ending on day2 must ONLY include day2's 300 delta under gpt-6.2-sol.
	got1d := scanCodexRolloutBuckets(p, day2.Add(-time.Hour), "2026-11-25")
	if len(got1d) != 1 || got1d[0].model != "gpt-6.2-sol" || got1d[0].tok.Total != 300 || got1d[0].tok.Input != 250 || got1d[0].tok.Output != 50 {
		t.Fatalf("unexpected 1d buckets: %+v", got1d)
	}
	// Full history splits cleanly across both models without double counting duplicate heartbeat.
	gotAll := scanCodexRolloutBuckets(p, time.Time{}, "2026-11-25")
	if len(gotAll) != 2 || gotAll[0].model != "gpt-6.1-sol" || gotAll[0].tok.Total != 1000 || gotAll[1].model != "gpt-6.2-sol" || gotAll[1].tok.Total != 300 {
		t.Fatalf("unexpected full buckets: %+v", gotAll)
	}
}

func TestDreamFailureStreakResetsNextDay(t *testing.T) {
	dreamRecordSuccess()
	t.Cleanup(dreamRecordSuccess)

	night1 := time.Date(2026, 11, 25, 4, 0, 0, 0, time.Local)
	dreamRecordFailure(night1, true)
	dreamRecordFailure(night1.Add(20*time.Minute), true)
	_, give1 := dreamRecordFailure(night1.Add(70*time.Minute), true)
	if !give1 {
		t.Fatalf("expected giveUp on 3rd failure of night1")
	}

	night2 := night1.Add(24 * time.Hour)
	sup2, give2 := dreamRecordFailure(night2, true)
	if sup2 || give2 {
		t.Fatalf("expected fresh streak on night2 1st failure, got suppress=%v giveUp=%v", sup2, give2)
	}
}

func TestPoolLastProviderCooldownEnablesNativeFallback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWAG_HOME", tmp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
	}))
	defer srv.Close()

	cfg := PoolConfig{
		Providers: []PoolProvider{{ID: "sole", BaseURL: srv.URL, Enabled: true, Priority: 10, CooldownSec: 180}},
		Tiers:     []ModelTier{{ID: "tier-fast", Providers: []string{"sole"}}},
	}
	if err := savePoolConfig(cfg); err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader([]byte(`{"model":"tier-fast","input":"hi"}`)))
	rec := httptest.NewRecorder()
	s.proxyPoolRequest(rec, req, "/responses")
	if rec.Code != 429 {
		t.Fatalf("expected 429 from exhausted pool, got %d: %s", rec.Code, rec.Body.String())
	}
	if poolHasReadyProvider(cfg, "tier-fast") {
		t.Fatalf("expected sole 429 provider to enter cooldown so native fallback can trigger")
	}
}

func TestSuperSayDeduplicatesRapidDoubleSubmit(t *testing.T) {
	sp := &Super{subs: map[chan struct{}]bool{}, st: &State{Threads: map[string]*Thread{}}}
	s := &Server{sup: sp}

	body := []byte(`{"text":"每周四晚上复盘本周所有工作"}`)
	req1 := httptest.NewRequest("POST", "/api/super/say", bytes.NewReader(body))
	rec1 := httptest.NewRecorder()
	s.superSay(rec1, req1)
	if rec1.Code != 200 {
		t.Fatalf("first say failed: %d %s", rec1.Code, rec1.Body.String())
	}

	req2 := httptest.NewRequest("POST", "/api/super/say", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	s.superSay(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("second say failed: %d %s", rec2.Code, rec2.Body.String())
	}
	if len(sp.st.Entries) != 1 || len(sp.st.Triggers) != 1 {
		t.Fatalf("expected rapid double-submit deduplicated to 1 entry & 1 trigger, got entries=%d triggers=%d", len(sp.st.Entries), len(sp.st.Triggers))
	}
	if !strings.Contains(rec2.Body.String(), `"deduped":true`) {
		t.Fatalf("expected deduped response on 2nd submit, got %s", rec2.Body.String())
	}
}

func TestCollectDeliverablesListsDoneTasksAndIgnoresExternalInputLinks(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWAG_HOME", tmp)

	sp := &Super{
		dir: tmp,
		st: &State{
			Entries: []*Entry{
				{ID: "e1", Role: "swag", At: time.Now(), Text: "已完成交付：[模型推理接入复盘](https://example.feishu.cn/docx/DocABC123?from=chat)，参考外部 wiki https://example.feishu.cn/wiki/ExternalRef999"},
				{ID: "e2", Role: "swag", At: time.Now().Add(time.Minute), Text: "复盘文档：https://example.feishu.cn/docx/DocABC123"},
			},
		},
	}
	doneDir := filepath.Join(sp.memDir(), "tasks", "done")
	if err := os.MkdirAll(doneDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Two finished tasks sharing ONE review doc URL -> merged into 1 card with both slugs in RelatedTasks.
	_ = os.WriteFile(filepath.Join(doneDir, "bench.md"), []byte("# GPU 单卡预检（进行中）\n- 已完成全部回归\n- 复盘文档：[GPU 单卡预检复盘](https://example.feishu.cn/docx/DocBench777)\n"), 0o600)
	_ = os.WriteFile(filepath.Join(doneDir, "bench-fix.md"), []byte("# GPU 预检补丁\n- 补齐校验脚本\n- 复盘文档：[GPU 单卡预检复盘](https://example.feishu.cn/docx/DocBench777)\n"), 0o600)
	// Finished task with ONLY an external input reference doc -> must surface as missing review doc (HasDoc=false, URL="").
	_ = os.WriteFile(filepath.Join(doneDir, "config-update.md"), []byte("# 模型配置更新\n- 目标：依照飞书文档 https://example.feishu.cn/docx/ExternalInput111 更新配置\n- 已完成配置回读验收。\n"), 0o600)
	// Weekly rollup index note -> surfaces first as 周总复盘.
	notesDir := filepath.Join(sp.memDir(), "notes")
	if err := os.MkdirAll(notesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(notesDir, "weekly-reviews.md"), []byte("# 周总复盘归档\n- 周总复盘：[本周工作总复盘](https://example.feishu.cn/docx/Weekly001)\n"), 0o600)
	// Routine scheduled scan task -> must be skipped.
	_ = os.WriteFile(filepath.Join(doneDir, "feishu-todo-1126-am.md"), []byte("# 早间盘点\n- 无新增\n"), 0o600)

	got := sp.collectDeliverables()
	if len(got) != 4 {
		t.Fatalf("expected 4 items (1 weekly rollup + 1 merged linked task + 1 missing-doc task + 1 standalone chat review doc), got %d: %+v", len(got), got)
	}
	if got[0].DocType != "周总复盘" || got[0].URL != "https://example.feishu.cn/docx/Weekly001" {
		t.Fatalf("expected weekly rollup ranked first, got %+v", got[0])
	}
	byKey := map[string]DeliverableItem{}
	for _, d := range got {
		k := d.URL
		if k == "" {
			k = d.TaskFile
		}
		byKey[k] = d
	}
	if d := byKey["https://example.feishu.cn/docx/DocBench777"]; !d.HasDoc || len(d.RelatedTasks) != 2 {
		t.Fatalf("expected bench + bench-fix merged into 1 card with 2 related tasks, got %+v", d)
	}
	if d := byKey["tasks/done/config-update.md"]; d.HasDoc || d.URL != "" || d.Scope != "missing" || d.Summary != "已完成配置回读验收。" {
		t.Fatalf("expected config-update to ignore external input doc and surface as missing review doc, got %+v", d)
	}
	if d := byKey["https://example.feishu.cn/docx/DocABC123"]; !d.HasDoc || d.Title != "模型推理接入复盘" {
		t.Fatalf("unexpected standalone chat review doc: %+v", d)
	}
	for _, d := range got {
		if strings.Contains(d.URL, "ExternalRef999") || strings.Contains(d.URL, "ExternalInput111") {
			t.Fatalf("external reference doc leaked into deliverables: %+v", d)
		}
	}

	// Mark config-update as rolled into weekly review via API.
	srv := &Server{sup: sp}
	req := httptest.NewRequest("POST", "/api/super/deliverables/scope", bytes.NewReader([]byte(`{"task_file":"tasks/done/config-update.md","scope":"weekly"}`)))
	rec := httptest.NewRecorder()
	srv.superDeliverableScope(rec, req)
	if rec.Code != 200 {
		t.Fatalf("scope update failed: %d %s", rec.Code, rec.Body.String())
	}
	after := sp.collectDeliverables()
	foundWeekly := false
	for _, d := range after {
		if d.TaskFile == "tasks/done/config-update.md" && d.Scope == "weekly" && d.DocType == "已归入周复盘" {
			foundWeekly = true
		}
	}
	if !foundWeekly {
		t.Fatalf("expected config-update to transition to weekly scope, got %+v", after)
	}
}

func TestFinishDreamQueuesInteractiveCardInQuietMode(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWAG_HOME", tmp)

	sp := &Super{
		dir:  tmp,
		subs: map[chan struct{}]bool{},
		st: &State{
			Settings: Settings{NotifyLark: true, NotifyLevel: "quiet"},
			Threads:  map[string]*Thread{},
		},
	}
	at := time.Date(2026, 11, 26, 4, 0, 0, 0, time.Local)
	dreamsDir := filepath.Join(sp.routerDir(), "dreams")
	if err := os.MkdirAll(dreamsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(dreamsDir, journalName(at)+".md")
	body := strings.Join([]string{
		"沉淀了最新验收与排障经验，压缩启动记忆至 2.8 KB。",
		"可做成 skill：发布巡检",
		"可改进 skill：deploy-flow（补全局门禁）",
		"可固化脚本/流水线：双schema验收（scripts/verify.py）",
		"可更新文档/Wiki：架构说明（补最新数据流）",
		"",
		"## 详情",
	}, "\n")
	if err := os.WriteFile(journal, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// Directly exercise journal parsing & notification queueing in quiet mode.
	summary := dreamSummary(journal)
	skills := dreamLine(journal, dreamSkillsPrefix)
	fixes := dreamLine(journal, dreamFixPrefix)
	scripts := dreamLine(journal, dreamScriptsPrefix)
	wikis := dreamLine(journal, dreamWikiPrefix)
	if summary == "" || skills == "" || fixes == "" || scripts == "" || wikis == "" {
		t.Fatalf("expected all 5 dream fields parsed: sum=%q sk=%q fx=%q sc=%q wk=%q", summary, skills, fixes, scripts, wikis)
	}
	sp.mu.Lock()
	sp.addEntryReplyLocked("swag", mainThread, "整理记忆完成（04:00）："+summary, "", "", nil, nil, true)
	sp.addEntryReplyLocked("swag", mainThread, skills, "", "", nil, []string{"做成个人 skill", "放到项目仓库", "先不用"}, false)
	sp.addEntryReplyLocked("swag", mainThread, fixes, "", "", nil, []string{"派人去修", "先不用"}, false)
	sp.addEntryReplyLocked("swag", mainThread, scripts, "", "", nil, []string{"派人固化", "先不用"}, false)
	sp.addEntryReplyLocked("swag", mainThread, wikis, "", "", nil, []string{"派人更新", "先不用"}, false)
	gotBuf := append([]notice(nil), sp.notifyBuf...)
	sp.mu.Unlock()

	if len(gotBuf) != 5 {
		t.Fatalf("expected 5 coalesced notices in quiet mode (summary + 4 interactive proposals), got %d", len(gotBuf))
	}
	for i, n := range gotBuf {
		if !n.needsYou() {
			t.Fatalf("notice %d should trigger Feishu Card 2.0 in quiet mode: %+v", i, n)
		}
	}
}

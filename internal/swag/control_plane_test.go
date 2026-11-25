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

func TestMachinePolicyDefaults(t *testing.T) {
	st := defaultState("/tmp")
	hubPol := st.Settings.PolicyFor("", true)
	if !hubPol.DispatchEnabled || hubPol.Group != "hub" {
		t.Fatalf("expected hub to be dispatch-enabled in group hub, got %+v", hubPol)
	}
	newNodePol := st.Settings.PolicyFor("node-xyz", false)
	if newNodePol.DispatchEnabled || newNodePol.Group != "observe" {
		t.Fatalf("expected new remote node to default to observe-only, got %+v", newNodePol)
	}
	st.Settings.MachinePolicies["node-xyz"] = MachinePolicy{DispatchEnabled: true, Group: "compute", DreamInclude: true}
	enabledPol := st.Settings.PolicyFor("node-xyz", false)
	if !enabledPol.DispatchEnabled || enabledPol.Group != "compute" || !enabledPol.DreamInclude {
		t.Fatalf("expected custom policy, got %+v", enabledPol)
	}
}

func TestBuildEnvExportCmd(t *testing.T) {
	cmd := buildEnvExportCmd(map[string]string{
		"OPENAI_API_KEY": "sk-test'123",
		"OPENAI_BASE_URL": "https://example.com/v1",
		"bad-key":         "ignored",
	})
	if !strings.HasPrefix(cmd, " export ") {
		t.Fatalf("expected leading space for history exemption, got %q", cmd)
	}
	if strings.Contains(cmd, "bad-key") {
		t.Fatalf("invalid env key should be ignored: %q", cmd)
	}
	if !strings.Contains(cmd, `OPENAI_API_KEY='sk-test'\''123'`) {
		t.Fatalf("single quote escaping broken: %q", cmd)
	}
}

func TestDirMirrorSyncAndPrune(t *testing.T) {
	tmp := t.TempDir()
	files := map[string]string{
		"instructions.md": "# Rules\n",
		"notes/index.md":  "# Index\n",
		"../escape.txt":   "nope",
	}
	if err := applyDirMirror(tmp, files, true); err != nil {
		t.Fatalf("applyDirMirror: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "..", "escape.txt")); err == nil {
		t.Fatalf("path traversal should have been rejected")
	}
	got := collectDirFiles(tmp, true)
	if got["instructions.md"] != "# Rules\n" || got["notes/index.md"] != "# Index\n" {
		t.Fatalf("unexpected mirrored files: %+v", got)
	}
	// Prune stale file on next mirror
	if err := applyDirMirror(tmp, map[string]string{"instructions.md": "# Updated\n"}, true); err != nil {
		t.Fatalf("applyDirMirror prune: %v", err)
	}
	got2 := collectDirFiles(tmp, true)
	if _, exists := got2["notes/index.md"]; exists {
		t.Fatalf("stale file should have been pruned")
	}
	if got2["instructions.md"] != "# Updated\n" {
		t.Fatalf("expected updated content, got %q", got2["instructions.md"])
	}
}

func TestScanCodexAndClaudeUsage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))

	cdir := filepath.Join(home, ".codex", "sessions", "2026", "11", "24")
	os.MkdirAll(cdir, 0o755)
	os.WriteFile(filepath.Join(cdir, "rollout-1.jsonl"), []byte(
		`{"type":"turn_context","payload":{"model":"gpt-6.2-sol"}}`+"\n"+
			`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":400,"output_tokens":200,"reasoning_output_tokens":50,"total_tokens":1200}}}}`+"\n",
	), 0o600)

	cldir := filepath.Join(home, ".claude", "projects", "proj1")
	os.MkdirAll(cldir, 0o755)
	os.WriteFile(filepath.Join(cldir, "s1.jsonl"), []byte(
		`{"message":{"id":"m1","model":"claude-sonnet-4-6","usage":{"input_tokens":100,"cache_creation_input_tokens":200,"cache_read_input_tokens":300,"output_tokens":50}}}`+"\n",
	), 0o600)

	u := scanLocalUsage(home, 7)
	if u.Total.Total != 1850 || u.Total.Cached != 700 || u.Total.Sessions != 2 {
		t.Fatalf("unexpected usage summary: %+v", u.Total)
	}
	if u.ByModel["gpt-6.2-sol"].Total != 1200 || u.ByModel["claude-sonnet-4-6"].Total != 650 {
		t.Fatalf("unexpected model breakdown: %+v", u.ByModel)
	}
}

func TestPoolFailoverAndCooldown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	poolMu.Lock()
	poolRuntime = map[string]*ProviderRuntime{}
	poolMu.Unlock()

	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":"quota exceeded"}`))
	}))
	defer srvA.Close()

	var gotModel string
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotModel, _ = body["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"resp_ok","status":"completed"}`))
	}))
	defer srvB.Close()

	cfg := PoolConfig{
		Providers: []PoolProvider{
			{ID: "p-a", Name: "A", Kind: "openai", BaseURL: srvA.URL, Enabled: true, Priority: 1, CooldownSec: 120, ModelMap: map[string]string{"tier-strong": "model-a"}},
			{ID: "p-b", Name: "B", Kind: "openai", BaseURL: srvB.URL, Enabled: true, Priority: 2, CooldownSec: 120, ModelMap: map[string]string{"tier-strong": "model-b"}},
		},
		Tiers: []ModelTier{{ID: "tier-strong", Label: "Strong", Providers: []string{"p-a", "p-b"}}},
	}
	if err := savePoolConfig(cfg); err != nil {
		t.Fatalf("savePoolConfig: %v", err)
	}

	s := &Server{}
	req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader([]byte(`{"model":"tier-strong","input":"hi"}`)))
	rec := httptest.NewRecorder()
	s.poolResponses(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected failover 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotModel != "model-b" {
		t.Fatalf("expected mapped model-b, got %q", gotModel)
	}
	if rec.Header().Get("X-Swag-Provider") != "p-b" {
		t.Fatalf("expected X-Swag-Provider=p-b, got %q", rec.Header().Get("X-Swag-Provider"))
	}
	stats := poolStatsSnapshot()
	if stats["p-a"].Failovers != 1 || stats["p-b"].Successes != 1 {
		t.Fatalf("unexpected pool runtime stats: %+v", stats)
	}
}

func TestParseCodexLatestQuota(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	cdir := filepath.Join(home, ".codex", "sessions", "2026", "11", "24")
	os.MkdirAll(cdir, 0o755)
	os.WriteFile(filepath.Join(cdir, "rollout-q.jsonl"), []byte(
		`{"type":"turn_context","payload":{"model":"gpt-6.2-sol"}}`+"\n"+
			`{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"plan_type":"prolite","primary":{"used_percent":10.0,"window_minutes":10080,"resets_at":1796005511}}}}`+"\n"+
			`{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"plan_type":"prolite","primary":{"used_percent":17.0,"window_minutes":10080,"resets_at":1796005511},"credits":{"balance":"28245.38"}}}}`+"\n",
	), 0o600)
	q := parseCodexLatestQuota(home)
	if q == nil || q.Plan != "prolite" || len(q.Windows) != 1 || q.Windows[0].RemainPct != 83 || q.Credits != "28245" {
		t.Fatalf("unexpected quota parsed: %+v", q)
	}
	if len(q.Models) != 1 || q.Models[0] != "gpt-6.2-sol" {
		t.Fatalf("expected model gpt-6.2-sol, got %+v", q.Models)
	}
}

func TestMergeSubscriptionQuotas(t *testing.T) {
	t1 := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(2 * time.Hour)
	merged := mergeSubscriptionQuotas([]SubscriptionQuota{
		{AccountKey: "codex:acct-1", AccountLabel: "dev***@example.com", Machine: "mac", Kind: "codex", Title: "Codex · GPT 订阅", Plan: "prolite", Status: "ready", Sessions7d: 10, LastUsedAt: t1, Models: []string{"gpt-6.1-sol"}, Windows: []QuotaWindow{{Label: "GPT 7D", RemainPct: 90}}},
		{AccountKey: "codex:acct-1", Machine: "dev-node-1", Kind: "codex", Title: "Codex · GPT 订阅", Status: "ready", Sessions7d: 25, LastUsedAt: t2, Models: []string{"gpt-6.2-sol"}, Windows: []QuotaWindow{{Label: "GPT 7D", RemainPct: 83}}, Credits: "28245"},
		{AccountKey: "codex:acct-1", Machine: "hub-node", Kind: "codex", Title: "Codex · GPT 订阅", Status: "ready", Sessions7d: 3, LastUsedAt: t1},
	})
	if len(merged) != 1 {
		t.Fatalf("expected 1 deduplicated subscription card, got %d", len(merged))
	}
	got := merged[0]
	if len(got.Machines) != 3 || got.Sessions7d != 38 || got.AccountLabel != "dev***@example.com" || got.Windows[0].RemainPct != 83 || got.Credits != "28245" || len(got.Models) != 2 {
		t.Fatalf("unexpected merged subscription: %+v", got)
	}
}

func TestParseProxyUsageOutput(t *testing.T) {
	sample := "Account quota (pack weekly_demo_user, resets 2026-11-29 23:59)\n  basic            30 / 300  (10%, 270 left)\n  advanced         0 used (unlimited)\n"
	snap := parseProxyUsageOutput(sample)
	if snap == nil || snap.RemainPct == nil || int(*snap.RemainPct) != 90 {
		t.Fatalf("unexpected proxy usage snapshot: %+v", snap)
	}
	if !strings.Contains(snap.Summary, "basic 270/300") || !strings.Contains(snap.Summary, "advanced 不限") || !strings.Contains(snap.Summary, "11-29 23:59 重置") {
		t.Fatalf("unexpected proxy usage summary: %q", snap.Summary)
	}
}

func TestPoolNativeFallbackHelpers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	poolMu.Lock()
	poolRuntime = map[string]*ProviderRuntime{}
	poolMu.Unlock()

	if !isPoolAuthProfile("swag-pool") || isPoolAuthProfile("default") || isPoolAuthProfile("") {
		t.Fatalf("unexpected isPoolAuthProfile result")
	}
	cfg := PoolConfig{
		Providers: []PoolProvider{{ID: "primary-gw", BaseURL: "https://api.example.com/v1", Enabled: true}},
		Tiers:     []ModelTier{{ID: "tier-fast", Providers: []string{"primary-gw"}}},
	}
	if !cfg.nativeFallbackEnabled() {
		t.Fatalf("expected default nativeFallbackEnabled=true")
	}
	if !poolHasReadyProvider(cfg, "tier-fast") {
		t.Fatalf("expected primary-gw ready before cooldown")
	}
	poolMu.Lock()
	poolRuntime["primary-gw"] = &ProviderRuntime{CooldownUntil: time.Now().Add(5 * time.Minute)}
	poolMu.Unlock()
	if poolHasReadyProvider(cfg, "tier-fast") {
		t.Fatalf("expected no ready provider when primary-gw is cooling")
	}
	if err := savePoolConfig(cfg); err != nil {
		t.Fatalf("savePoolConfig: %v", err)
	}
	prof, model := "swag-pool", "tier-fast"
	if !resolveNativeFallback(&prof, &model) {
		t.Fatalf("expected resolveNativeFallback to trigger when all pool providers are cooling")
	}
	if prof != "" || model != "" {
		t.Fatalf("expected cleared authProfile and tier-* model after fallback, got prof=%q model=%q", prof, model)
	}
	if !isPoolFailureScreen("HTTP 429 Too Many Requests: rate limit exceeded") || isPoolFailureScreen("syntax error near unexpected token") {
		t.Fatalf("unexpected isPoolFailureScreen classification")
	}
}

func TestCodexMetaSkipsGuardianSubagent(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "guardian.jsonl")
	os.WriteFile(child, []byte(`{"type":"session_meta","payload":{"id":"child-1","parent_thread_id":"parent-1","cwd":"/mnt","source":{"subagent":{"other":"guardian"}},"thread_source":"guardian_review"}}`+"\n"), 0o600)
	if id, cwd := codexMeta(child); id != "" || cwd != "" {
		t.Fatalf("expected Guardian subagent rollout to be skipped, got id=%q cwd=%q", id, cwd)
	}
	parent := filepath.Join(dir, "parent.jsonl")
	os.WriteFile(parent, []byte(`{"type":"session_meta","payload":{"id":"parent-1","cwd":"/mnt","source":"cli"}}`+"\n"), 0o600)
	if id, cwd := codexMeta(parent); id != "parent-1" || cwd != "/mnt" {
		t.Fatalf("expected parent rollout to match, got id=%q cwd=%q", id, cwd)
	}
}

func TestLarkCardClickableOptionButtons(t *testing.T) {
	s := &Server{token: "test-secret-token"}
	u1 := s.quickReplyURL("http://192.0.2.10:7788", "e42", "批准推送和测试构建")
	u2 := s.quickReplyURL("http://192.0.2.10:7788", "e42", "暂缓")
	card := larkCard([]notice{{
		text:       "请审批下一步",
		entryID:    "e42",
		options:    []string{"批准推送和测试构建", "暂缓"},
		optionURLs: []string{u1, u2},
	}}, "http://192.0.2.10:7788/#/swag")
	if !strings.Contains(card, `"type":"primary"`) || !strings.Contains(card, "批准推送和测试构建") || !strings.Contains(card, "/api/super/quick-reply?") {
		t.Fatalf("expected clickable quick-reply buttons in lark card, got: %s", card)
	}
	req := httptest.NewRequest("GET", u1, nil)
	rec := httptest.NewRecorder()
	s.superQuickReply(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "已发送给 Swag") {
		t.Fatalf("expected 200 quick-reply confirmation, got %d: %s", rec.Code, rec.Body.String())
	}
	badReq := httptest.NewRequest("GET", u1+"tamper", nil)
	badRec := httptest.NewRecorder()
	s.superQuickReply(badRec, badReq)
	if badRec.Code != 403 {
		t.Fatalf("expected 403 on tampered signature, got %d", badRec.Code)
	}
}

func TestTodosAndInboxAck(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := &Server{}
	req := httptest.NewRequest("POST", "/api/super/todos", bytes.NewReader([]byte(`{"title":"复查本周发布检查项","source":"feishu"}`)))
	rec := httptest.NewRecorder()
	s.superTodoUpsert(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "复查本周发布检查项") {
		t.Fatalf("expected 200 todo create, got %d: %s", rec.Code, rec.Body.String())
	}
	list := loadTodos()
	if len(list) != 1 || list[0].Status != "open" || list[0].Source != "feishu" {
		t.Fatalf("unexpected todos: %+v", list)
	}
	ackReq := httptest.NewRequest("POST", "/api/super/inbox/ack", bytes.NewReader([]byte(`{"ids":["t1","t2"]}`)))
	ackRec := httptest.NewRecorder()
	s.superInboxAck(ackRec, ackReq)
	if ackRec.Code != 200 || !loadInboxAck()["t1"] || !loadInboxAck()["t2"] {
		t.Fatalf("unexpected inbox ack result: %d %+v", ackRec.Code, loadInboxAck())
	}
}

func TestPoolRuntimeStatsPersistAcrossRestart(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWAG_HOME", tmp)

	poolMu.Lock()
	poolRuntimeDir = ""
	poolRuntime = map[string]*ProviderRuntime{}
	rt := runtimeFor("openai-primary")
	rt.Requests = 3
	rt.Successes = 3
	savePoolRuntimeLocked()
	poolMu.Unlock()

	recordPoolUsageTokens("openai-primary", []byte(`{"usage":{"input_tokens":1200,"output_tokens":345,"total_tokens":1545}}`))

	// Simulate Hub process restart by wiping in-memory state.
	poolMu.Lock()
	poolRuntimeDir = ""
	poolRuntime = map[string]*ProviderRuntime{}
	poolMu.Unlock()

	snap := poolStatsSnapshot()
	got, ok := snap["openai-primary"]
	if !ok {
		t.Fatalf("expected openai-primary stats after restart, got %+v", snap)
	}
	if got.Requests != 3 || got.Successes != 3 || got.InputTokens != 1200 || got.OutputTokens != 345 || got.TotalTokens != 1545 {
		t.Fatalf("unexpected persisted pool stats: %+v", got)
	}
}

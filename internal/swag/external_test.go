package swag

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseEtime(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"05:07":      5*time.Minute + 7*time.Second,
		"01:06:25":   time.Hour + 6*time.Minute + 25*time.Second,
		"2-03:00:01": 51*time.Hour + time.Second,
	} {
		if got := parseEtime(in); got != want {
			t.Errorf("%s: %v, want %v", in, got, want)
		}
	}
}

func TestProgName(t *testing.T) {
	for want, args := range map[string][]string{
		"claude": {"/Users/x/.local/bin/claude", "--resume", "id"},
		"codex":  {"node", "/usr/lib/node_modules/@openai/codex/bin/codex.js"},
		"zsh":    {"-zsh"},
	} {
		if got := progName(args); got != want {
			t.Errorf("%v: %q, want %q", args, got, want)
		}
	}
}

// SWAG_EXTERNAL=1 go test -run TestExternalLive -v prints what this machine finds.
func TestExternalLive(t *testing.T) {
	if os.Getenv("SWAG_EXTERNAL") == "" {
		t.Skip("set SWAG_EXTERNAL=1")
	}
	home, _ := os.UserHomeDir()
	s := &Server{home: home}
	for _, e := range s.externalSessions() {
		t.Logf("%s app=%q cwd=%s session=%s title=%q path=%s", e.ID, e.App, e.Cwd, e.Session, e.Title, e.path)
	}
}

func TestLocateExternalCodexResumeSameCwd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	sessDir := filepath.Join(home, ".codex", "sessions", "2026", "11", "18")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	idA := "01a1edc2-84e3-7273-8835-2ba25706e270"
	idB := "01a1e8b7-43f1-79a0-bdcf-c34486305784"
	fA := filepath.Join(sessDir, "rollout-2026-11-18T10-00-00-"+idA+".jsonl")
	fB := filepath.Join(sessDir, "rollout-2026-11-18T11-00-00-"+idB+".jsonl")
	os.WriteFile(fA, []byte(`{"type":"session_meta","payload":{"id":"`+idA+`","cwd":"/mnt"}}`+"\n"), 0o600)
	os.WriteFile(fB, []byte(`{"type":"session_meta","payload":{"id":"`+idB+`","cwd":"/mnt"}}`+"\n"), 0o600)

	s := &Server{home: home}
	claimed := map[string]bool{}
	now := time.Now()
	eA := &External{Kind: "codex", Cwd: "/mnt", Started: now.Add(-time.Hour)}
	eB := &External{Kind: "codex", Cwd: "/mnt", Started: now.Add(-time.Hour)}
	s.locateExternal(eA, &proc{args: []string{"codex", "resume", idA}}, claimed)
	claimed["codex:"+eA.Session] = true
	s.locateExternal(eB, &proc{args: []string{"codex", "resume", idB}}, claimed)

	if eA.Session != idA || eA.path != fA {
		t.Fatalf("eA got session=%q path=%q, want %q / %q", eA.Session, eA.path, idA, fA)
	}
	if eB.Session != idB || eB.path != fB {
		t.Fatalf("eB got session=%q path=%q, want %q / %q", eB.Session, eB.path, idB, fB)
	}
}

func TestDeckSessionDetectsAutoTitledRollout(t *testing.T) {
	dir := t.TempDir()
	rollout := filepath.Join(dir, "rollout.jsonl")
	body := `{"type":"session_meta","payload":{"id":"abc","cwd":"/tmp"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"text":"[swag] 1 new event\n#t42 You are worker demo-worker-1"}]}}` + "\n"
	if err := os.WriteFile(rollout, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if deckSession("更新集群模型配置") {
		t.Fatalf("auto-generated title alone should not match")
	}
	if !isDeckRollout(rollout, "codex") {
		t.Fatalf("isDeckRollout should identify Swag-managed rollout even when SQLite title was auto-summarized")
	}
}

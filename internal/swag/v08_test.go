package swag

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNextEscalationHintLadder(t *testing.T) {
	cases := []struct {
		name string
		w    *Worker
		want string
	}{
		{"pool-fast", &Worker{Model: "tier-fast", AuthProfile: "swag-pool"}, "--auth-profile swag-pool --model tier-standard"},
		{"pool-standard", &Worker{Model: "tier-standard", AuthProfile: "swag-pool"}, "--auth-profile swag-pool --model tier-strong"},
		{"effort-low", &Worker{Model: "gpt-6.2-astra", Effort: "low"}, "--effort medium"},
		{"effort-medium", &Worker{Model: "gpt-6.2-astra", Effort: "medium"}, "--effort high"},
		{"effort-from-run", &Worker{Run: &[2]string{"gpt-6.2-astra", "high"}}, "--effort xhigh"},
	}
	for _, tc := range cases {
		if got := nextEscalationHint(tc.w); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDirectCodexExternalLookup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	sessID := "019a0000-0000-7000-8000-000000000099"
	dir := filepath.Join(home, ".codex", "sessions", "2026", "11", "26")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(dir, "rollout-2026-11-26-"+sessID+".jsonl")
	line := `{"type":"session_meta","payload":{"id":"` + sessID + `","cwd":"/tmp/demo-project"}}` + "\n"
	if err := os.WriteFile(rollout, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	// Ensure warm cache is empty so direct lookup path is exercised.
	externalCache.Lock()
	externalCache.at = time.Time{}
	externalCache.list = nil
	externalCache.Unlock()

	s := &Server{home: home}
	got := s.viewableByID("cx-" + sessID)
	if got == nil {
		t.Fatalf("expected direct Codex lookup to resolve cx-%s", sessID)
	}
	if got.path != rollout || got.Cwd != "/tmp/demo-project" || !got.desktop {
		t.Fatalf("unexpected resolved external session: %+v", got)
	}
}

func TestMemoryZipExportAndImportMergeReplace(t *testing.T) {
	srcDir := filepath.Join(t.TempDir(), "memory")
	if err := os.MkdirAll(filepath.Join(srcDir, "notes"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(srcDir, "instructions.md"), []byte("# Rules\nUse medium.\n"), 0o600)
	_ = os.WriteFile(filepath.Join(srcDir, "notes", "index.md"), []byte("- a.md\n"), 0o600)
	_ = os.WriteFile(filepath.Join(srcDir, "messages.md"), []byte("hub-managed\n"), 0o600)

	var buf bytes.Buffer
	n, err := exportMemoryZip(srcDir, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 exported files (excluding messages.md), got %d", n)
	}

	// Append a malicious zip-slip entry and ensure it is skipped safely.
	var badBuf bytes.Buffer
	zw := zip.NewWriter(&badBuf)
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	for _, f := range zr.File {
		w, _ := zw.Create(f.Name)
		rc, _ := f.Open()
		_, _ = bytes.NewBuffer(nil).ReadFrom(rc)
		rc.Close()
		rc2, _ := f.Open()
		b, _ := os.ReadFile(filepath.Join(srcDir, strings.TrimPrefix(f.Name, "memory/")))
		rc2.Close()
		_, _ = w.Write(b)
	}
	ew, _ := zw.Create("../escape.md")
	_, _ = ew.Write([]byte("bad"))
	_ = zw.Close()

	dstDir := filepath.Join(t.TempDir(), "memory")
	_ = os.MkdirAll(filepath.Join(dstDir, "notes"), 0o700)
	_ = os.WriteFile(filepath.Join(dstDir, "instructions.md"), []byte("# Rules\nOld.\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dstDir, "notes", "local-only.md"), []byte("keep in merge\n"), 0o600)

	dry, err := importMemoryZip(dstDir, badBuf.Bytes(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Added) != 1 || len(dry.Updated) != 1 || len(dry.Skipped) != 1 {
		t.Fatalf("unexpected dry-run diff: %+v", dry)
	}
	// Dry-run must not mutate disk.
	if b, _ := os.ReadFile(filepath.Join(dstDir, "instructions.md")); string(b) != "# Rules\nOld.\n" {
		t.Fatalf("dry-run modified file on disk")
	}

	// Merge keeps local-only.md.
	merged, err := importMemoryZip(dstDir, badBuf.Bytes(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Removed) != 0 {
		t.Fatalf("merge should not remove files: %+v", merged)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "notes", "local-only.md")); err != nil {
		t.Fatalf("merge removed local-only.md")
	}

	// Replace removes local-only.md.
	rep, err := importMemoryZip(dstDir, badBuf.Bytes(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Removed) != 1 || rep.Removed[0] != "notes/local-only.md" {
		t.Fatalf("expected replace to remove notes/local-only.md, got %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "notes", "local-only.md")); !os.IsNotExist(err) {
		t.Fatalf("expected notes/local-only.md deleted on replace")
	}
}

func TestReadCursorMonotonicPersistence(t *testing.T) {
	dir := t.TempDir()
	db, err := openDB(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	st := defaultState(dir)
	sp := &Super{db: db, st: st, subs: map[chan struct{}]bool{}}
	s := &Server{sup: sp}

	post := func(payload string) string {
		req := httptest.NewRequest("POST", "/api/super/read", strings.NewReader(payload))
		rec := httptest.NewRecorder()
		s.superReadCursor(rec, req)
		if rec.Code != 200 {
			t.Fatalf("read cursor status %d: %s", rec.Code, rec.Body.String())
		}
		var out struct {
			ReadCursor string `json:"read_cursor"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.ReadCursor
	}

	if got := post(`{"cursor":"e120"}`); got != "e120" {
		t.Fatalf("want e120, got %q", got)
	}
	if got := post(`{"cursor":"e90"}`); got != "e120" {
		t.Fatalf("backward cursor should be ignored without force, got %q", got)
	}
	if got := post(`{"cursor":"e155"}`); got != "e155" {
		t.Fatalf("want e155, got %q", got)
	}

	if err := saveState(db, sp.st, nil); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadState(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ReadCursor != "e155" {
		t.Fatalf("expected persisted ReadCursor e155, got %q", reloaded.ReadCursor)
	}
}

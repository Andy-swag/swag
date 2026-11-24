package swag

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Executable architectural guardrails (inspired by Lauren Tan's "All Code Is Slop"):
// Put recurring corrections in deterministic checks, not prose.

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func goFiles(t *testing.T, includeTests bool) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		if !includeTests && strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, name)
	}
	return out
}

func TestArchNoHardcodedSecrets(t *testing.T) {
	root := repoRoot(t)
	re := regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----|ghp_[A-Za-z0-9]{20,}|sk-[A-Za-z0-9]{20,}|AIza[0-9A-Za-z_-]{20,}|AKIA[0-9A-Z]{16}|cli_[0-9a-f]{12,}|ou_[0-9a-f]{16,}|Bearer\s+[A-Za-z0-9._-]{20,}`)
	dirs := []string{
		root,
		filepath.Join(root, "internal", "swag"),
		filepath.Join(root, "web"),
		filepath.Join(root, "scripts"),
		filepath.Join(root, "templates", "memory"),
		filepath.Join(root, "templates", "memory", "notes"),
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || e.Name() == "arch_test.go" {
				continue
			}
			ext := filepath.Ext(e.Name())
			if ext != ".go" && ext != ".js" && ext != ".html" && ext != ".css" && ext != ".sh" && ext != ".md" && e.Name() != "go.mod" {
				continue
			}
			p := filepath.Join(dir, e.Name())
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read %s: %v", p, err)
			}
			if loc := re.FindIndex(b); loc != nil {
				t.Errorf("%s contains hardcoded secret pattern %q", filepath.Join(filepath.Base(dir), e.Name()), string(b[loc[0]:loc[1]]))
			}
		}
	}
}

func TestArchSingleAgentStartPath(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range goFiles(t, false) {
		if name == "server.go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING && lit.Value == `"agent.start"` {
				pos := fset.Position(lit.Pos())
				t.Errorf("%s:%d calls \"agent.start\" directly; route all launches through Server.startAgentWithProfile in server.go so agentMissing, trustForStart, and AuthProfile injection cannot be bypassed", pos.Filename, pos.Line)
			}
			return true
		})
	}
}

func TestArchSingleLarkSendPath(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range goFiles(t, false) {
		if name == "lark.go" || name == "sync.go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "larkSend" {
				pos := fset.Position(call.Pos())
				t.Errorf("%s:%d calls larkSend() directly; use Server.larkNotify() in sync.go so remote LarkMachine routing is never bypassed", pos.Filename, pos.Line)
			}
			return true
		})
	}
}

func TestArchNoWorkaroundComments(t *testing.T) {
	bad := regexp.MustCompile(`(?i)\b(todo|fixme|hack|xxx|workaround|kludge|for now|temporary fix)\b`)
	fset := token.NewFileSet()
	for _, name := range goFiles(t, false) {
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if bad.MatchString(c.Text) {
					pos := fset.Position(c.Pos())
					t.Errorf("%s:%d has workaround-excuse comment %q — fix the root cause or encode the constraint as an executable check in arch_test.go", pos.Filename, pos.Line, strings.TrimSpace(c.Text))
				}
			}
		}
	}
}

func TestArchTimeSleepRatchet(t *testing.T) {
	// Ratchet cap: existing legacy sites are frozen; no new file may add raw time.Sleep.
	// Use sleepCtx(ctx, d) or event-driven readiness instead.
	allowed := map[string]int{
		"desktop.go":       1,
		"dream.go":         1,
		"external.go":      1,
		"external_send.go": 1,
		"fleet.go":         1,
		"hub.go":           1,
		"server.go":        2,
		"super.go":         2,
		"superapi.go":      1,
		"updates.go":       2,
	}
	fset := token.NewFileSet()
	for _, name := range goFiles(t, false) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		count := 0
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if ok && sel.Sel.Name == "Sleep" {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "time" {
					count++
				}
			}
			return true
		})
		capN := allowed[name]
		if count > capN {
			t.Errorf("%s has %d raw time.Sleep calls (ratchet max %d) — use sleepCtx(ctx, d) or readiness signals instead of imperative sleeps", name, count, capN)
		}
	}
}

func TestArchFileSizeRatchet(t *testing.T) {
	root := repoRoot(t)
	caps := map[string]int{
		filepath.Join(root, "internal", "swag", "super.go"):    2080,
		filepath.Join(root, "internal", "swag", "superapi.go"): 1220,
		filepath.Join(root, "web", "app.js"):                   5800,
	}
	for path, maxLines := range caps {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		lines := strings.Count(string(b), "\n") + 1
		if lines > maxLines {
			t.Errorf("%s grew to %d lines (ratchet max %d) — extract new functionality into a focused module instead of expanding a monolith", filepath.Base(path), lines, maxLines)
		}
	}
}

func TestArchCommentRatioOnNewModules(t *testing.T) {
	// New/refactored modules keep comments concise (<= 22% of lines) so code stays self-documenting.
	strictFiles := []string{"profiles.go", "sync.go", "trust.go", "search.go", "notify.go", "usage.go", "pool.go", "quota.go"}
	for _, name := range strictFiles {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		lines := strings.Split(string(b), "\n")
		comments := 0
		for _, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "//") {
				comments++
			}
		}
		ratio := float64(comments) / float64(max(len(lines), 1))
		if ratio > 0.22 {
			t.Errorf("%s comment ratio is %.1f%% (max 22%%) — prefer clear names, types, and executable checks over prose comments", name, ratio*100)
		}
	}
}

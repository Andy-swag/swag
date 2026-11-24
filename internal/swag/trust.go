package swag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Antigravity asks "Do you trust the contents of this project?" the first
// time it runs in a folder, even with --dangerously-skip-permissions: folder
// trust is its own setting. When Swag starts an agy agent with permissions
// bypassed (the human's choice in settings), it trusts the folder first, so a
// new worker doesn't sit on that dialog.

// agySettings is where agy keeps trustedWorkspaces.
func agySettings(home string) string {
	return filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
}

// trustAgyFolder adds dir (and the path it resolves to) to agy's trusted
// workspaces on this computer, keeping every other setting as it is.
func trustAgyFolder(home, dir string) error {
	if dir == "" || !filepath.IsAbs(dir) {
		return fmt.Errorf("not an absolute folder: %q", dir)
	}
	want := []string{filepath.Clean(dir)}
	if r, err := filepath.EvalSymlinks(dir); err == nil && r != want[0] {
		want = append(want, r)
	}
	file := agySettings(home)
	cfg := map[string]json.RawMessage{}
	if b, err := os.ReadFile(file); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var trusted []string
	if raw, ok := cfg["trustedWorkspaces"]; ok {
		if err := json.Unmarshal(raw, &trusted); err != nil {
			return fmt.Errorf("%s: trustedWorkspaces: %w", file, err)
		}
	}
	added := false
	for _, p := range want {
		if !slices.Contains(trusted, p) {
			trusted, added = append(trusted, p), true
		}
	}
	if !added {
		return nil
	}
	cfg["trustedWorkspaces"], _ = json.Marshal(trusted)
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".swag-tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// trustClaudeBypass records bypassPermissionsModeAccepted=true in ~/.claude.json
// when the user opted into Swag's "跳过权限确认" setting, so Claude Code does not
// stall on its first-run Bypass Permissions warning dialog.
func trustClaudeBypass(home string) error {
	file := filepath.Join(home, ".claude.json")
	cfg := map[string]json.RawMessage{}
	if b, err := os.ReadFile(file); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if raw, ok := cfg["bypassPermissionsModeAccepted"]; ok && string(bytes.TrimSpace(raw)) == "true" {
		return nil
	}
	cfg["bypassPermissionsModeAccepted"] = json.RawMessage("true")
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := file + ".swag-tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// trustForStart pre-trusts cwd (for agy) or bypass mode (for claude) about to
// start with bypassed permissions, on the machine it runs on.
func (s *Server) trustForStart(ctx context.Context, machine, herdrKind, cwd string, args []string) {
	if (herdrKind != "agy" && herdrKind != "claude") || !slices.Contains(args, "--dangerously-skip-permissions") {
		return
	}
	var err error
	if mc, e := s.herdr.machine(machine); e != nil {
		err = e
	} else if mc.Local {
		if herdrKind == "claude" {
			err = trustClaudeBypass(s.home)
		} else {
			err = trustAgyFolder(s.home, s.expand(cwd))
		}
	} else {
		err = s.trustRemote(ctx, machine, herdrKind, cwd)
	}
	if err != nil {
		log.Printf("%s 预信任没写上（%s %s）：%v", herdrKind, machine, cwd, err)
	}
}

func (s *Server) trustRemote(ctx context.Context, machine, kind, cwd string) error {
	client, err := s.herdr.Client(machine)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, _ := json.Marshal(map[string]string{"kind": kind, "dir": cwd})
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://node/api/trust", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("node: %s", resp.Status) // an older node: 404
	}
	return nil
}

// trustFolder is the node side of trustRemote.
func (s *Server) trustFolder(w http.ResponseWriter, r *http.Request) {
	var body struct{ Kind, Dir string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Kind != "agy" && body.Kind != "claude") {
		writeJSON(w, 400, map[string]any{"error": "want {kind: agy|claude, dir}"})
		return
	}
	var err error
	if body.Kind == "claude" {
		err = trustClaudeBypass(s.home)
	} else {
		err = trustAgyFolder(s.home, s.expand(body.Dir))
	}
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

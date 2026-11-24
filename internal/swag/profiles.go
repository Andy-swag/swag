package swag

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// AuthProfile holds a named set of environment variables (e.g. OPENAI_API_KEY,
// OPENAI_BASE_URL, ANTHROPIC_API_KEY, ANTHROPIC_BASE_URL, CODEX_HOME,
// CLAUDE_CONFIG_DIR) injected into a fresh agent pane before starting the CLI.
// Profiles live strictly in ~/.swag/profiles.json (0600) and are never written
// to memory/ or returned in plaintext over the API.
type AuthProfile struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Env         map[string]string `json:"env"`
}

// AuthProfileView is the masked view returned to the web UI.
type AuthProfileView struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Keys        []string          `json:"keys"`
	Masked      map[string]string `json:"masked"`
}

var (
	profileMu     sync.Mutex
	profileNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,47}$`)
	envKeyRe      = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
)

func profilesPath() string { return filepath.Join(swagDir(), "profiles.json") }

func loadAuthProfiles() map[string]AuthProfile {
	profileMu.Lock()
	defer profileMu.Unlock()
	out := map[string]AuthProfile{}
	b, err := os.ReadFile(profilesPath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

func saveAuthProfiles(m map[string]AuthProfile) error {
	profileMu.Lock()
	defer profileMu.Unlock()
	if err := os.MkdirAll(swagDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := profilesPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, profilesPath())
}

func getAuthProfileEnv(name string) map[string]string {
	name = strings.TrimSpace(name)
	if name == "" || name == "default" {
		return nil
	}
	m := loadAuthProfiles()
	p, ok := m[name]
	if !ok || len(p.Env) == 0 {
		return nil
	}
	cp := make(map[string]string, len(p.Env))
	for k, v := range p.Env {
		if envKeyRe.MatchString(k) {
			cp[k] = v
		}
	}
	return cp
}

func maskSecret(k, v string) string {
	lk := strings.ToLower(k)
	if strings.Contains(lk, "url") || strings.Contains(lk, "dir") || strings.Contains(lk, "home") || strings.Contains(lk, "model") {
		return v
	}
	r := []rune(v)
	if len(r) <= 6 {
		return "••••"
	}
	return string(r[:2]) + "••••" + string(r[len(r)-4:])
}

func listAuthProfilesMasked() []AuthProfileView {
	m := loadAuthProfiles()
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]AuthProfileView, 0, len(names))
	for _, n := range names {
		p := m[n]
		keys := make([]string, 0, len(p.Env))
		masked := make(map[string]string, len(p.Env))
		for k, v := range p.Env {
			keys = append(keys, k)
			masked[k] = maskSecret(k, v)
		}
		sort.Strings(keys)
		out = append(out, AuthProfileView{
			Name:        n,
			Description: p.Description,
			Keys:        keys,
			Masked:      masked,
		})
	}
	return out
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// buildEnvExportCmd builds a history-exempt (leading-space) shell command that
// exports profile env vars in a fresh herdr pane right before agent.start.
func buildEnvExportCmd(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		if envKeyRe.MatchString(k) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := env[k]
		if v == "" {
			parts = append(parts, "unset "+k)
		} else {
			parts = append(parts, "export "+k+"="+shellSingleQuote(v))
		}
	}
	return " " + strings.Join(parts, "; ") + "; clear\n"
}

// HTTP handlers for Auth Profiles (masked reads, safe writes).

func (s *Server) superProfiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"profiles": listAuthProfilesMasked()})
}

func (s *Server) superSaveProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Env         map[string]string `json:"env"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if !profileNameRe.MatchString(body.Name) || body.Name == "default" {
		writeJSON(w, 400, map[string]any{"error": "profile 名称仅支持字母/数字/._-，且不能为 default"})
		return
	}
	clean := map[string]string{}
	for k, v := range body.Env {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if !envKeyRe.MatchString(k) {
			writeJSON(w, 400, map[string]any{"error": fmt.Sprintf("环境变量名不合法：%q", k)})
			return
		}
		clean[k] = v
	}
	m := loadAuthProfiles()
	// Merge with existing values if caller sent an unchanged masked placeholder.
	if old, ok := m[body.Name]; ok {
		for k, v := range clean {
			if strings.Contains(v, "••••") {
				if prev, exists := old.Env[k]; exists {
					clean[k] = prev
				}
			}
		}
	}
	m[body.Name] = AuthProfile{Name: body.Name, Description: strings.TrimSpace(body.Description), Env: clean}
	if err := saveAuthProfiles(m); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "profiles": listAuthProfilesMasked()})
}

func (s *Server) superDeleteProfile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	m := loadAuthProfiles()
	delete(m, name)
	if err := saveAuthProfiles(m); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "profiles": listAuthProfilesMasked()})
}

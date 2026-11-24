package swag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Cross-machine control-plane sync over the existing yamux HTTP tunnel:
//   1. Read-only memory mirror (Hub → all online nodes at ~/.swag/memory-mirror/)
//   2. Remote Dream workspace push/pull (Hub ↔ DreamMachine at ~/.swag/dream-work/)
//   3. Opt-in external desktop/CLI session digest pull for Dream
//   4. Remote Lark CLI proxy (Hub → LarkMachine e.g. dev-node-1)

const (
	mirrorSubdir    = "memory-mirror"
	dreamWorkSubdir = "dream-work"
)

func safeRelPath(rel string) (string, bool) {
	rel = filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
	if rel == "" || rel == "." || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		return "", false
	}
	return rel, true
}

func collectDirFiles(root string, skipDotApple bool) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if skipDotApple && strings.HasPrefix(name, "._") {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if info, err := d.Info(); err != nil || info.Size() > 512*1024 {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		out[rel] = string(b)
		return nil
	})
	return out
}

func applyDirMirror(root string, files map[string]string, prune bool) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	want := map[string]bool{}
	for rel, content := range files {
		clean, ok := safeRelPath(rel)
		if !ok {
			continue
		}
		want[clean] = true
		dst := filepath.Join(root, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(content), 0o600); err != nil {
			return err
		}
	}
	if prune {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if rel == "README-MIRROR.md" {
				return nil
			}
			if !want[rel] {
				_ = os.Remove(p)
			}
			return nil
		})
	}
	return nil
}

// ---------------------------------------------------------------- node endpoints

func (s *Server) nodeRoutesExtra(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/node/memory-mirror", s.nodeReceiveMemoryMirror)
	mux.HandleFunc("POST /api/node/dream-push", s.nodeDreamPush)
	mux.HandleFunc("GET /api/node/dream-pull", s.nodeDreamPull)
	mux.HandleFunc("GET /api/node/dream-external", s.nodeDreamExternal)
	mux.HandleFunc("GET /api/node/lark-status", s.nodeLarkStatus)
	mux.HandleFunc("POST /api/node/lark-send", s.nodeLarkSend)
	mux.HandleFunc("GET /api/node/usage", s.nodeUsage)
	mux.HandleFunc("GET /api/node/quota", s.nodeQuota)
	mux.HandleFunc("GET /api/node/router-memory", s.nodeRouterMemoryGet)
	mux.HandleFunc("POST /api/node/router-memory", s.nodeRouterMemorySave)
}

func (s *Server) nodeReceiveMemoryMirror(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Files map[string]string `json:"files"`
	}
	if !decode(w, r, &body) {
		return
	}
	dir := filepath.Join(swagDir(), mirrorSubdir)
	if err := applyDirMirror(dir, body.Files, true); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	readme := "# Swag Memory Mirror (Read-Only)\n\nSynced automatically from the Swag Hub at " + time.Now().Format("2006-01-02 15:04:05") + ".\nDo not edit files here; changes are overwritten on the next sync.\n"
	_ = os.WriteFile(filepath.Join(dir, "README-MIRROR.md"), []byte(readme), 0o600)
	writeJSON(w, 200, map[string]any{"ok": true, "dir": dir, "files": len(body.Files)})
}

func (s *Server) nodeDreamPush(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RouterMD string            `json:"router_md"`
		InputMD  string            `json:"input_md"`
		Memory   map[string]string `json:"memory"`
		Dreams   map[string]string `json:"dreams"`
	}
	if !decode(w, r, &body) {
		return
	}
	work := filepath.Join(swagDir(), dreamWorkSubdir)
	if err := os.MkdirAll(filepath.Join(work, "dream"), 0o700); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(filepath.Join(work, "dreams"), 0o700); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if body.RouterMD != "" {
		_ = os.WriteFile(filepath.Join(work, "ROUTER.md"), []byte(body.RouterMD), 0o600)
	}
	if body.InputMD != "" {
		_ = os.WriteFile(filepath.Join(work, "dream", "input.md"), []byte(body.InputMD), 0o600)
	}
	if err := applyDirMirror(filepath.Join(work, "memory"), body.Memory, true); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if err := applyDirMirror(filepath.Join(work, "dreams"), body.Dreams, false); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "dir": work})
}

func (s *Server) nodeDreamPull(w http.ResponseWriter, r *http.Request) {
	work := filepath.Join(swagDir(), dreamWorkSubdir)
	writeJSON(w, 200, map[string]any{
		"memory": collectDirFiles(filepath.Join(work, "memory"), true),
		"dreams": collectDirFiles(filepath.Join(work, "dreams"), true),
	})
}

func (s *Server) nodeRouterMemoryGet(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(swagDir(), "router", "memory")
	writeJSON(w, 200, map[string]any{"files": collectDirFiles(dir, true)})
}

func (s *Server) nodeRouterMemorySave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	rel, ok := safeRelPath(body.Path)
	if !ok || !strings.HasSuffix(rel, ".md") || rel == "messages.md" {
		writeJSON(w, 400, map[string]any{"error": "invalid memory path"})
		return
	}
	dst := filepath.Join(swagDir(), "router", "memory", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if err := os.WriteFile(dst, []byte(body.Text), 0o600); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) nodeDreamExternal(w http.ResponseWriter, r *http.Request) {
	sinceRaw := strings.TrimSpace(r.URL.Query().Get("since"))
	var since time.Time
	if sinceRaw != "" {
		since, _ = time.Parse(time.RFC3339, sinceRaw)
	}
	var cwds []string
	if raw := strings.TrimSpace(r.URL.Query().Get("cwds")); raw != "" {
		for _, c := range strings.Split(raw, ",") {
			if c = strings.TrimSpace(c); c != "" {
				cwds = append(cwds, expandIn(s.home, c))
			}
		}
	}
	pinned := map[string]bool{}
	if raw := strings.TrimSpace(r.URL.Query().Get("pinned")); raw != "" {
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				pinned[p] = true
			}
		}
	}
	digest := s.buildLocalExternalDreamDigest(since, cwds, pinned)
	writeJSON(w, 200, map[string]any{"digest": digest})
}

func (s *Server) buildLocalExternalDreamDigest(since time.Time, cwds []string, pinned map[string]bool) string {
	rows, _ := s.desktopRows()
	if len(rows) == 0 {
		return ""
	}
	const maxBytes = 40 << 10
	var b strings.Builder
	matchCwd := func(dir string) bool {
		if len(cwds) == 0 {
			return true
		}
		dir = filepath.Clean(dir)
		for _, prefix := range cwds {
			prefix = filepath.Clean(prefix)
			if dir == prefix || strings.HasPrefix(dir, prefix+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].updated.After(rows[j].updated) })
	count := 0
	for _, row := range rows {
		isPinned := pinned[row.session] || pinned[row.id] || pinned[row.kind+":"+row.session]
		if !isPinned {
			if !since.IsZero() && row.updated.Before(since) {
				continue
			}
			if !matchCwd(row.cwd) {
				continue
			}
		}
		if row.path == "" {
			continue
		}
		tr := newTranscript(row.path, row.format)
		tr.update()
		if len(tr.msgs) == 0 {
			continue
		}
		var sec strings.Builder
		fmt.Fprintf(&sec, "\n### %s · %s (%s, updated %s)\n", row.app, clip(oneLine(row.title), 80), s.tilde(row.cwd), row.updated.Format("01-02 15:04"))
		start := 0
		if len(tr.msgs) > 12 {
			start = len(tr.msgs) - 12
		}
		for _, m := range tr.msgs[start:] {
			switch m.Kind {
			case "user":
				fmt.Fprintf(&sec, "USER: %s\n", clip(oneLine(m.Text), 400))
			case "assistant":
				fmt.Fprintf(&sec, "ASSISTANT: %s\n", clip(oneLine(m.Text), 500))
			}
		}
		if b.Len()+sec.Len() > maxBytes {
			break
		}
		b.WriteString(sec.String())
		count++
		if count >= 12 {
			break
		}
	}
	return b.String()
}

type larkNoticeWire struct {
	Tone       string   `json:"tone"`
	Text       string   `json:"text"`
	EntryID    string   `json:"entry_id,omitempty"`
	Re         *Quote   `json:"re,omitempty"`
	Options    []string `json:"options,omitempty"`
	OptionURLs []string `json:"option_urls,omitempty"`
	Check      bool     `json:"check,omitempty"`
}

func (s *Server) nodeLarkStatus(w http.ResponseWriter, r *http.Request) {
	_, _, who, problem := lark.get(r.URL.Query().Has("fresh"))
	writeJSON(w, 200, map[string]any{"ready": problem == "", "who": who, "problem": problem})
}

func (s *Server) nodeLarkSend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Notices []larkNoticeWire `json:"notices"`
		Link    string           `json:"link"`
	}
	if !decode(w, r, &body) {
		return
	}
	ns := make([]notice, 0, len(body.Notices))
	for _, n := range body.Notices {
		ns = append(ns, notice{tone: n.Tone, text: n.Text, entryID: n.EntryID, re: n.Re, options: n.Options, optionURLs: n.OptionURLs, check: n.Check})
	}
	if err := larkSend(ns, body.Link); err != nil {
		writeJSON(w, 502, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------------------------------------------------------------- hub helpers

func (s *Server) nodeJSON(ctx context.Context, machineID, method, path string, reqBody any, respOut any) error {
	client, err := s.herdr.Client(machineID)
	if err != nil {
		return err
	}
	var body io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://node"+path, body)
	if err != nil {
		return err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		var er struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &er) == nil && er.Error != "" {
			return errors.New(er.Error)
		}
		return fmt.Errorf("node %s %s: HTTP %d", machineID, path, resp.StatusCode)
	}
	if respOut != nil && len(raw) > 0 {
		return json.Unmarshal(raw, respOut)
	}
	return nil
}

var remoteRouterMemSync = struct {
	sync.Mutex
	at time.Time
}{}

func (sp *Super) remoteRouterMachineID() string {
	if sp.s == nil || sp.s.herdr == nil {
		return ""
	}
	sp.mu.Lock()
	want := strings.TrimSpace(sp.st.Settings.RouterMachine)
	sp.mu.Unlock()
	if want == "" || want == "hub" || want == "local" {
		return ""
	}
	for _, m := range sp.s.herdr.Machines() {
		if !m.Local && m.Online && (m.ID == want || m.Name == want) {
			return m.ID
		}
	}
	return ""
}

func (sp *Super) PullRemoteRouterMemory(ctx context.Context, force bool) {
	id := sp.remoteRouterMachineID()
	if id == "" {
		return
	}
	remoteRouterMemSync.Lock()
	if !force && time.Since(remoteRouterMemSync.at) < 4*time.Second {
		remoteRouterMemSync.Unlock()
		return
	}
	remoteRouterMemSync.at = time.Now()
	remoteRouterMemSync.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var res struct {
		Files map[string]string `json:"files"`
	}
	if err := sp.s.nodeJSON(cctx, id, "GET", "/api/node/router-memory", nil, &res); err != nil || len(res.Files) == 0 {
		return
	}
	memDir := sp.memDir()
	for rel, content := range res.Files {
		clean, ok := safeRelPath(rel)
		if !ok || clean == "messages.md" {
			continue
		}
		dst := filepath.Join(memDir, filepath.FromSlash(clean))
		if cur, err := os.ReadFile(dst); err == nil && string(cur) == content {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(dst), 0o700)
		_ = os.WriteFile(dst, []byte(content), 0o600)
	}
	if entries, err := os.ReadDir(filepath.Join(memDir, "tasks")); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			activeRel := "tasks/" + e.Name()
			doneRel := "tasks/done/" + e.Name()
			if _, stillActive := res.Files[activeRel]; !stillActive {
				if _, movedDone := res.Files[doneRel]; movedDone {
					_ = os.Remove(filepath.Join(memDir, "tasks", e.Name()))
				}
			}
		}
	}
}

func (sp *Super) PushRemoteRouterMemoryFile(ctx context.Context, rel, text string) {
	id := sp.remoteRouterMachineID()
	if id == "" {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = sp.s.nodeJSON(cctx, id, "POST", "/api/node/router-memory", map[string]any{"path": rel, "text": text}, nil)
}

// SyncMemoryMirrors pushes the Hub's authoritative memory/ directory as a
// read-only mirror (~/.swag/memory-mirror/) to every online remote node.
func (sp *Super) SyncMemoryMirrors(ctx context.Context) map[string]string {
	results := map[string]string{}
	if sp.s == nil || sp.s.herdr == nil {
		return results
	}
	sp.PullRemoteRouterMemory(ctx, true)
	files := collectDirFiles(sp.memDir(), true)
	machines := sp.s.herdr.Machines()
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, m := range machines {
		if m.Local || !m.Online {
			continue
		}
		wg.Add(1)
		go func(m Machine) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			err := sp.s.nodeJSON(cctx, m.ID, "POST", "/api/node/memory-mirror", map[string]any{"files": files}, nil)
			mu.Lock()
			if err != nil {
				results[m.Name] = "error: " + err.Error()
				log.Printf("memory-mirror sync to %s: %v", m.Name, err)
			} else {
				results[m.Name] = fmt.Sprintf("ok (%d files)", len(files))
			}
			mu.Unlock()
		}(m)
	}
	wg.Wait()
	return results
}

func (sp *Super) pushDreamWork(ctx context.Context, machineID string) (string, error) {
	routerMD, _ := os.ReadFile(filepath.Join(sp.routerDir(), "ROUTER.md"))
	inputMD, _ := os.ReadFile(filepath.Join(sp.routerDir(), "dream", "input.md"))
	payload := map[string]any{
		"router_md": string(routerMD),
		"input_md":  string(inputMD),
		"memory":    collectDirFiles(sp.memDir(), true),
		"dreams":    collectDirFiles(filepath.Join(sp.routerDir(), "dreams"), true),
	}
	var res struct {
		Dir string `json:"dir"`
	}
	if err := sp.s.nodeJSON(ctx, machineID, "POST", "/api/node/dream-push", payload, &res); err != nil {
		return "", err
	}
	return res.Dir, nil
}

func (sp *Super) pullDreamWork(ctx context.Context, machineID string) error {
	var res struct {
		Memory map[string]string `json:"memory"`
		Dreams map[string]string `json:"dreams"`
	}
	if err := sp.s.nodeJSON(ctx, machineID, "GET", "/api/node/dream-pull", nil, &res); err != nil {
		return err
	}
	if len(res.Memory) > 0 {
		if err := applyDirMirror(sp.memDir(), res.Memory, true); err != nil {
			return err
		}
	}
	if len(res.Dreams) > 0 {
		if err := applyDirMirror(filepath.Join(sp.routerDir(), "dreams"), res.Dreams, false); err != nil {
			return err
		}
	}
	return nil
}

// collectExternalDreamInput pulls opted-in external session digests from Hub
// and all online nodes configured with DreamInclude or manually pinned sessions.
func (sp *Super) collectExternalDreamInput(ctx context.Context, since time.Time) string {
	if sp.s == nil || sp.s.herdr == nil {
		return ""
	}
	st := sp.settings()
	pinnedByMachine := map[string][]string{}
	for _, ref := range st.DreamPinned {
		parts := strings.SplitN(ref, ":", 2)
		if len(parts) == 2 {
			pinnedByMachine[parts[0]] = append(pinnedByMachine[parts[0]], parts[1])
		} else if ref != "" {
			pinnedByMachine["hub"] = append(pinnedByMachine["hub"], ref)
		}
	}
	var out strings.Builder
	for _, m := range sp.s.herdr.Machines() {
		key := m.ID
		if m.Local {
			key = "hub"
		}
		pol := st.PolicyFor(m.ID, m.Local)
		pins := append([]string(nil), pinnedByMachine[key]...)
		if m.Name != "" && m.Name != key {
			pins = append(pins, pinnedByMachine[m.Name]...)
		}
		if !pol.DreamInclude && len(pins) == 0 {
			continue
		}
		var digest string
		if m.Local {
			pinSet := map[string]bool{}
			for _, p := range pins {
				pinSet[p] = true
			}
			digest = sp.s.buildLocalExternalDreamDigest(since, pol.DreamCwds, pinSet)
		} else if m.Online {
			q := url.Values{}
			if !since.IsZero() {
				q.Set("since", since.Format(time.RFC3339))
			}
			if len(pol.DreamCwds) > 0 {
				q.Set("cwds", strings.Join(pol.DreamCwds, ","))
			}
			if len(pins) > 0 {
				q.Set("pinned", strings.Join(pins, ","))
			}
			var res struct {
				Digest string `json:"digest"`
			}
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := sp.s.nodeJSON(cctx, m.ID, "GET", "/api/node/dream-external?"+q.Encode(), nil, &res)
			cancel()
			if err == nil {
				digest = res.Digest
			}
		}
		if strings.TrimSpace(digest) != "" {
			fmt.Fprintf(&out, "\n### Machine: %s\n%s\n", m.Name, digest)
		}
	}
	return out.String()
}

// resolveLarkMachine returns the target machine ID for lark-cli ("" = local Hub).
func (s *Server) resolveLarkMachine() (string, string) {
	if s == nil || s.sup == nil {
		return "", ""
	}
	want := strings.TrimSpace(s.sup.settings().LarkMachine)
	if want == "" || want == "hub" {
		return "", ""
	}
	id := s.machineByName(want)
	if id == "" {
		return "", want
	}
	return id, want
}

func (s *Server) larkStatus(ctx context.Context, fresh bool) (ready bool, who, via, problem string) {
	id, want := s.resolveLarkMachine()
	if want != "" && id == "" {
		return false, "", want, "指定的飞书机器「" + want + "」未在线"
	}
	if id != "" {
		q := ""
		if fresh {
			q = "?fresh=1"
		}
		var res struct {
			Ready   bool   `json:"ready"`
			Who     string `json:"who"`
			Problem string `json:"problem"`
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := s.nodeJSON(cctx, id, "GET", "/api/node/lark-status"+q, nil, &res); err != nil {
			return false, "", want, "无法访问 " + want + " 的 lark-cli：" + err.Error()
		}
		return res.Ready, res.Who, want, res.Problem
	}
	_, _, who, problem = lark.get(fresh)
	return problem == "", who, "hub", problem
}

func (s *Server) larkNotify(ctx context.Context, ns []notice, link string) error {
	id, want := s.resolveLarkMachine()
	if want != "" && id == "" {
		return fmt.Errorf("指定的飞书机器 %q 未在线", want)
	}
	if id != "" {
		wire := make([]larkNoticeWire, 0, len(ns))
		for _, n := range ns {
			wire = append(wire, larkNoticeWire{Tone: n.tone, Text: n.text, EntryID: n.entryID, Re: n.re, Options: n.options, OptionURLs: n.optionURLs, Check: n.check})
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return s.nodeJSON(cctx, id, "POST", "/api/node/lark-send", map[string]any{"notices": wire, "link": link}, nil)
	}
	return larkSend(ns, link)
}

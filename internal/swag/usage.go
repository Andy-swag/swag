package swag

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type TokenBucket struct {
	Input     int64 `json:"input"`
	Cached    int64 `json:"cached"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning,omitempty"`
	Total     int64 `json:"total"`
	Sessions  int   `json:"sessions"`
}

func (b *TokenBucket) add(o TokenBucket) {
	b.Input += o.Input
	b.Cached += o.Cached
	b.Output += o.Output
	b.Reasoning += o.Reasoning
	b.Total += o.Total
	b.Sessions += o.Sessions
}

type NodeUsage struct {
	Machine string                 `json:"machine"`
	Days    int                    `json:"days"`
	Total   TokenBucket            `json:"total"`
	ByDay   map[string]TokenBucket `json:"by_day"`
	ByModel map[string]TokenBucket `json:"by_model"`
	ByKind  map[string]TokenBucket `json:"by_kind"`
}

var localUsageCache = struct {
	sync.Mutex
	days int
	at   time.Time
	data NodeUsage
}{}

func scanCodexRolloutUsage(path string) (model string, tok TokenBucket) {
	f, err := os.Open(path)
	if err != nil {
		return "", tok
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 256<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			s := string(line)
			if model == "" && strings.Contains(s, `"turn_context"`) {
				var tc struct {
					Payload struct {
						Model string `json:"model"`
					} `json:"payload"`
				}
				if json.Unmarshal(line, &tc) == nil && tc.Payload.Model != "" {
					model = tc.Payload.Model
				}
			}
			if strings.Contains(s, `"token_count"`) {
				var ev struct {
					Payload struct {
						Info *struct {
							TotalTokenUsage struct {
								InputTokens           int64 `json:"input_tokens"`
								CachedInputTokens     int64 `json:"cached_input_tokens"`
								OutputTokens          int64 `json:"output_tokens"`
								ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
								TotalTokens           int64 `json:"total_tokens"`
							} `json:"total_token_usage"`
						} `json:"info"`
					} `json:"payload"`
				}
				if json.Unmarshal(line, &ev) == nil && ev.Payload.Info != nil {
					u := ev.Payload.Info.TotalTokenUsage
					if u.TotalTokens > tok.Total {
						tok = TokenBucket{
							Input:     u.InputTokens,
							Cached:    u.CachedInputTokens,
							Output:    u.OutputTokens,
							Reasoning: u.ReasoningOutputTokens,
							Total:     u.TotalTokens,
							Sessions:  1,
						}
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	if model == "" {
		model = "codex-unknown"
	}
	return model, tok
}

func scanClaudeSessionUsage(path string) (model string, tok TokenBucket) {
	f, err := os.Open(path)
	if err != nil {
		return "", tok
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 256<<10)
	seenMsg := map[string]bool{}
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && strings.Contains(string(line), `"input_tokens"`) {
			var ev struct {
				Message struct {
					ID    string `json:"id"`
					Model string `json:"model"`
					Usage *struct {
						InputTokens              int64 `json:"input_tokens"`
						CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
						CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
						OutputTokens             int64 `json:"output_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &ev) == nil && ev.Message.Usage != nil {
				if ev.Message.ID != "" {
					if seenMsg[ev.Message.ID] {
						goto next
					}
					seenMsg[ev.Message.ID] = true
				}
				if ev.Message.Model != "" {
					model = ev.Message.Model
				}
				u := ev.Message.Usage
				in := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
				tok.Input += in
				tok.Cached += u.CacheReadInputTokens
				tok.Output += u.OutputTokens
				tok.Total += in + u.OutputTokens
			}
		}
	next:
		if err != nil {
			break
		}
	}
	if tok.Total > 0 {
		tok.Sessions = 1
	}
	if model == "" {
		model = "claude-unknown"
	}
	return model, tok
}

func scanLocalUsage(home string, days int) NodeUsage {
	if days <= 0 || days > 90 {
		days = 7
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	out := NodeUsage{
		Days:    days,
		ByDay:   map[string]TokenBucket{},
		ByModel: map[string]TokenBucket{},
		ByKind:  map[string]TokenBucket{},
	}
	addRec := func(day, model, kind string, t TokenBucket) {
		if t.Total == 0 {
			return
		}
		out.Total.add(t)
		db := out.ByDay[day]
		db.add(t)
		out.ByDay[day] = db
		mb := out.ByModel[model]
		mb.add(t)
		out.ByModel[model] = mb
		kb := out.ByKind[kind]
		kb.add(t)
		out.ByKind[kind] = kb
	}

	codexRoot := envOr("CODEX_HOME", filepath.Join(home, ".codex"))
	if files, err := filepath.Glob(filepath.Join(codexRoot, "sessions", "*", "*", "*", "rollout-*.jsonl")); err == nil {
		for _, f := range files {
			fi, err := os.Stat(f)
			if err != nil || fi.ModTime().Before(cutoff) {
				continue
			}
			m, t := scanCodexRolloutUsage(f)
			addRec(fi.ModTime().Format("2006-01-02"), m, "codex", t)
		}
	}

	claudeRoot := envOr("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	if files, err := filepath.Glob(filepath.Join(claudeRoot, "projects", "*", "*.jsonl")); err == nil {
		for _, f := range files {
			fi, err := os.Stat(f)
			if err != nil || fi.ModTime().Before(cutoff) {
				continue
			}
			m, t := scanClaudeSessionUsage(f)
			addRec(fi.ModTime().Format("2006-01-02"), m, "claude", t)
		}
	}
	return out
}

func (s *Server) localUsageCached(days int, fresh bool) NodeUsage {
	localUsageCache.Lock()
	if !fresh && localUsageCache.days == days && time.Since(localUsageCache.at) < time.Minute {
		d := localUsageCache.data
		localUsageCache.Unlock()
		return d
	}
	localUsageCache.Unlock()
	d := scanLocalUsage(s.home, days)
	localUsageCache.Lock()
	localUsageCache.days, localUsageCache.at, localUsageCache.data = days, time.Now(), d
	localUsageCache.Unlock()
	return d
}

func (s *Server) nodeUsage(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 {
		days = 7
	}
	u := s.localUsageCached(days, r.URL.Query().Has("fresh"))
	writeJSON(w, 200, u)
}

func (s *Server) superUsage(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 90 {
		days = 7
	}
	fresh := r.URL.Query().Has("fresh")
	machines := s.herdr.Machines()
	type machResult struct {
		name string
		u    NodeUsage
		err  string
	}
	ch := make(chan machResult, len(machines))
	for _, m := range machines {
		go func(m Machine) {
			if m.Local {
				u := s.localUsageCached(days, fresh)
				u.Machine = m.Name
				ch <- machResult{name: m.Name, u: u}
				return
			}
			if !m.Online {
				ch <- machResult{name: m.Name, err: "offline"}
				return
			}
			q := fmt.Sprintf("/api/node/usage?days=%d", days)
			if fresh {
				q += "&fresh=1"
			}
			var u NodeUsage
			cctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
			err := s.nodeJSON(cctx, m.ID, "GET", q, nil, &u)
			cancel()
			if err != nil {
				ch <- machResult{name: m.Name, err: err.Error()}
				return
			}
			u.Machine = m.Name
			ch <- machResult{name: m.Name, u: u}
		}(m)
	}

	total := TokenBucket{}
	byDay := map[string]TokenBucket{}
	byModel := map[string]TokenBucket{}
	byKind := map[string]TokenBucket{}
	byMachine := map[string]TokenBucket{}
	errorsMap := map[string]string{}

	for range machines {
		res := <-ch
		if res.err != "" {
			errorsMap[res.name] = res.err
			continue
		}
		total.add(res.u.Total)
		byMachine[res.name] = res.u.Total
		for k, v := range res.u.ByDay {
			b := byDay[k]
			b.add(v)
			byDay[k] = b
		}
		for k, v := range res.u.ByModel {
			b := byModel[k]
			b.add(v)
			byModel[k] = b
		}
		for k, v := range res.u.ByKind {
			b := byKind[k]
			b.add(v)
			byKind[k] = b
		}
	}

	// Persist daily snapshot into SQLite kv so historical trends survive log rotation.
	if s.sup != nil && s.sup.db != nil && len(byDay) > 0 {
		hist := map[string]TokenBucket{}
		var raw string
		if s.sup.db.QueryRow(`SELECT v FROM kv WHERE k='usage_daily'`).Scan(&raw) == nil {
			_ = json.Unmarshal([]byte(raw), &hist)
		}
		for day, v := range byDay {
			if prev, ok := hist[day]; !ok || v.Total >= prev.Total {
				hist[day] = v
			}
		}
		if b, err := json.Marshal(hist); err == nil {
			_, _ = s.sup.db.Exec(`INSERT INTO kv(k,v) VALUES('usage_daily',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, string(b))
		}
		// Fill any older days within requested window from persisted history.
		cutoff := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
		for day, v := range hist {
			if day >= cutoff && byDay[day].Total < v.Total {
				byDay[day] = v
			}
		}
	}

	dayKeys := make([]string, 0, len(byDay))
	for k := range byDay {
		dayKeys = append(dayKeys, k)
	}
	sort.Strings(dayKeys)
	daily := make([]map[string]any, 0, len(dayKeys))
	for _, k := range dayKeys {
		v := byDay[k]
		daily = append(daily, map[string]any{
			"date": k, "input": v.Input, "cached": v.Cached, "output": v.Output,
			"reasoning": v.Reasoning, "total": v.Total, "sessions": v.Sessions,
		})
	}

	writeJSON(w, 200, map[string]any{
		"days":       days,
		"today":      byDay[time.Now().Format("2006-01-02")],
		"total":      total,
		"daily":      daily,
		"by_machine": byMachine,
		"by_model":   byModel,
		"by_kind":    byKind,
		"pool_stats": poolStatsSnapshot(),
		"errors":     errorsMap,
	})
}

func (s *Server) superArchive(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       string `json:"id"`
		Archived bool   `json:"archived"`
	}
	if !decode(w, r, &body) {
		return
	}
	id := strings.TrimSpace(body.ID)
	if id == "" {
		writeJSON(w, 400, map[string]any{"error": "id is required"})
		return
	}
	sp := s.sup
	sp.mu.Lock()
	out := make([]string, 0, len(sp.st.Settings.ArchivedSessions)+1)
	for _, x := range sp.st.Settings.ArchivedSessions {
		if x != id {
			out = append(out, x)
		}
	}
	if body.Archived {
		out = append(out, id)
	}
	sp.st.Settings.ArchivedSessions = out
	sp.changed()
	sp.mu.Unlock()
	writeJSON(w, 200, map[string]any{"ok": true, "archived_sessions": out})
}

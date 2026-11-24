package swag

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type QuotaWindow struct {
	Label         string  `json:"label"`
	WindowMinutes int     `json:"window_minutes"`
	UsedPct       float64 `json:"used_percent"`
	RemainPct     float64 `json:"remain_percent"`
	ResetsAt      int64   `json:"resets_at,omitempty"`
}

type SubscriptionQuota struct {
	ID           string        `json:"id"`
	AccountKey   string        `json:"account_key,omitempty"`
	AccountLabel string        `json:"account_label,omitempty"`
	Machine      string        `json:"machine"`
	Machines     []string      `json:"machines,omitempty"`
	Kind         string        `json:"kind"` // codex | claude | agy
	Title        string        `json:"title"`
	Plan         string        `json:"plan,omitempty"`
	Status       string        `json:"status"` // ready | low | exhausted | idle
	QuotaNote    string        `json:"quota_note,omitempty"`
	Windows      []QuotaWindow `json:"windows,omitempty"`
	Models       []string      `json:"models,omitempty"`
	Credits      string        `json:"credits,omitempty"`
	Sessions7d   int           `json:"sessions_7d"`
	LastUsedAt   time.Time     `json:"last_used_at,omitempty"`
}

var localQuotaCache = struct {
	sync.Mutex
	at   time.Time
	list []SubscriptionQuota
}{}

func windowLabel(mins int) string {
	switch {
	case mins <= 0:
		return "窗口"
	case mins < 60:
		return strconv.Itoa(mins) + " 分钟"
	case mins < 1440:
		return strconv.Itoa(mins/60) + " 小时"
	default:
		return strconv.Itoa(mins/1440) + "D"
	}
}

func maskEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 1 {
		return email
	}
	keep := min(3, at)
	return email[:keep] + "***" + email[at:]
}

func readCodexAccountMeta(home string) (acctKey, acctLabel string) {
	root := envOr("CODEX_HOME", filepath.Join(home, ".codex"))
	b, err := os.ReadFile(filepath.Join(root, "auth.json"))
	if err != nil {
		return "", ""
	}
	var d struct {
		Tokens struct {
			AccountID string `json:"account_id"`
			IDToken   string `json:"id_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(b, &d) != nil {
		return "", ""
	}
	if d.Tokens.AccountID != "" {
		acctKey = "codex:" + d.Tokens.AccountID
	}
	if parts := strings.Split(d.Tokens.IDToken, "."); len(parts) == 3 {
		if raw, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			var claims struct {
				Email string `json:"email"`
			}
			if json.Unmarshal(raw, &claims) == nil && claims.Email != "" {
				acctLabel = maskEmail(claims.Email)
				if acctKey == "" {
					acctKey = "codex:" + claims.Email
				}
			}
		}
	}
	return acctKey, acctLabel
}

func parseCodexLatestQuota(home string) *SubscriptionQuota {
	root := envOr("CODEX_HOME", filepath.Join(home, ".codex"))
	files, err := filepath.Glob(filepath.Join(root, "sessions", "*", "*", "*", "rollout-*.jsonl"))
	if err != nil || len(files) == 0 {
		return nil
	}
	type fileMod struct {
		path string
		mod  time.Time
	}
	cutoff := time.Now().AddDate(0, 0, -7)
	var list []fileMod
	sessions7d := 0
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil {
			if !fi.ModTime().Before(cutoff) {
				sessions7d++
			}
			list = append(list, fileMod{path: f, mod: fi.ModTime()})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].mod.After(list[j].mod) })

	type rlWindow struct {
		UsedPercent   float64 `json:"used_percent"`
		WindowMinutes int     `json:"window_minutes"`
		ResetsAt      int64   `json:"resets_at"`
	}
	type rlPayload struct {
		LimitID   string    `json:"limit_id"`
		LimitName string    `json:"limit_name"`
		PlanType  string    `json:"plan_type"`
		Primary   *rlWindow `json:"primary"`
		Secondary *rlWindow `json:"secondary"`
		Credits   *struct {
			Balance string `json:"balance"`
		} `json:"credits"`
	}

	limitsByID := map[string]*rlPayload{}
	modelSet := map[string]bool{}
	var latestAt time.Time
	maxScan := min(len(list), 12)
	for _, fm := range list[:maxScan] {
		f, err := os.Open(fm.path)
		if err != nil {
			continue
		}
		fileLimits := map[string]*rlPayload{}
		r := bufio.NewReaderSize(f, 256<<10)
		for {
			line, rerr := r.ReadBytes('\n')
			if len(line) > 0 {
				s := string(line)
				if strings.Contains(s, `"turn_context"`) {
					var tc struct {
						Payload struct {
							Model string `json:"model"`
						} `json:"payload"`
					}
					if json.Unmarshal(line, &tc) == nil && tc.Payload.Model != "" {
						modelSet[tc.Payload.Model] = true
					}
				}
				if strings.Contains(s, `"rate_limits"`) {
					var ev struct {
						Payload struct {
							RateLimits *rlPayload `json:"rate_limits"`
						} `json:"payload"`
					}
					if json.Unmarshal(line, &ev) == nil && ev.Payload.RateLimits != nil &&
						(ev.Payload.RateLimits.Primary != nil || ev.Payload.RateLimits.Secondary != nil || ev.Payload.RateLimits.PlanType != "") {
						lid := ev.Payload.RateLimits.LimitID
						if lid == "" {
							lid = "codex"
						}
						fileLimits[lid] = ev.Payload.RateLimits
					}
				}
			}
			if rerr != nil {
				break
			}
		}
		f.Close()
		for lid, rl := range fileLimits {
			if _, exists := limitsByID[lid]; !exists {
				limitsByID[lid] = rl
				if latestAt.IsZero() {
					latestAt = fm.mod
				}
			}
		}
	}

	acctKey, acctLabel := readCodexAccountMeta(home)
	if acctKey == "" {
		acctKey = "codex:default"
	}
	var models []string
	for m := range modelSet {
		models = append(models, m)
	}
	sort.Strings(models)

	q := &SubscriptionQuota{
		AccountKey:   acctKey,
		AccountLabel: acctLabel,
		Kind:         "codex",
		Title:        "Codex · GPT 订阅",
		Status:       "ready",
		Models:       models,
		Sessions7d:   sessions7d,
		LastUsedAt:   list[0].mod,
	}
	if !latestAt.IsZero() {
		q.LastUsedAt = latestAt
	}

	lids := make([]string, 0, len(limitsByID))
	for k := range limitsByID {
		lids = append(lids, k)
	}
	sort.Strings(lids)
	for _, lid := range lids {
		rl := limitsByID[lid]
		if q.Plan == "" && rl.PlanType != "" {
			q.Plan = rl.PlanType
		}
		prefix := "GPT"
		if rl.LimitName != "" {
			prefix = rl.LimitName
		} else if lid != "codex" {
			prefix = lid
		}
		addWin := func(w *rlWindow) {
			if w == nil {
				return
			}
			rem := 100.0 - w.UsedPercent
			if rem < 0 {
				rem = 0
			}
			q.Windows = append(q.Windows, QuotaWindow{
				Label:         prefix + " " + windowLabel(w.WindowMinutes),
				WindowMinutes: w.WindowMinutes,
				UsedPct:       w.UsedPercent,
				RemainPct:     rem,
				ResetsAt:      w.ResetsAt,
			})
		}
		addWin(rl.Primary)
		addWin(rl.Secondary)
		if q.Credits == "" && rl.Credits != nil && strings.TrimSpace(rl.Credits.Balance) != "" {
			if v, err := strconv.ParseFloat(rl.Credits.Balance, 64); err == nil {
				q.Credits = strconv.FormatFloat(v, 'f', 0, 64)
			} else {
				q.Credits = rl.Credits.Balance
			}
		}
	}
	sort.Slice(q.Windows, func(i, j int) bool { return q.Windows[i].WindowMinutes < q.Windows[j].WindowMinutes })
	for _, w := range q.Windows {
		if w.RemainPct <= 1 {
			q.Status = "exhausted"
			break
		}
		if w.RemainPct < 20 && q.Status == "ready" {
			q.Status = "low"
		}
	}
	return q
}

func detectAgyFamilies(home string) []string {
	bin := findBin("agy", home)
	if bin == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "models").Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var fams []string
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		label := strings.TrimSpace(parts[1])
		if i := strings.IndexByte(label, '('); i > 0 {
			label = strings.TrimSpace(label[:i])
		}
		if label != "" && !seen[label] {
			seen[label] = true
			fams = append(fams, label)
		}
	}
	return fams
}

type proxyUsageSnapshot struct {
	RemainPct *float64
	Summary   string
}

var proxyUsageCache = struct {
	sync.Mutex
	at   time.Time
	snap *proxyUsageSnapshot
}{}

func parseProxyUsageOutput(out string) *proxyUsageSnapshot {
	var resets, basicDesc string
	var remPct *float64
	unlimitedAdv := false
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if idx := strings.Index(line, "resets "); idx >= 0 {
			rest := strings.TrimSuffix(strings.TrimSpace(line[idx+7:]), ")")
			if len(rest) >= 16 && rest[4] == '-' {
				rest = rest[5:16]
			}
			resets = rest + " 重置"
		}
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == "basic" && fields[2] == "/" {
			used, err1 := strconv.ParseFloat(fields[1], 64)
			total, err2 := strconv.ParseFloat(fields[3], 64)
			if err1 == nil && err2 == nil && total > 0 {
				rem := 100.0 * (1.0 - used/total)
				if rem < 0 {
					rem = 0
				}
				remPct = &rem
				left := int(total - used)
				if left < 0 {
					left = 0
				}
				basicDesc = "basic " + strconv.Itoa(left) + "/" + strconv.Itoa(int(total))
			}
		}
		if len(fields) >= 3 && fields[0] == "advanced" && strings.Contains(line, "unlimited") {
			unlimitedAdv = true
		}
	}
	if remPct == nil && resets == "" {
		return nil
	}
	var parts []string
	if basicDesc != "" {
		parts = append(parts, basicDesc)
	}
	if unlimitedAdv {
		parts = append(parts, "advanced 不限")
	}
	if resets != "" {
		parts = append(parts, resets)
	}
	return &proxyUsageSnapshot{RemainPct: remPct, Summary: strings.Join(parts, " · ")}
}

func cachedProxyUsage(fresh bool) *proxyUsageSnapshot {
	cmdStr := strings.TrimSpace(os.Getenv("SWAG_PROXY_USAGE_CMD"))
	if cmdStr == "" {
		return nil
	}
	proxyUsageCache.Lock()
	if !fresh && time.Since(proxyUsageCache.at) < 60*time.Second && proxyUsageCache.snap != nil {
		cp := *proxyUsageCache.snap
		proxyUsageCache.Unlock()
		return &cp
	}
	proxyUsageCache.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/sh", "-c", cmdStr).Output()
	if err != nil {
		return nil
	}
	snap := parseProxyUsageOutput(string(out))
	if snap != nil {
		proxyUsageCache.Lock()
		proxyUsageCache.at = time.Now()
		proxyUsageCache.snap = snap
		proxyUsageCache.Unlock()
	}
	return snap
}

func scanLocalQuotas(home string) []SubscriptionQuota {
	var out []SubscriptionQuota
	if cq := parseCodexLatestQuota(home); cq != nil {
		out = append(out, *cq)
	}
	cutoff := time.Now().AddDate(0, 0, -7)
	if findBin("claude", home) != "" {
		claudeRoot := envOr("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
		files, _ := filepath.Glob(filepath.Join(claudeRoot, "projects", "*", "*.jsonl"))
		n := 0
		var last time.Time
		for _, f := range files {
			if fi, err := os.Stat(f); err == nil {
				if fi.ModTime().After(last) {
					last = fi.ModTime()
				}
				if !fi.ModTime().Before(cutoff) {
					n++
				}
			}
		}
		out = append(out, SubscriptionQuota{
			AccountKey: "claude:local",
			Kind:       "claude",
			Title:      "Claude Code",
			Plan:       "CLI",
			Status:     "ready",
			QuotaNote:  "Claude CLI 未开放本地剩余百分比接口",
			Sessions7d: n,
			LastUsedAt: last,
		})
	}
	if findBin("agy", home) != "" {
		brain := filepath.Join(home, ".gemini", "antigravity", "brain")
		entries, _ := os.ReadDir(brain)
		n := 0
		var last time.Time
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if fi, err := e.Info(); err == nil {
				if fi.ModTime().After(last) {
					last = fi.ModTime()
				}
				if !fi.ModTime().Before(cutoff) {
					n++
				}
			}
		}
		out = append(out, SubscriptionQuota{
			AccountKey: "agy:gemini",
			Kind:       "agy",
			Title:      "Antigravity · Gemini 订阅",
			Plan:       "Gemini",
			Status:     "ready",
			QuotaNote:  "Gemini/Antigravity 客户端未开放本地剩余百分比接口（展示真实可用模型族）",
			Models:     detectAgyFamilies(home),
			Sessions7d: n,
			LastUsedAt: last,
		})
	}
	return out
}

func (s *Server) localQuotasCached(fresh bool) []SubscriptionQuota {
	localQuotaCache.Lock()
	if !fresh && time.Since(localQuotaCache.at) < 60*time.Second && localQuotaCache.list != nil {
		cp := append([]SubscriptionQuota(nil), localQuotaCache.list...)
		localQuotaCache.Unlock()
		return cp
	}
	localQuotaCache.Unlock()
	list := scanLocalQuotas(s.home)
	localQuotaCache.Lock()
	localQuotaCache.at, localQuotaCache.list = time.Now(), list
	localQuotaCache.Unlock()
	return list
}

func (s *Server) nodeQuota(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"subscriptions": s.localQuotasCached(r.URL.Query().Has("fresh"))})
}

func mergeSubscriptionQuotas(raw []SubscriptionQuota) []SubscriptionQuota {
	byKey := map[string]*SubscriptionQuota{}
	var order []string
	for _, s := range raw {
		key := s.AccountKey
		if key == "" {
			key = s.Kind + ":" + s.Machine
		}
		cur, ok := byKey[key]
		if !ok {
			cp := s
			cp.ID = key
			cp.Machines = []string{s.Machine}
			byKey[key] = &cp
			order = append(order, key)
			continue
		}
		if s.Machine != "" && !contains(cur.Machines, s.Machine) {
			cur.Machines = append(cur.Machines, s.Machine)
		}
		cur.Sessions7d += s.Sessions7d
		if cur.AccountLabel == "" && s.AccountLabel != "" {
			cur.AccountLabel = s.AccountLabel
		}
		if cur.Plan == "" && s.Plan != "" {
			cur.Plan = s.Plan
		}
		for _, m := range s.Models {
			if !contains(cur.Models, m) {
				cur.Models = append(cur.Models, m)
			}
		}
		sort.Strings(cur.Models)
		if s.LastUsedAt.After(cur.LastUsedAt) {
			cur.LastUsedAt = s.LastUsedAt
			if len(s.Windows) > 0 {
				cur.Windows = s.Windows
				cur.Status = s.Status
			}
			if s.Credits != "" {
				cur.Credits = s.Credits
			}
		} else if len(cur.Windows) == 0 && len(s.Windows) > 0 {
			cur.Windows = s.Windows
			cur.Status = s.Status
		}
		if cur.Credits == "" && s.Credits != "" {
			cur.Credits = s.Credits
		}
	}
	out := make([]SubscriptionQuota, 0, len(order))
	for _, k := range order {
		sort.Strings(byKey[k].Machines)
		out = append(out, *byKey[k])
	}
	sort.Slice(out, func(i, j int) bool {
		rank := map[string]int{"codex": 0, "agy": 1, "claude": 2}
		if rank[out[i].Kind] != rank[out[j].Kind] {
			return rank[out[i].Kind] < rank[out[j].Kind]
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (s *Server) superQuota(w http.ResponseWriter, r *http.Request) {
	fresh := r.URL.Query().Has("fresh")
	machines := s.herdr.Machines()
	type mres struct {
		subs []SubscriptionQuota
	}
	ch := make(chan mres, len(machines))
	for _, m := range machines {
		go func(m Machine) {
			if m.Local {
				subs := s.localQuotasCached(fresh)
				for i := range subs {
					subs[i].Machine = m.Name
				}
				ch <- mres{subs: subs}
				return
			}
			if !m.Online {
				ch <- mres{}
				return
			}
			q := "/api/node/quota"
			if fresh {
				q += "?fresh=1"
			}
			var res struct {
				Subscriptions []SubscriptionQuota `json:"subscriptions"`
			}
			cctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			err := s.nodeJSON(cctx, m.ID, "GET", q, nil, &res)
			cancel()
			if err != nil {
				ch <- mres{}
				return
			}
			for i := range res.Subscriptions {
				res.Subscriptions[i].Machine = m.Name
			}
			ch <- mres{subs: res.Subscriptions}
		}(m)
	}
	var all []SubscriptionQuota
	for range machines {
		all = append(all, (<-ch).subs...)
	}
	poolView := maskPoolConfig(loadPoolConfig())
	if snap := cachedProxyUsage(fresh); snap != nil {
		if provs, ok := poolView["providers"].([]map[string]any); ok {
			for _, p := range provs {
				if base, _ := p["base_url"].(string); strings.Contains(base, "127.0.0.1") || strings.Contains(base, "localhost") {
					if rt, ok := p["runtime"].(ProviderRuntime); ok && rt.QuotaPct == nil {
						rt.QuotaPct = snap.RemainPct
						rt.QuotaReset = snap.Summary
						p["runtime"] = rt
					}
				}
			}
		}
	}
	writeJSON(w, 200, map[string]any{
		"subscriptions": mergeSubscriptionQuotas(all),
		"pool":          poolView,
	})
}

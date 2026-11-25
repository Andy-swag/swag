package swag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type PoolProvider struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Kind         string            `json:"kind"` // openai | gateway | proxy | custom
	BaseURL      string            `json:"base_url"`
	ConsoleURL   string            `json:"console_url,omitempty"`
	APIKey       string            `json:"api_key,omitempty"`
	ProxyURL     string            `json:"proxy_url,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Enabled      bool              `json:"enabled"`
	Priority     int               `json:"priority"`
	CooldownSec  int               `json:"cooldown_sec,omitempty"`
	ModelMap     map[string]string `json:"model_map,omitempty"`
	CachedModels []string          `json:"cached_models,omitempty"`
}

type ModelTier struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Providers   []string `json:"providers"`
}

type PoolConfig struct {
	Providers      []PoolProvider `json:"providers"`
	Tiers          []ModelTier    `json:"tiers"`
	NativeFallback *bool          `json:"native_fallback,omitempty"`
}

func (c PoolConfig) nativeFallbackEnabled() bool {
	return c.NativeFallback == nil || *c.NativeFallback
}

type ProviderRuntime struct {
	Requests      int64     `json:"requests"`
	Successes     int64     `json:"successes"`
	Failovers     int64     `json:"failovers"`
	InputTokens   int64     `json:"input_tokens,omitempty"`
	OutputTokens  int64     `json:"output_tokens,omitempty"`
	TotalTokens   int64     `json:"total_tokens,omitempty"`
	LastStatus    int       `json:"last_status,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	LastUsedAt    time.Time `json:"last_used_at,omitempty"`
	CooldownUntil time.Time `json:"cooldown_until,omitempty"`
	QuotaPct      *float64  `json:"quota_pct,omitempty"`
	QuotaReset    string    `json:"quota_reset,omitempty"`
}

var (
	poolMu         sync.Mutex
	poolRuntime    = map[string]*ProviderRuntime{}
	poolRuntimeDir string
)

func poolPath() string      { return filepath.Join(swagDir(), "pool.json") }
func poolStatsPath() string { return filepath.Join(swagDir(), "pool-stats.json") }

// ensurePoolRuntimeLoadedLocked loads persisted per-provider counters for the
// active swagDir() once so Hub restarts/upgrades never reset model pool stats.
func ensurePoolRuntimeLoadedLocked() {
	dir := swagDir()
	if poolRuntimeDir == dir {
		return
	}
	poolRuntimeDir = dir
	poolRuntime = map[string]*ProviderRuntime{}
	b, err := os.ReadFile(filepath.Join(dir, "pool-stats.json"))
	if err != nil {
		return
	}
	var saved map[string]*ProviderRuntime
	if json.Unmarshal(b, &saved) == nil && saved != nil {
		for k, v := range saved {
			if k != "" && v != nil {
				poolRuntime[k] = v
			}
		}
	}
}

func savePoolRuntimeLocked() {
	dir := swagDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	b, err := json.MarshalIndent(poolRuntime, "", "  ")
	if err != nil {
		return
	}
	p := filepath.Join(dir, "pool-stats.json")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err == nil {
		_ = os.Rename(tmp, p)
	}
}

func defaultPoolConfig() PoolConfig {
	return PoolConfig{
		Providers: []PoolProvider{
			{
				ID:          "openai-primary",
				Name:        "主 API 网关（OpenAI 兼容）",
				Kind:        "gateway",
				BaseURL:     "https://api.openai.com/v1",
				Enabled:     false,
				Priority:    10,
				CooldownSec: 300,
				ModelMap: map[string]string{
					"tier-fast":     "gpt-4.1-mini",
					"tier-standard": "gpt-4.1",
					"tier-strong":   "o3",
				},
			},
			{
				ID:          "openai-backup",
				Name:        "备用上游（OpenAI 兼容）",
				Kind:        "openai",
				BaseURL:     "https://openrouter.ai/api/v1",
				Enabled:     false,
				Priority:    20,
				CooldownSec: 300,
				ModelMap:    map[string]string{},
			},
			{
				ID:          "local-proxy",
				Name:        "本地反代 / 自托管网关（可选）",
				Kind:        "proxy",
				BaseURL:     "http://127.0.0.1:8321/v1",
				Enabled:     false,
				Priority:    30,
				CooldownSec: 300,
				ModelMap:    map[string]string{},
			},
		},
		Tiers: []ModelTier{
			{ID: "tier-fast", Label: "轻量快速", Description: "简单问答、只读巡检与日常摘要（ops-auditor）", Providers: []string{"openai-primary", "openai-backup", "local-proxy"}},
			{ID: "tier-standard", Label: "标准主力", Description: "日常编码、调试与常规派活（dev-worker）", Providers: []string{"openai-primary", "openai-backup", "local-proxy"}},
			{ID: "tier-strong", Label: "高配旗舰", Description: "复杂架构、深度排查与夜间做梦（infra-worker / dream）", Providers: []string{"openai-primary", "openai-backup", "local-proxy"}},
		},
	}
}

func loadPoolConfig() PoolConfig {
	poolMu.Lock()
	defer poolMu.Unlock()
	b, err := os.ReadFile(poolPath())
	if err != nil {
		return defaultPoolConfig()
	}
	var cfg PoolConfig
	if json.Unmarshal(b, &cfg) != nil || (len(cfg.Providers) == 0 && len(cfg.Tiers) == 0) {
		return defaultPoolConfig()
	}
	if len(cfg.Tiers) == 0 {
		cfg.Tiers = defaultPoolConfig().Tiers
	}
	return cfg
}

func savePoolConfig(cfg PoolConfig) error {
	poolMu.Lock()
	defer poolMu.Unlock()
	if err := os.MkdirAll(swagDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := poolPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, poolPath())
}

func poolStatsSnapshot() map[string]ProviderRuntime {
	cfg := loadPoolConfig()
	poolMu.Lock()
	defer poolMu.Unlock()
	ensurePoolRuntimeLoadedLocked()
	out := make(map[string]ProviderRuntime, len(poolRuntime)+len(cfg.Providers))
	for _, p := range cfg.Providers {
		if p.Enabled {
			out[p.ID] = ProviderRuntime{}
		}
	}
	for k, v := range poolRuntime {
		out[k] = *v
	}
	return out
}

func recordPoolUsageTokens(id string, tail []byte) {
	if len(tail) == 0 {
		return
	}
	s := string(tail)
	idx := strings.LastIndex(s, `"usage"`)
	if idx < 0 {
		return
	}
	rest := s[idx+7:]
	start := strings.IndexByte(rest, '{')
	if start < 0 {
		return
	}
	dec := json.NewDecoder(strings.NewReader(rest[start:]))
	var u struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		InputTokens      int64 `json:"input_tokens"`
		OutputTokens     int64 `json:"output_tokens"`
		TotalTokens      int64 `json:"total_tokens"`
	}
	if dec.Decode(&u) != nil {
		return
	}
	inTok := max(u.PromptTokens, u.InputTokens)
	outTok := max(u.CompletionTokens, u.OutputTokens)
	tot := u.TotalTokens
	if tot == 0 {
		tot = inTok + outTok
	}
	if tot <= 0 {
		return
	}
	poolMu.Lock()
	rt := runtimeFor(id)
	rt.InputTokens += inTok
	rt.OutputTokens += outTok
	rt.TotalTokens += tot
	savePoolRuntimeLocked()
	poolMu.Unlock()
}

func runtimeFor(id string) *ProviderRuntime {
	ensurePoolRuntimeLoadedLocked()
	r, ok := poolRuntime[id]
	if !ok {
		r = &ProviderRuntime{}
		poolRuntime[id] = r
	}
	return r
}

func maskPoolConfig(cfg PoolConfig) map[string]any {
	stats := poolStatsSnapshot()
	provs := make([]map[string]any, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		maskedKey := ""
		if p.APIKey != "" {
			maskedKey = maskSecret("api_key", p.APIKey)
		}
		rt := stats[p.ID]
		provs = append(provs, map[string]any{
			"id":            p.ID,
			"name":          p.Name,
			"kind":          p.Kind,
			"base_url":      p.BaseURL,
			"console_url":   p.ConsoleURL,
			"api_key":       maskedKey,
			"has_key":       p.APIKey != "",
			"proxy_url":     p.ProxyURL,
			"enabled":       p.Enabled,
			"priority":      p.Priority,
			"cooldown_sec":  p.CooldownSec,
			"model_map":     p.ModelMap,
			"cached_models": p.CachedModels,
			"runtime":       rt,
			"cooling":       time.Now().Before(rt.CooldownUntil),
		})
	}
	return map[string]any{
		"providers":       provs,
		"tiers":           cfg.Tiers,
		"native_fallback": cfg.nativeFallbackEnabled(),
	}
}

func pickPoolCandidates(cfg PoolConfig, requestedModel string) []PoolProvider {
	byID := map[string]PoolProvider{}
	for _, p := range cfg.Providers {
		if p.Enabled && strings.TrimSpace(p.BaseURL) != "" {
			byID[p.ID] = p
		}
	}
	var ordered []PoolProvider
	seen := map[string]bool{}
	for _, tier := range cfg.Tiers {
		if tier.ID == requestedModel {
			for _, pid := range tier.Providers {
				if p, ok := byID[pid]; ok && !seen[pid] {
					ordered = append(ordered, p)
					seen[pid] = true
				}
			}
			break
		}
	}
	if len(ordered) == 0 {
		for _, p := range cfg.Providers {
			if p.Enabled && strings.TrimSpace(p.BaseURL) != "" {
				ordered = append(ordered, p)
			}
		}
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Priority < ordered[j].Priority })
	}
	now := time.Now()
	poolMu.Lock()
	ensurePoolRuntimeLoadedLocked()
	var ready, cooling []PoolProvider
	for _, p := range ordered {
		if rt := poolRuntime[p.ID]; rt != nil && now.Before(rt.CooldownUntil) {
			cooling = append(cooling, p)
		} else {
			ready = append(ready, p)
		}
	}
	poolMu.Unlock()
	if len(ready) > 0 {
		return ready
	}
	return cooling
}

func isFailoverStatus(code int) bool {
	return code == 402 || code == 403 || code == 429 || code >= 500
}

func updateQuotaFromHeaders(rt *ProviderRuntime, h http.Header) {
	parseRatio := func(remKey, limKey string) (float64, bool) {
		r, err1 := parseFloatHeader(h.Get(remKey))
		l, err2 := parseFloatHeader(h.Get(limKey))
		if err1 == nil && err2 == nil && l > 0 {
			pct := (r / l) * 100
			if pct < 0 {
				pct = 0
			} else if pct > 100 {
				pct = 100
			}
			return pct, true
		}
		return 0, false
	}
	if pct, ok := parseRatio("x-ratelimit-remaining-tokens", "x-ratelimit-limit-tokens"); ok {
		rt.QuotaPct = &pct
	} else if pct, ok := parseRatio("x-ratelimit-remaining-requests", "x-ratelimit-limit-requests"); ok {
		rt.QuotaPct = &pct
	}
	if reset := strings.TrimSpace(h.Get("x-ratelimit-reset-tokens")); reset != "" {
		rt.QuotaReset = reset
	} else if reset := strings.TrimSpace(h.Get("x-ratelimit-reset-requests")); reset != "" {
		rt.QuotaReset = reset
	}
}

func parseFloatHeader(s string) (float64, error) {
	var v float64
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%f", &v)
	return v, err
}

var (
	poolClientsMu sync.Mutex
	poolClients   = map[string]*http.Client{}
)

func poolHTTPClient(proxyRaw string) *http.Client {
	key := strings.TrimSpace(proxyRaw)
	poolClientsMu.Lock()
	defer poolClientsMu.Unlock()
	if c, ok := poolClients[key]; ok {
		return c
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 90 * time.Second
	if key != "" {
		if u, err := url.Parse(key); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	c := &http.Client{Transport: tr}
	poolClients[key] = c
	return c
}

func (s *Server) proxyPoolRequest(w http.ResponseWriter, r *http.Request, suffix string) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	var payload map[string]any
	requestedModel := ""
	if json.Unmarshal(raw, &payload) == nil {
		if m, ok := payload["model"].(string); ok {
			requestedModel = m
		}
	}
	cfg := loadPoolConfig()
	cands := pickPoolCandidates(cfg, requestedModel)
	if len(cands) == 0 {
		writeJSON(w, 503, map[string]any{"error": "模型池暂无已启用的 Provider，请在「自动化 → 模型池」启用并配置上游"})
		return
	}

	var lastErr string
	lastCode := 502
	for idx, p := range cands {
		bodyBytes := raw
		if payload != nil && requestedModel != "" {
			if mapped := strings.TrimSpace(p.ModelMap[requestedModel]); mapped != "" && mapped != requestedModel {
				cp := make(map[string]any, len(payload))
				for k, v := range payload {
					cp[k] = v
				}
				cp["model"] = mapped
				if b, err := json.Marshal(cp); err == nil {
					bodyBytes = b
				}
			}
		}
		targetURL := strings.TrimRight(p.BaseURL, "/") + suffix
		req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bytes.NewReader(bodyBytes))
		if err != nil {
			lastErr = err.Error()
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if p.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+p.APIKey)
		} else if auth := r.Header.Get("Authorization"); auth != "" {
			req.Header.Set("Authorization", auth)
		}
		for k, v := range p.Headers {
			req.Header.Set(k, v)
		}

		poolMu.Lock()
		rt := runtimeFor(p.ID)
		rt.Requests++
		rt.LastUsedAt = time.Now()
		poolMu.Unlock()

		resp, err := poolHTTPClient(p.ProxyURL).Do(req)
		if err != nil {
			poolMu.Lock()
			rt := runtimeFor(p.ID)
			rt.Failovers++
			rt.LastError = err.Error()
			rt.CooldownUntil = time.Now().Add(time.Duration(max(p.CooldownSec, 60)) * time.Second)
			savePoolRuntimeLocked()
			poolMu.Unlock()
			lastErr = p.ID + ": " + err.Error()
			continue
		}

		if isFailoverStatus(resp.StatusCode) {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			poolMu.Lock()
			rt := runtimeFor(p.ID)
			rt.Failovers++
			rt.LastStatus = resp.StatusCode
			rt.LastError = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, clip(oneLine(string(errBody)), 180))
			rt.CooldownUntil = time.Now().Add(time.Duration(max(p.CooldownSec, 180)) * time.Second)
			savePoolRuntimeLocked()
			poolMu.Unlock()
			lastCode = resp.StatusCode
			lastErr = p.ID + " " + rt.LastError
			if idx+1 < len(cands) {
				continue
			}
			break
		}

		poolMu.Lock()
		rt = runtimeFor(p.ID)
		rt.LastStatus = resp.StatusCode
		updateQuotaFromHeaders(rt, resp.Header)
		if resp.StatusCode < 400 {
			rt.Successes++
			rt.LastError = ""
			rt.CooldownUntil = time.Time{}
		}
		savePoolRuntimeLocked()
		poolMu.Unlock()

		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.Header().Set("X-Swag-Provider", p.ID)
		w.WriteHeader(resp.StatusCode)
		var tail []byte
		appendTail := func(chunk []byte) {
			const maxTail = 16384
			if len(chunk) >= maxTail {
				tail = append(tail[:0], chunk[len(chunk)-maxTail:]...)
				return
			}
			tail = append(tail, chunk...)
			if len(tail) > maxTail {
				tail = append(tail[:0], tail[len(tail)-maxTail:]...)
			}
		}
		fl, canFlush := w.(http.Flusher)
		buf := make([]byte, 32*1024)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				if canFlush {
					fl.Flush()
				}
				appendTail(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
		resp.Body.Close()
		if resp.StatusCode < 400 {
			recordPoolUsageTokens(p.ID, tail)
		}
		return
	}
	writeJSON(w, lastCode, map[string]any{"error": "模型池所有上游均不可用：" + lastErr})
}

func (s *Server) poolModels(w http.ResponseWriter, r *http.Request) {
	cfg := loadPoolConfig()
	data := make([]map[string]any, 0, len(cfg.Tiers))
	now := time.Now().Unix()
	for _, t := range cfg.Tiers {
		data = append(data, map[string]any{"id": t.ID, "object": "model", "created": now, "owned_by": "swag-pool"})
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": data})
}

func (s *Server) poolResponses(w http.ResponseWriter, r *http.Request) {
	s.proxyPoolRequest(w, r, "/responses")
}

func (s *Server) poolChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.proxyPoolRequest(w, r, "/chat/completions")
}

func (s *Server) superPoolGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, maskPoolConfig(loadPoolConfig()))
}

func (s *Server) superPoolSave(w http.ResponseWriter, r *http.Request) {
	var body PoolConfig
	if !decode(w, r, &body) {
		return
	}
	old := loadPoolConfig()
	oldKeys := map[string]string{}
	oldModels := map[string][]string{}
	oldHeaders := map[string]map[string]string{}
	oldConsole := map[string]string{}
	for _, p := range old.Providers {
		oldKeys[p.ID] = p.APIKey
		oldModels[p.ID] = p.CachedModels
		oldHeaders[p.ID] = p.Headers
		oldConsole[p.ID] = p.ConsoleURL
	}
	for i := range body.Providers {
		p := &body.Providers[i]
		p.ID = strings.TrimSpace(p.ID)
		p.BaseURL = strings.TrimSpace(p.BaseURL)
		if strings.Contains(p.APIKey, "••••") {
			p.APIKey = oldKeys[p.ID]
		}
		if len(p.CachedModels) == 0 && len(oldModels[p.ID]) > 0 {
			p.CachedModels = oldModels[p.ID]
		}
		if len(p.Headers) == 0 && len(oldHeaders[p.ID]) > 0 {
			p.Headers = oldHeaders[p.ID]
		}
		if p.ConsoleURL == "" && oldConsole[p.ID] != "" {
			p.ConsoleURL = oldConsole[p.ID]
		}
	}
	if err := savePoolConfig(body); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, maskPoolConfig(body))
}

func extractUpstreamModelIDs(raw []byte) []string {
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, d := range list.Data {
		add(d.ID)
	}
	for _, m := range list.Models {
		if m.ID != "" {
			add(m.ID)
		} else {
			add(m.Slug)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Server) superPoolProbe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       string        `json:"id"`
		Provider *PoolProvider `json:"provider,omitempty"`
	}
	if !decode(w, r, &body) {
		return
	}
	cfg := loadPoolConfig()
	var target *PoolProvider
	idx := -1
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == body.ID {
			cp := cfg.Providers[i]
			target = &cp
			idx = i
			break
		}
	}
	if body.Provider != nil {
		p := *body.Provider
		if target != nil {
			if strings.Contains(p.APIKey, "••••") {
				p.APIKey = target.APIKey
			}
			if len(p.Headers) == 0 && len(target.Headers) > 0 {
				p.Headers = target.Headers
			}
		}
		target = &p
	}
	if target == nil || strings.TrimSpace(target.BaseURL) == "" {
		writeJSON(w, 404, map[string]any{"ok": false, "error": "未找到 Provider 或 Base URL 为空"})
		return
	}
	modelsURL := strings.TrimRight(target.BaseURL, "/") + "/models"
	cctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, "GET", modelsURL, nil)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if target.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+target.APIKey)
	}
	for k, v := range target.Headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := poolHTTPClient(target.ProxyURL).Do(req)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	lat := time.Since(start).Milliseconds()
	models := extractUpstreamModelIDs(raw)
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	if ok && idx >= 0 && len(models) > 0 {
		cfg.Providers[idx].CachedModels = models
		_ = savePoolConfig(cfg)
	}
	errMsg := ""
	if !ok {
		errMsg = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, clip(oneLine(string(raw)), 140))
	}
	writeJSON(w, 200, map[string]any{
		"ok":         ok,
		"status":     resp.StatusCode,
		"latency_ms": lat,
		"models":     models,
		"error":      errMsg,
	})
}

func (s *Server) superPoolResetCooldown(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	poolMu.Lock()
	ensurePoolRuntimeLoadedLocked()
	if body.ID == "" {
		for _, rt := range poolRuntime {
			rt.CooldownUntil = time.Time{}
			rt.LastError = ""
		}
	} else if rt, ok := poolRuntime[body.ID]; ok {
		rt.CooldownUntil = time.Time{}
		rt.LastError = ""
	}
	savePoolRuntimeLocked()
	poolMu.Unlock()
	writeJSON(w, 200, maskPoolConfig(loadPoolConfig()))
}

func isPoolAuthProfile(profileName string) bool {
	name := strings.TrimSpace(profileName)
	if name == "" || name == "default" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(name), "swag-pool") {
		return true
	}
	env := getAuthProfileEnv(name)
	for _, k := range []string{"OPENAI_BASE_URL", "ANTHROPIC_BASE_URL"} {
		u := strings.ToLower(strings.TrimSpace(env[k]))
		if strings.Contains(u, ":7788/v1") || strings.Contains(u, ":17788/v1") {
			return true
		}
	}
	return false
}

func poolHasReadyProvider(cfg PoolConfig, tier string) bool {
	byID := map[string]PoolProvider{}
	for _, p := range cfg.Providers {
		if p.Enabled && strings.TrimSpace(p.BaseURL) != "" {
			byID[p.ID] = p
		}
	}
	var cands []PoolProvider
	for _, t := range cfg.Tiers {
		if t.ID == tier {
			for _, pid := range t.Providers {
				if p, ok := byID[pid]; ok {
					cands = append(cands, p)
				}
			}
			break
		}
	}
	if len(cands) == 0 {
		for _, p := range byID {
			cands = append(cands, p)
		}
	}
	now := time.Now()
	poolMu.Lock()
	defer poolMu.Unlock()
	ensurePoolRuntimeLoadedLocked()
	for _, p := range cands {
		if rt := poolRuntime[p.ID]; rt == nil || !now.Before(rt.CooldownUntil) {
			return true
		}
	}
	return false
}

func isPoolFailureScreen(screen string) bool {
	low := strings.ToLower(screen)
	for _, pat := range []string{
		"429", "502", "503", "504", "rate limit", "quota", "too many requests",
		"connection refused", "模型池", "upstream", "bad gateway", "service unavailable",
		"unauthorized", "401", "403", "exceeded",
	} {
		if strings.Contains(low, pat) {
			return true
		}
	}
	return false
}

func resolveNativeFallback(authProfile, model *string) bool {
	if !isPoolAuthProfile(*authProfile) {
		return false
	}
	cfg := loadPoolConfig()
	if !cfg.nativeFallbackEnabled() || poolHasReadyProvider(cfg, *model) {
		return false
	}
	*authProfile = ""
	if strings.HasPrefix(*model, "tier-") {
		*model = ""
	}
	return true
}

func (sp *Super) tryNativeFallback(w *Worker, screen, reason string) bool {
	if w == nil {
		return false
	}
	sp.mu.Lock()
	already := w.FallbackUsed
	authProf := w.AuthProfile
	sp.mu.Unlock()
	if already || !isPoolAuthProfile(authProf) {
		return false
	}
	cfg := loadPoolConfig()
	if !cfg.nativeFallbackEnabled() {
		return false
	}
	if screen != "" && !isPoolFailureScreen(screen) {
		return false
	}
	sp.mu.Lock()
	if w.FallbackUsed {
		sp.mu.Unlock()
		return false
	}
	w.FallbackUsed = true
	sp.mu.Unlock()
	nextModel := w.Model
	if strings.HasPrefix(nextModel, "tier-") {
		nextModel = ""
	}
	retryName := w.Name + "-native"
	go func() {
		nw, _, err := sp.SpawnWithOptions(w.Kind, w.Cwd, w.Task, w.Thread, retryName, w.CreatedBy, w.Machine, nextModel, w.Effort, false, "", SpawnOptions{
			Role:        w.Role,
			MachineSet:  true,
			ForceNative: true,
		})
		sp.mu.Lock()
		if err != nil {
			sp.addEntryLocked("event", w.Thread, w.Name+" 切换原生订阅兜底失败："+err.Error(), w.Name, "error")
		} else {
			msg := w.Name + " 反代池异常"
			if reason != "" {
				msg += "（" + reason + "）"
			}
			msg += "，已自动切换原生订阅直连重跑（" + nw.Name + "）"
			sp.addEntryLocked("event", w.Thread, msg, nw.Name, "spawn")
		}
		sp.changed()
		sp.mu.Unlock()
	}()
	return true
}

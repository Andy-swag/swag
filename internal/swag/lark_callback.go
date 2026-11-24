package swag

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	larkCallbackActive atomic.Bool
	larkCardSeenMu     sync.Mutex
	larkCardSeen       = map[string]time.Time{}
	larkCardSumMu      sync.Mutex
	larkCardSum        = map[string]string{}
)

func rememberLarkSummary(re, summary string) {
	if re == "" || summary == "" {
		return
	}
	larkCardSumMu.Lock()
	if len(larkCardSum) > 200 {
		larkCardSum = map[string]string{}
	}
	larkCardSum[re] = summary
	larkCardSumMu.Unlock()
}

func lookupLarkSummary(re string) string {
	larkCardSumMu.Lock()
	defer larkCardSumMu.Unlock()
	return larkCardSum[re]
}

func hubFallbackBaseURL() string {
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range ifaces {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP == nil || ipNet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipNet.IP.To4(); ip4 != nil {
			return "http://" + ip4.String() + ":7788"
		}
	}
	return ""
}

func (s *Server) startLarkCardCallbackLoop(ctx context.Context, hubBase string) {
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		bin, _, _, problem := lark.get(false)
		if problem != "" || bin == "" {
			larkCallbackActive.Store(false)
			if sleepCtx(ctx, 2*time.Minute) != nil {
				return
			}
			continue
		}
		started := time.Now()
		err := s.runLarkCardConsumer(ctx, bin, hubBase)
		larkCallbackActive.Store(false)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > time.Minute {
			backoff = 5 * time.Second
		} else if backoff < 2*time.Minute {
			backoff *= 2
		}
		if err != nil {
			log.Printf("lark card callback consumer exited: %v (retry in %s)", err, backoff)
		}
		if sleepCtx(ctx, backoff) != nil {
			return
		}
	}
}

func (s *Server) runLarkCardConsumer(ctx context.Context, bin, hubBase string) error {
	cmd := exec.CommandContext(ctx, bin, "event", "consume", "card.action.trigger", "--as", "bot")
	stdinR, stdinW := io.Pipe()
	cmd.Stdin = stdinR
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdinW.Close()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdinW.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		stdinW.Close()
		return err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			stdinW.Close()
		case <-done:
			stdinW.Close()
		}
	}()
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			if strings.Contains(line, "[event] ready") && strings.Contains(line, "card.action.trigger") {
				larkCallbackActive.Store(true)
				log.Printf("lark card callback consumer ready")
			}
		}
	}()
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 2<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		go s.handleLarkCardEvent(ctx, bin, hubBase, line)
	}
	err = cmd.Wait()
	close(done)
	return err
}

type larkCardAction struct {
	EventID string
	Token   string
	Re      string
	Text    string
	URL     string
	Summary string
}

func parseLarkCardAction(raw string) (larkCardAction, bool) {
	var ev struct {
		EventID     string `json:"event_id"`
		Token       string `json:"token"`
		ActionTag   string `json:"action_tag"`
		ActionValue string `json:"action_value"`
		FormValue   string `json:"form_value"`
		InputValue  string `json:"input_value"`
	}
	if json.Unmarshal([]byte(raw), &ev) != nil {
		return larkCardAction{}, false
	}
	act := larkCardAction{EventID: ev.EventID, Token: ev.Token}

	if strings.TrimSpace(ev.FormValue) != "" {
		var fm map[string]any
		if json.Unmarshal([]byte(ev.FormValue), &fm) == nil {
			for k, v := range fm {
				if !strings.HasPrefix(k, "reply_") {
					continue
				}
				txt, _ := v.(string)
				txt = strings.TrimSpace(txt)
				if txt == "" {
					continue
				}
				parts := strings.Split(strings.TrimPrefix(k, "reply_"), "_")
				reTag := parts[0]
				if reTag != "" && reTag != "free" {
					act.Re = reTag
				}
				act.Text = txt
				act.Summary = lookupLarkSummary(reTag)
				return act, true
			}
		}
		return larkCardAction{}, false
	}

	if ev.ActionTag == "input" && strings.TrimSpace(ev.InputValue) != "" {
		act.Text = strings.TrimSpace(ev.InputValue)
		var val struct {
			Re      string `json:"re"`
			Summary string `json:"summary"`
		}
		if ev.ActionValue != "" && json.Unmarshal([]byte(ev.ActionValue), &val) == nil {
			act.Re, act.Summary = val.Re, val.Summary
		}
		return act, true
	}

	if ev.ActionTag != "button" || ev.ActionValue == "" {
		return larkCardAction{}, false
	}
	var val struct {
		Re      string `json:"re"`
		Opt     string `json:"opt"`
		URL     string `json:"url"`
		Summary string `json:"summary"`
	}
	if json.Unmarshal([]byte(ev.ActionValue), &val) != nil || strings.TrimSpace(val.Opt) == "" {
		return larkCardAction{}, false
	}
	act.Re, act.Text, act.URL, act.Summary = val.Re, strings.TrimSpace(val.Opt), val.URL, val.Summary
	return act, true
}

func (s *Server) forwardLarkSay(ctx context.Context, hubBase, re, text string) error {
	base := strings.TrimRight(hubBase, "/")
	tok := ""
	if c, ok := loadNodeConfig(); ok {
		if base == "" {
			base = strings.TrimRight(c.Hub, "/")
		}
		tok = c.Token
	}
	if base == "" || tok == "" {
		return nil
	}
	b, _ := json.Marshal(map[string]any{"text": text, "re": re})
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, "POST", base+"/api/super/say", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	direct := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := direct.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (s *Server) handleLarkCardEvent(ctx context.Context, bin, hubBase, raw string) {
	act, ok := parseLarkCardAction(raw)
	if !ok {
		return
	}
	dedupKey := act.EventID
	if dedupKey == "" {
		dedupKey = act.Re + "\n" + act.Text
	}
	larkCardSeenMu.Lock()
	if _, ok := larkCardSeen[dedupKey]; ok {
		larkCardSeenMu.Unlock()
		return
	}
	larkCardSeen[dedupKey] = time.Now()
	for k, ts := range larkCardSeen {
		if time.Since(ts) > 24*time.Hour {
			delete(larkCardSeen, k)
		}
	}
	larkCardSeenMu.Unlock()

	if !s.node && s.sup != nil {
		s.applyQuickReply(act.Re, act.Text)
	} else if hubBase != "" && act.URL != "" {
		if u, err := url.Parse(act.URL); err == nil && u.RawQuery != "" {
			target := strings.TrimRight(hubBase, "/") + "/api/super/quick-reply?" + u.RawQuery
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			req, _ := http.NewRequestWithContext(cctx, "GET", target, nil)
			direct := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}}
			if resp, err := direct.Do(req); err == nil {
				resp.Body.Close()
			} else {
				log.Printf("lark card callback forward failed: %v", err)
			}
			cancel()
		}
	} else if err := s.forwardLarkSay(ctx, hubBase, act.Re, act.Text); err != nil {
		log.Printf("lark card custom reply forward failed: %v", err)
	}

	if act.Token != "" {
		s.updateLarkCardConfirmed(ctx, bin, act.Token, act.Summary, act.Text)
	}
}

func (s *Server) updateLarkCardConfirmed(ctx context.Context, bin, token, summary, opt string) {
	body := ""
	if strings.TrimSpace(summary) != "" {
		body = strings.TrimSpace(summary) + "\n\n"
	}
	body += "✅ **已确认：「" + larkEscape(opt) + "」**\n<font color='grey'>Swag 已收到并继续推进</font>"
	payload, _ := json.Marshal(map[string]any{
		"token": token,
		"card": map[string]any{
			"schema": "2.0",
			"config": map[string]any{"summary": map[string]string{"content": "已确认：" + clip(opt, 40)}},
			"header": map[string]any{
				"title":    map[string]string{"tag": "plain_text", "content": "Swag · 已确认"},
				"template": "green",
			},
			"body": map[string]any{
				"elements": []any{map[string]any{"tag": "markdown", "content": body}},
			},
		},
	})
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(cctx, bin, "api", "POST", "/open-apis/interactive/v1/card/update", "--as", "bot", "--data", string(payload)).CombinedOutput(); err != nil {
		log.Printf("lark card update failed: %v (%s)", err, clip(string(out), 200))
	}
}

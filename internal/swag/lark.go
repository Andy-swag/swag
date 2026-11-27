package swag

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Notifications can also go out as a Feishu/Lark message from the hub's own
// lark-cli: its bot writes to whoever is logged in to it. No lark-cli on the
// hub, or no one logged in, and nothing is sent.

type larkTarget struct {
	mu      sync.Mutex
	bin     string
	openID  string
	who     string
	err     string
	checked time.Time
}

var lark larkTarget

// get says who lark-cli would write to, looking again every 10 minutes (or
// at once when asked), so installing or logging in later just works.
func (l *larkTarget) get(fresh bool) (bin, openID, who, problem string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if fresh || time.Since(l.checked) > 10*time.Minute {
		l.checked = time.Now()
		l.bin, l.openID, l.who, l.err = larkLookup()
	}
	return l.bin, l.openID, l.who, l.err
}

func larkLookup() (bin, openID, who, problem string) {
	home, _ := os.UserHomeDir()
	if bin = findBin("lark-cli", home); bin == "" {
		return "", "", "", "hub 上没有 lark-cli"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "auth", "status").Output()
	var st struct {
		Identities struct {
			Bot struct {
				Available bool `json:"available"`
			} `json:"bot"`
			User struct {
				OpenID   string `json:"openId"`
				UserName string `json:"userName"`
			} `json:"user"`
		} `json:"identities"`
	}
	if err != nil || json.Unmarshal(out, &st) != nil {
		return bin, "", "", "lark-cli 读不到登录状态"
	}
	if !st.Identities.Bot.Available {
		return bin, "", "", "lark-cli 的机器人身份不可用"
	}
	if st.Identities.User.OpenID == "" {
		return bin, "", "", "lark-cli 没有登录用户（lark-cli auth login）"
	}
	return bin, st.Identities.User.OpenID, st.Identities.User.UserName, ""
}

// larkJumpURL adds Feishu's official external-browser query parameters before
// any #fragment so both mobile (iOS/Android) and desktop Feishu open Swag in
// the system default browser (reusing the 1-year login cookie) instead of
// Feishu's ephemeral built-in WebView.
func larkJumpURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return raw
	}
	if u.Path == "" {
		u.Path = "/"
	}
	q := u.Query()
	var extra []string
	if !q.Has("lk_mobile_jump_to_browser") {
		extra = append(extra, "lk_mobile_jump_to_browser=true")
	}
	if !q.Has("lk_jump_to_browser") {
		extra = append(extra, "lk_jump_to_browser=true")
	}
	if len(extra) > 0 {
		if u.RawQuery != "" {
			u.RawQuery += "&" + strings.Join(extra, "&")
		} else {
			u.RawQuery = strings.Join(extra, "&")
		}
	}
	return u.String()
}

// splitLarkBatches keeps plain informational notices coalesced into one card,
// while giving each interactive (needsYou) notice its own standalone card so
// confirming one item can never overwrite or hide other pending items.
func splitLarkBatches(ns []notice) [][]notice {
	if len(ns) == 0 {
		return nil
	}
	interactive := 0
	for _, n := range ns {
		if n.needsYou() {
			interactive++
		}
	}
	if interactive <= 1 {
		return [][]notice{ns}
	}
	var out [][]notice
	var plain []notice
	flushPlain := func() {
		if len(plain) > 0 {
			out = append(out, plain)
			plain = nil
		}
	}
	for _, n := range ns {
		if n.needsYou() {
			flushPlain()
			out = append(out, []notice{n})
		} else {
			plain = append(plain, n)
		}
	}
	flushPlain()
	return out
}

// larkSend writes to the logged-in user, as the bot (a message from yourself
// would not ring): a card, or plain text if the card is turned down.
func larkSend(ns []notice, link string) error {
	bin, openID, _, problem := lark.get(false)
	if problem != "" {
		return errors.New(problem)
	}
	jumpLink := larkJumpURL(link)
	send := func(args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		args = append([]string{"im", "+messages-send", "--as", "bot", "--user-id", openID}, args...)
		if out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput(); err != nil {
			return errors.New(clip(string(out), 300))
		}
		return nil
	}
	for _, batch := range splitLarkBatches(ns) {
		err := send("--msg-type", "interactive", "--content", larkCard(batch, jumpLink))
		if err == nil {
			continue
		}
		text := noticesText(batch)
		if jumpLink != "" {
			text += "\n\n打开 Swag：" + jumpLink
		}
		if err2 := send("--text", text); err2 != nil {
			lark.get(true) // logged out, or moved: look again next time
			return err2
		}
		log.Printf("notify lark: card refused, sent as text: %v", err)
	}
	return nil
}

func larkNoticeKey(n notice, idx int) string {
	if n.entryID != "" {
		return n.entryID
	}
	return "idx:" + strconv.Itoa(idx)
}

// larkCard is a Feishu card (JSON 2.0): the header says how much it matters
// (orange when it needs you, as on the page), the body is Swag's own
// Markdown, and a button opens Swag.
func larkCard(ns []notice, link string) string {
	link = larkJumpURL(link)
	cid := rememberLarkCardState(ns, link)
	return renderLarkCard(ns, link, cid, nil)
}

func renderLarkCard(ns []notice, link, cid string, answered map[string]string) string {
	link = larkJumpURL(link)
	title, color := "Swag", "default" // plain white: colour only when it needs you
	remaining := 0
	var lastAns string
	var unanswered []notice
	for i, n := range ns {
		k := larkNoticeKey(n, i)
		if ans, ok := answered[k]; ok {
			lastAns = ans
			continue
		}
		unanswered = append(unanswered, n)
		switch {
		case n.needsYou():
			remaining++
			title, color = n.label(), "orange"
		case n.tone == "error" && color != "orange":
			title, color = n.label(), "red"
		}
	}
	if len(answered) > 0 {
		if remaining == 0 {
			title, color = "Swag · 已确认", "green"
		} else {
			title, color = "Swag · 还有 "+strconv.Itoa(remaining)+" 项待确认", "orange"
		}
	}
	var els []any
	for i, n := range ns {
		if i > 0 {
			els = append(els, map[string]any{"tag": "hr"})
		}
		k := larkNoticeKey(n, i)
		ans, done := answered[k]
		t := clip(n.text, 1500)
		if n.re != nil {
			t = "<font color='grey'>↩ " + larkEscape(n.re.line()) + "</font>\n" + t
		}
		if len(ns) > 1 && ((n.tone != "" && n.tone != "swag") || n.needsYou()) {
			t = "**" + n.label() + "**\n" + t
		}
		if done {
			t += "\n\n✅ **已确认：「" + larkEscape(ans) + "」**\n<font color='grey'>Swag 已收到并继续推进</font>"
			els = append(els, map[string]any{"tag": "markdown", "content": t})
			continue
		}
		if len(n.options) > 0 && len(n.optionURLs) != len(n.options) {
			var qs []string
			for _, o := range n.options {
				qs = append(qs, "「"+larkEscape(o)+"」")
			}
			t += "\n<font color='grey'>可选：" + strings.Join(qs, " · ") + "</font>"
		}
		els = append(els, map[string]any{"tag": "markdown", "content": t})
		if len(n.options) > 0 && (larkCallbackActive.Load() || len(n.optionURLs) == len(n.options)) {
			useCB := larkCallbackActive.Load()
			for j, o := range n.options {
				u := ""
				if j < len(n.optionURLs) {
					u = n.optionURLs[j]
				}
				if !useCB && u == "" {
					continue
				}
				btype := "default"
				if j == 0 {
					btype = "primary"
				}
				var behav any
				if useCB {
					val := map[string]string{
						"re":      n.entryID,
						"opt":     o,
						"url":     u,
						"summary": clip(n.text, 600),
					}
					if cid != "" {
						val["cid"] = cid
					}
					behav = map[string]any{
						"type":  "callback",
						"value": val,
					}
				} else {
					behav = map[string]string{"type": "open_url", "default_url": u}
				}
				els = append(els, map[string]any{
					"tag": "button", "type": btype, "width": "fill",
					"text":      map[string]string{"tag": "plain_text", "content": clip(o, 40)},
					"behaviors": []any{behav},
				})
			}
		}
		if larkCallbackActive.Load() && n.needsYou() {
			reTag := n.entryID
			if reTag == "" {
				reTag = "free"
			}
			rememberLarkSummary(reTag, clip(n.text, 500))
			els = append(els, map[string]any{
				"tag":              "form",
				"name":             "swag_reply_form_" + strconv.Itoa(i),
				"vertical_spacing": "small",
				"elements": []any{
					map[string]any{
						"tag":         "input",
						"name":        "reply_" + reTag + "_" + strconv.Itoa(i),
						"required":    true,
						"width":       "fill",
						"max_length":  1000,
						"placeholder": map[string]string{"tag": "plain_text", "content": "其他：直接输入你想回复 Swag 的内容…"},
					},
					map[string]any{
						"tag":              "button",
						"name":             "submit_" + reTag + "_" + strconv.Itoa(i),
						"form_action_type": "submit",
						"type":             "default",
						"width":            "fill",
						"text":             map[string]string{"tag": "plain_text", "content": "发送自定义回复"},
					},
				},
			})
		}
	}
	if link != "" {
		els = append(els, map[string]any{
			"tag": "button", "type": "default", "width": "default",
			"text":      map[string]string{"tag": "plain_text", "content": "打开 Swag"},
			"behaviors": []any{map[string]string{"type": "open_url", "default_url": link}},
		})
	}
	sum := cardSummary(ns)
	if len(answered) > 0 && remaining == 0 {
		sum = "已确认：" + clip(lastAns, 40)
	} else if len(unanswered) > 0 && len(answered) > 0 {
		sum = cardSummary(unanswered)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(map[string]any{
		"schema": "2.0",
		"config": map[string]any{"summary": map[string]string{"content": clip(sum, 60)}},
		"header": map[string]any{"title": map[string]string{"tag": "plain_text", "content": title}, "template": color},
		"body":   map[string]any{"elements": els},
	})
	return strings.TrimSuffix(buf.String(), "\n")
}

// larkEscape keeps quoted text from being read as card Markdown.
var larkEscape = strings.NewReplacer("<", "&lt;", ">", "&gt;", "*", "\\*", "`", "'", "[", "\\[", "_", "\\_").Replace

// cardSummary is what the chat list shows for the card: the first line of
// what matters most (needs you, then an error, else the latest), without
// the Markdown marks the list would show as they are.
var mdMarks = strings.NewReplacer("**", "", "__", "", "`", "")

func cardSummary(ns []notice) string {
	if len(ns) == 0 {
		return ""
	}
	pick := ns[len(ns)-1]
	for _, n := range ns {
		if n.tone == "error" {
			pick = n
		}
	}
	for _, n := range ns {
		if n.needsYou() {
			pick = n
		}
	}
	for _, l := range strings.Split(pick.text, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "#>-*• "))
		if l = strings.TrimSpace(mdMarks.Replace(l)); l != "" {
			return l
		}
	}
	return pick.label()
}

var quickReplyDone = struct {
	sync.Mutex
	seen map[string]time.Time
}{seen: map[string]time.Time{}}

func quickReplySig(secret, reID, opt string, exp int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(reID + "\n" + opt + "\n" + strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(mac.Sum(nil))[:24]
}

func (s *Server) quickReplyURL(baseURL, entryID, opt string) string {
	if s == nil || s.token == "" || baseURL == "" || opt == "" {
		return ""
	}
	exp := time.Now().Add(72 * time.Hour).Unix()
	q := url.Values{}
	if entryID != "" {
		q.Set("re", entryID)
	}
	q.Set("opt", opt)
	q.Set("exp", strconv.FormatInt(exp, 10))
	q.Set("sig", quickReplySig(s.token, entryID, opt, exp))
	return strings.TrimRight(baseURL, "/") + "/api/super/quick-reply?" + q.Encode()
}

func (s *Server) superQuickReply(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	reID := strings.TrimSpace(q.Get("re"))
	opt := strings.TrimSpace(q.Get("opt"))
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	sig := q.Get("sig")
	want := quickReplySig(s.token, reID, opt, exp)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err != nil || opt == "" || s.token == "" || time.Now().Unix() > exp || !hmac.Equal([]byte(sig), []byte(want)) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><body style="font-family:-apple-system,sans-serif;padding:36px 20px;text-align:center;color:#111"><h2>链接已失效或签名错误</h2><p style="color:#666">请直接打开 Swag 回复。</p></body>`))
		return
	}
	key := reID + "\n" + opt + "\n" + strconv.FormatInt(exp, 10)
	quickReplyDone.Lock()
	_, dup := quickReplyDone.seen[key]
	if !dup {
		quickReplyDone.seen[key] = time.Now()
		for k, ts := range quickReplyDone.seen {
			if time.Since(ts) > 72*time.Hour {
				delete(quickReplyDone.seen, k)
			}
		}
	}
	quickReplyDone.Unlock()
	if !dup && s.sup != nil {
		s.applyQuickReply(reID, opt)
	}
	statusTitle := "已发送给 Swag"
	if dup {
		statusTitle = "已确认过该选项"
	}
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><body style="font-family:-apple-system,BlinkMacSystemFont,sans-serif;max-width:440px;margin:48px auto;padding:0 20px;text-align:center;color:#111"><div style="font-size:40px;margin-bottom:10px">✅</div><h2 style="margin:0 0 8px">` + statusTitle + `</h2><p style="background:#f4f4f5;padding:12px 14px;border-radius:10px;font-weight:600;margin:14px 0">` + html.EscapeString(opt) + `</p><p style="color:#666;font-size:13px">Swag 已收到并继续推进，可直接关闭本页。</p><a href="/#/swag" style="display:inline-block;margin-top:10px;padding:9px 18px;border-radius:8px;background:#111;color:#fff;text-decoration:none;font-size:13px">打开 Swag 查看进展</a></body>`))
}

func (s *Server) applyQuickReply(reID, opt string) {
	if s == nil || s.sup == nil || strings.TrimSpace(opt) == "" {
		return
	}
	sp := s.sup
	sp.mu.Lock()
	re := sp.deckQuoteLocked(reID)
	e := sp.addEntryReLocked("user", mainThread, opt, "", "", re)
	text := opt
	if re != nil {
		text = "[In reply to what you said earlier: \"" + clip(re.Text, 300) + "\"]\n" + text
	}
	sp.ingestLocked("chat", mainThread, "you", text, "", "", e.ID)
	sp.mu.Unlock()
}

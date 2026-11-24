package swag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (n notice) label() string {
	switch {
	case n.needsYou():
		return "Swag · 需要你"
	case n.tone == "error":
		return "Swag · 出错了"
	}
	return "Swag"
}

// noticesText is the plain form (webhooks, and the card's fallback).
func noticesText(ns []notice) string {
	var parts []string
	for _, n := range ns {
		p := n.label() + "：" + clip(n.text, 400)
		if n.re != nil {
			p = "↩ " + n.re.line() + "\n" + p
		}
		if len(n.options) > 0 {
			p += "\n可选：" + strings.Join(n.options, " / ")
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "\n\n")
}

func isLoopbackOrigin(u string) bool {
	low := strings.ToLower(strings.TrimSpace(u))
	return strings.Contains(low, "://127.0.0.1") || strings.Contains(low, "://localhost") || strings.Contains(low, "://[::1]")
}

// Seen notes that a page of Swag is open in front of you (the page says so
// every 30 seconds while it is visible). Never record loopback URLs so links
// in notifications stay reachable from phones and other machines.
func (sp *Super) Seen(url string) {
	sp.mu.Lock()
	sp.seenAt = time.Now()
	if url != "" && !isLoopbackOrigin(url) {
		sp.seenURL = url
	}
	sp.mu.Unlock()
}

// sendNotifications paces outgoing notifications: messages are coalesced and
// sent at most every 20 seconds, and not at all while you are looking at
// Swag (you see them there).
func (sp *Super) sendNotifications() {
	sp.mu.Lock()
	if len(sp.notifyBuf) == 0 || time.Since(sp.notifyAt) < 20*time.Second {
		sp.mu.Unlock()
		return
	}
	notices := sp.notifyBuf
	text := noticesText(notices)
	url, toLark := sp.st.Settings.NotifyWebhook, sp.st.Settings.NotifyLark
	looking := time.Since(sp.seenAt) < 75*time.Second
	link := sp.seenURL
	sp.notifyBuf, sp.notifyAt = nil, time.Now()
	sp.mu.Unlock()
	urgent := false
	for _, n := range notices {
		if n.needsYou() {
			urgent = true
			break
		}
	}
	if looking && !urgent {
		return
	}
	if toLark {
		base := strings.TrimRight(link, "/")
		if base == "" || isLoopbackOrigin(base) {
			if sp.s != nil {
				base = hubFallbackBaseURL()
			} else {
				base = ""
			}
		}
		if base != "" {
			if sp.s != nil {
				for i := range notices {
					if len(notices[i].options) > 0 {
						urls := make([]string, len(notices[i].options))
						for j, o := range notices[i].options {
							urls[j] = sp.s.quickReplyURL(base, notices[i].entryID, o)
						}
						notices[i].optionURLs = urls
					}
				}
			}
			link = larkJumpURL(base + "/#/swag")
		} else {
			link = ""
		}
		if err := sp.s.larkNotify(context.Background(), notices, link); err != nil {
			log.Printf("notify lark: %v", err)
		}
	}
	if url == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"msg_type": "text", "content": map[string]string{"text": text}, // Feishu/Lark bot
		"text": text, // Slack-style
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(req); err != nil {
		log.Printf("notify: %v", err)
	} else {
		resp.Body.Close()
	}
}

// ---------------------------------------------------------------- recall

// Show renders any object by id with links to related ids (chain of recall).
func (sp *Super) Show(id string) (string, bool) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	var b strings.Builder
	if t := sp.st.trigger(id); t != nil {
		fmt.Fprintf(&b, "trigger %s\nsource: %s  thread: %s  from: %s  at: %s  status: %s  action: %s\n", t.ID, t.Source, t.Thread, t.From, t.At.Format(time.RFC3339), t.Status, t.Action)
		if t.Ref != "" {
			fmt.Fprintf(&b, "ref: %s\n", t.Ref)
		}
		fmt.Fprintf(&b, "\n%s\n", t.Text)
		return b.String(), true
	}
	if th := sp.st.Threads[id]; th != nil {
		fmt.Fprintf(&b, "thread %s\nsubscribers: %s\nrecent triggers:\n", th.ID, strings.Join(th.Subscribers, ", "))
		ids := th.Triggers
		if len(ids) > 15 {
			ids = ids[len(ids)-15:]
		}
		for _, tid := range ids {
			if t := sp.st.trigger(tid); t != nil {
				fmt.Fprintf(&b, "  #%s %s %s: %s\n", t.ID, t.At.Format("01-02 15:04"), t.Source, clip(firstLine(t.Text), 90))
			}
		}
		return b.String(), true
	}
	if w := sp.st.Workers[id]; w != nil {
		fmt.Fprintf(&b, "worker %s (%s)\nstatus: %s  thread: %s  pane: %s  cwd: %s\ncreated: %s by %s\n", w.Name, w.Kind, w.Status, w.Thread, w.PaneID, w.Cwd, w.CreatedAt.Format("01-02 15:04"), w.CreatedBy)
		fmt.Fprintf(&b, "\ntask:\n%s\n", w.Task)
		if w.LastReport != "" {
			fmt.Fprintf(&b, "\nlast report (%s):\n%s\n", w.ReportAt.Format("01-02 15:04"), w.LastReport)
		}
		if w.Note != "" {
			fmt.Fprintf(&b, "\nnote: %s\n", w.Note)
		}
		fmt.Fprintf(&b, "\nterminal: herdr agent read %s --source recent-unwrapped --lines 80\n", w.Name)
		return b.String(), true
	}
	return "", false
}

func (sp *Super) List() string {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	var b strings.Builder
	b.WriteString("machines (pass name to `swag spawn -machine` or group to `-group`):\n")
	for _, m := range sp.s.herdr.Machines() {
		state := "online"
		if !m.Local && !m.Online {
			state = "offline"
		}
		id := m.ID
		if m.Local {
			id = "hub"
		}
		pol := sp.st.Settings.PolicyFor(m.ID, m.Local)
		disp := "off"
		if pol.DispatchEnabled {
			disp = "on"
		}
		extra := ""
		if pol.DreamInclude {
			extra += " dream=on"
		}
		fmt.Fprintf(&b, "  %-14s %-8s group=%-8s dispatch=%-3s %s  home=%s%s\n", id, state, pol.Group, disp, m.Name, m.Home, extra)
	}
	if len(sp.st.Settings.Roles) > 0 {
		b.WriteString("roles (pass to `swag spawn -role`):\n")
		rnames := make([]string, 0, len(sp.st.Settings.Roles))
		for rn := range sp.st.Settings.Roles {
			rnames = append(rnames, rn)
		}
		sort.Strings(rnames)
		for _, rn := range rnames {
			rp := sp.st.Settings.Roles[rn]
			fmt.Fprintf(&b, "  %-14s group=%-8s kind=%-6s cwd=%s\n", rp.Name, rp.Group, rp.Kind, rp.Cwd)
		}
	}
	b.WriteString("workers:\n")
	names := make([]string, 0, len(sp.st.Workers))
	for n := range sp.st.Workers {
		names = append(names, n)
	}
	sort.Strings(names)
	any := false
	for _, n := range names {
		w := sp.st.Workers[n]
		if w.Ended && time.Since(w.CreatedAt) > 24*time.Hour {
			continue
		}
		any = true
		fmt.Fprintf(&b, "  %-14s %-8s %-8s thread=%s  %s\n", w.Name, w.Kind, w.Status, w.Thread, clip(firstLine(w.Task), 70))
	}
	if !any {
		b.WriteString("  (none)\n")
	}
	b.WriteString("threads:\n")
	ths := make([]*Thread, 0, len(sp.st.Threads))
	for _, th := range sp.st.Threads {
		ths = append(ths, th)
	}
	sort.Slice(ths, func(i, j int) bool { return ths[i].LastAt.After(ths[j].LastAt) })
	for i, th := range ths {
		if i >= 12 {
			break
		}
		fmt.Fprintf(&b, "  %-28s last=%s subscribers=%s\n", th.ID, th.LastAt.Format("01-02 15:04"), strings.Join(th.Subscribers, ","))
	}
	b.WriteString("timers:\n")
	for _, t := range sp.st.Timers {
		if t.Enabled {
			fmt.Fprintf(&b, "  %s %q next=%s → %s\n", t.ID, t.Spec, t.NextRun.Format("01-02 15:04"), clip(t.Text, 60))
		}
	}
	return b.String()
}

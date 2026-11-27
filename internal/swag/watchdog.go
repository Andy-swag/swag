package swag

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	emptyPromiseCooldown = 15 * time.Minute
	orphanTaskStaleAfter = 45 * time.Minute
	orphanTaskCooldown   = 2 * time.Hour
	wedgedRouterAfter    = 4 * time.Minute
	wedgedRouterCooldown = 15 * time.Minute
)

var (
	emptyPromiseRe = regexp.MustCompile(`(?i)(下一步由我|接下来(我会|我去|将)|我会.*(查|核实|核验|继续|跟进|处理|补)|仍待(核|查|验证|证)|还欠|还没做完|目前(也)?没有后台|稍后(再)?(查|核|补|跟进)|will check|i'?ll (check|investigate|follow up|verify) next)`)
	waitingHumanRe = regexp.MustCompile(`(?i)(等你|等主人|请(你|主人|您).*(确认|批准|回复|提供|决定|刷新|操作)|需要你|等待(用户|主人|审批|确认|人工|根因修复|进一步证据)|待(审批|确认|主人|用户)|已通知|awaiting (your|human|user))`)
	workerRefusalRe = regexp.MustCompile(`(?i)(无法将.*系统事件视为.*授权|不能将.*系统事件视为.*授权|不会据此访问.*平台|向外部进程发送|不会对.*内部环境开展|不会.*(定向探查|探测|扫描).*内部|暴露服务拓扑|安全控制弱点|prompt injection|untrusted system event|cannot treat.*system event.*authorization|refuse to (execute|run|access)|cannot (probe|scan|inspect).*internal)`)
	bannerOnlyRe    = regexp.MustCompile(`(?i)(OpenAI Codex|Ask Codex to do anything|YOLO mode|Claude Code)`)
	agentFatalErrRe = regexp.MustCompile(`(?i)(api error|please run /login|invalid api key|quota exceeded|daily limit|rate.?limit(ed)?\b|overloaded|unauthori[sz]ed|credit balance|unexpected status \d{3})`)
	agentWarnRe     = regexp.MustCompile(`(?i)(⚠\s*\d+\s*warnings?|f2 to view|⚠\s*warning\b|update available|deprecated|model.?fallback|falling back to|migrations?\b)`)
)

func isAgentFatalError(line string) bool {
	s := strings.TrimSpace(line)
	if s == "" {
		return false
	}
	if agentFatalErrRe.MatchString(s) {
		return true
	}
	if agentWarnRe.MatchString(s) {
		return false
	}
	return agentErrRe.MatchString(s)
}

var watchdogState = struct {
	sync.Mutex
	seenReply         map[string]bool
	lastPromise       time.Time
	orphanSent        map[string]time.Time
	routerWedgedSince time.Time
	lastWedgedRestart time.Time
}{
	seenReply:  map[string]bool{},
	orphanSent: map[string]time.Time{},
}

func resetWatchdogState() {
	watchdogState.Lock()
	watchdogState.seenReply = map[string]bool{}
	watchdogState.lastPromise = time.Time{}
	watchdogState.orphanSent = map[string]time.Time{}
	watchdogState.routerWedgedSince = time.Time{}
	watchdogState.lastWedgedRestart = time.Time{}
	watchdogState.Unlock()
}

// shouldRestartWedgedRouter tracks when a live router stays not-ready/unknown
// while triggers are waiting to be delivered, preventing multi-hour silent stalls.
func shouldRestartWedgedRouter(now time.Time, wedged bool) bool {
	watchdogState.Lock()
	defer watchdogState.Unlock()
	if !wedged {
		watchdogState.routerWedgedSince = time.Time{}
		return false
	}
	if watchdogState.routerWedgedSince.IsZero() {
		watchdogState.routerWedgedSince = now
		return false
	}
	if now.Sub(watchdogState.routerWedgedSince) < wedgedRouterAfter {
		return false
	}
	if !watchdogState.lastWedgedRestart.IsZero() && now.Sub(watchdogState.lastWedgedRestart) < wedgedRouterCooldown {
		return false
	}
	watchdogState.lastWedgedRestart = now
	watchdogState.routerWedgedSince = time.Time{}
	return true
}

func (sp *Super) checkWedgedRouter(ctx context.Context, live map[string]liveAgent, queued []*Trigger) {
	sp.mu.Lock()
	paused := sp.st.Settings.Paused || sp.dream != nil || sp.handover != nil
	sp.mu.Unlock()
	if paused {
		shouldRestartWedgedRouter(time.Now(), false)
		return
	}
	a, alive := live[routerName]
	wedged := alive && len(queued) > 0 && !a.Ready && (a.Status == "unknown" || a.Status == "starting")
	if !shouldRestartWedgedRouter(time.Now(), wedged) {
		return
	}
	sp.mu.Lock()
	sp.addEntryLocked("event", mainThread, "Swag 启动超时卡住，已自动重启恢复", routerName, "error")
	sp.mu.Unlock()
	_ = sp.RestartRouter(ctx)
}

func isStartupBannerOnly(summary string) bool {
	s := strings.TrimSpace(summary)
	if s == "" || len(s) > 1200 {
		return false
	}
	return bannerOnlyRe.MatchString(s) && !strings.Contains(s, "REPORT") && !strings.Contains(s, "BLOCKED")
}

func isWorkerRefusal(w *Worker, summary string) bool {
	if w == nil || !w.ReportAt.IsZero() {
		return false
	}
	return workerRefusalRe.MatchString(summary)
}

// nextEscalationHint returns concrete respawn flags to escalate one rung up
// when a lower-tier model or lower reasoning effort stalls or fails to solve a task.
func nextEscalationHint(w *Worker) string {
	if w == nil {
		return "--model tier-standard"
	}
	model, effort := strings.ToLower(strings.TrimSpace(w.Model)), strings.ToLower(strings.TrimSpace(w.Effort))
	if w.Run != nil {
		if model == "" {
			model = strings.ToLower(strings.TrimSpace(w.Run[0]))
		}
		if effort == "" {
			effort = strings.ToLower(strings.TrimSpace(w.Run[1]))
		}
	}
	switch model {
	case "tier-fast":
		if w.AuthProfile != "" {
			return fmt.Sprintf("--auth-profile %s --model tier-standard", w.AuthProfile)
		}
		return "--model tier-standard"
	case "tier-standard":
		if w.AuthProfile != "" {
			return fmt.Sprintf("--auth-profile %s --model tier-strong", w.AuthProfile)
		}
		return "--model tier-strong"
	}
	switch effort {
	case "minimal", "low":
		return "--effort medium"
	case "", "medium":
		return "--effort high"
	case "high":
		return "--effort xhigh"
	}
	return "--auth-profile swag-pool --model tier-strong"
}

func (sp *Super) handleWorkerNonProgressLocked(w *Worker, summary string) bool {
	age := time.Since(w.CreatedAt)
	if age < 20*time.Second && w.ReportAt.IsZero() && isStartupBannerOnly(summary) {
		return true
	}
	esc := nextEscalationHint(w)
	if isWorkerRefusal(w, summary) {
		sp.ingestLocked("worker", w.Thread, w.Name,
			fmt.Sprintf("worker %s REFUSED/stalled without executing the task (safety/prompt-injection false positive, 0 reports):\n%s\nDo NOT stay silent: stop %s and respawn with a clean, direct engineering brief using an escalated/alternate model (%s, or --auth-profile swag-pool --model tier-standard), or clearly report the blocker to the human.", w.Name, summary, w.Name, esc), "", "", w.Name)
		sp.addEntryLocked("event", w.Thread, w.Name+" 拒跑/误拒，需升级/换模型重派", w.Name, "error")
		return true
	}
	if age < 90*time.Second && w.ReportAt.IsZero() && strings.TrimSpace(summary) != "" {
		sp.ingestLocked("worker", w.Thread, w.Name,
			fmt.Sprintf("worker %s finished its first turn in %ds with ZERO swag report (possible refusal, missing repo/context, or model capability limit). Last message:\n%s\nDo NOT stay silent: verify whether the job actually ran; if the current model/effort couldn't handle it, stop and respawn escalated (%s), or tell the human the concrete blocker.", w.Name, int(age.Seconds()), summary, esc), "", "", w.Name)
		sp.addEntryLocked("event", w.Thread, w.Name+" 首轮未回报，待核查/升级", w.Name, "done")
		return true
	}
	return false
}

// formatDeliveryPrompt renders the prompt sent to router or workers.
// A worker's initial system brief is sent directly without the "[swag] 1 new event"
// wrapper or 700-rune spill truncation so it isn't misclassified as an injected event
// and never loses its trailing report instructions.
func formatDeliveryPrompt(target, sessionStart string, list []*Trigger, words int) string {
	if target != routerName && len(list) == 1 && list[0].Source == "system" && strings.HasPrefix(strings.TrimSpace(list[0].Text), "You are worker ") {
		text := strings.TrimSpace(list[0].Text)
		if r := []rune(text); len(r) > 4000 {
			text = string(r[:4000]) + fmt.Sprintf("\n… [truncated; run `swag show %s`]", list[0].ID)
		}
		return text + "\n"
	}
	var b strings.Builder
	if sessionStart != "" {
		b.WriteString(sessionStart)
		b.WriteString("\n")
	}
	if len(list) == 1 {
		b.WriteString("[swag] 1 new event\n")
	} else {
		fmt.Fprintf(&b, "[swag] %d new events (batched)\n", len(list))
	}
	for _, t := range list {
		fmt.Fprintf(&b, "\n#%s · %s · %s", t.ID, t.Thread, t.Source)
		if t.From != "" {
			fmt.Fprintf(&b, " · from %s", t.From)
		}
		if t.Action == "candidate" {
			b.WriteString(" · candidate (act only if a rule or memory says so; otherwise stay silent)")
		}
		fmt.Fprintf(&b, " · %s\n", t.At.Format("15:04"))
		text := strings.TrimSpace(t.Text)
		if r := []rune(text); len(r) > 700 {
			text = string(r[:700]) + fmt.Sprintf(" … [spilled; run `swag show %s`]", t.ID)
		}
		b.WriteString(indent(text, "  "))
		b.WriteString("\n")
	}
	if target == routerName {
		fmt.Fprintf(&b, "\nReply with `swag reply`, usually ≤%d characters.", words)
	}
	return b.String()
}

func isEmptyPromiseReply(e *Entry) bool {
	if e == nil || e.Role != "swag" || len(e.Options) > 0 || e.Check {
		return false
	}
	txt := strings.TrimSpace(e.Text)
	if txt == "" || waitingHumanRe.MatchString(txt) {
		return false
	}
	return emptyPromiseRe.MatchString(txt)
}

func (sp *Super) hasWorkingOrFreshActionLocked(asked time.Time) bool {
	for _, w := range sp.st.Workers {
		if w == nil || w.Ended {
			continue
		}
		if w.Status == "working" || (!asked.IsZero() && !w.CreatedAt.Before(asked)) {
			return true
		}
	}
	for _, m := range sp.st.Timers {
		if m != nil && m.Enabled && m.LastRun == nil && strings.HasPrefix(strings.ToLower(strings.TrimSpace(m.Spec)), "in ") {
			return true
		}
	}
	for i := len(sp.st.Entries) - 1; i >= 0; i-- {
		ev := sp.st.Entries[i]
		if !asked.IsZero() && ev.At.Before(asked) {
			break
		}
		if ev.Role == "event" && (ev.Tone == "spawn" || strings.Contains(ev.Text, "派出") || strings.Contains(ev.Text, "续上")) {
			return true
		}
	}
	return false
}

// checkEmptyPromiseLocked wakes the router once if it promised follow-up work
// without spawning/sending any worker or scheduling a timer while tasks remain open.
func (sp *Super) checkEmptyPromiseLocked(asked time.Time, reply *Entry) bool {
	if !isEmptyPromiseReply(reply) || sp.st.Settings.Paused {
		return false
	}
	if sp.hasWorkingOrFreshActionLocked(asked) {
		return false
	}
	open := sp.openTasks()
	if len(open) == 0 {
		return false
	}
	now := time.Now()
	watchdogState.Lock()
	if watchdogState.seenReply[reply.ID] || (!watchdogState.lastPromise.IsZero() && now.Sub(watchdogState.lastPromise) < emptyPromiseCooldown) {
		watchdogState.Unlock()
		return false
	}
	watchdogState.seenReply[reply.ID] = true
	watchdogState.lastPromise = now
	watchdogState.Unlock()

	msg := fmt.Sprintf("[watchdog] 你刚才在回复 %s 中说明了未完成或后续动作，但本轮未派出任何 Worker，当前也没有运行中的 Worker（进行中任务：%s）。由于你是事件驱动控制器，回复完即休眠，任务不会自行推进。请现在立刻 spawn 新 Worker 完成剩余已授权步骤；若确实需要主人决策或外部人工操作，请用 `swag reply --opt/--check` 明确告知缺口，不要空口停在原地。",
		reply.ID, strings.Join(open, ", "))
	sp.ingestLocked("system", mainThread, "watchdog", msg, "", "", "")
	return true
}

func extractTerminalAssistantReply(lines []string, start int) string {
	var out []string
	for i := start; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "Worked for ") || strings.HasPrefix(l, "› ") || strings.HasPrefix(l, "← ") || strings.HasPrefix(l, "GPT-") || strings.HasPrefix(l, "─") || strings.HasPrefix(l, "└") || strings.HasPrefix(l, "│") || strings.HasPrefix(l, "┌") {
			if len(out) > 0 && (strings.HasPrefix(l, "Worked for ") || strings.HasPrefix(l, "› ")) {
				break
			}
			continue
		}
		l = strings.TrimSpace(strings.TrimPrefix(l, "• "))
		if l != "" {
			out = append(out, l)
		}
	}
	return clip(strings.Join(out, " "), 500)
}

// surfaceSilentRouterReplyLocked recovers when the router answered a direct
// human chat message only in its terminal (e.g. a refusal without calling
// `swag reply`) and dispatched no worker, preventing infinite silence in chat.
func (sp *Super) surfaceSilentRouterReplyLocked(asked time.Time, lines []string, start int) bool {
	if sp.hasWorkingOrFreshActionLocked(asked) {
		return false
	}
	var hasHumanChat bool
	cutoff := asked.Add(-30 * time.Second)
	for i := len(sp.st.Triggers) - 1; i >= 0; i-- {
		t := sp.st.Triggers[i]
		if t.At.Before(cutoff) {
			break
		}
		if t.Source == "chat" && t.Thread == mainThread {
			hasHumanChat = true
			break
		}
	}
	if !hasHumanChat {
		return false
	}
	reply := extractTerminalAssistantReply(lines, start)
	if reply == "" || isStartupBannerOnly(reply) {
		return false
	}
	sp.addEntryReplyLocked("swag", mainThread, "（终端直出，未调 reply）"+reply, routerName, "", nil, nil, false)
	return true
}

func (sp *Super) workerTaskOpen(name string) bool {
	if name == "" {
		return false
	}
	p := filepath.Join(sp.memDir(), "tasks", name+".md")
	_, err := os.Stat(p)
	return err == nil
}

func (sp *Super) notifyOrphanOnReapLocked(wk *Worker, reason string) {
	if wk == nil || sp.st.Settings.Paused || !strings.Contains(reason, "自动") {
		return
	}
	if !sp.workerTaskOpen(wk.Name) {
		return
	}
	msg := fmt.Sprintf("[watchdog] worker %s 已因空闲自动归档，但进行中任务 memory/tasks/%s.md 尚未移入 tasks/done/（该 worker 上次 report：%s）。请立刻核查该任务：若还需继续则重派新 Worker；若已完成或等待主人决定，请归档或用 --check 明确汇报。",
		wk.Name, wk.Name, func() string {
			if wk.ReportAt.IsZero() {
				return "无"
			}
			return wk.ReportAt.Format("15:04")
		}())
	sp.ingestLocked("system", wk.Thread, "watchdog", msg, "", "", wk.Name)
}

func (sp *Super) checkOrphanOpenTasks(now time.Time) []string {
	sp.mu.Lock()
	if sp.st.Settings.Paused {
		sp.mu.Unlock()
		return nil
	}
	hasRemoteRouter := sp.st.Settings.RouterMachine != "" && sp.st.Settings.RouterMachine != "hub"
	activeWorkers := map[string]bool{}
	for _, w := range sp.st.Workers {
		if w != nil && !w.Ended {
			activeWorkers[strings.ToLower(w.Name)] = true
		}
	}
	timerTexts := make([]string, 0, len(sp.st.Timers))
	for _, m := range sp.st.Timers {
		if m != nil && m.Enabled {
			timerTexts = append(timerTexts, strings.ToLower(m.Target+" "+m.Text))
		}
	}
	sp.mu.Unlock()

	if hasRemoteRouter && sp.s != nil && sp.s.herdr != nil {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		sp.PullRemoteRouterMemory(pctx, false)
		cancel()
	}

	var alerted []string
	for _, f := range sp.memFiles() {
		if !strings.HasPrefix(f.Name, "tasks/") || strings.HasPrefix(f.Name, "tasks/done/") || !strings.HasSuffix(f.Name, ".md") {
			continue
		}
		if now.Sub(f.Mod) < orphanTaskStaleAfter {
			continue
		}
		if waitingHumanRe.MatchString(f.Text) {
			continue
		}
		slug := strings.TrimSuffix(strings.TrimPrefix(f.Name, "tasks/"), ".md")
		lowSlug := strings.ToLower(slug)
		lowText := strings.ToLower(f.Text)
		hasWorker := activeWorkers[lowSlug]
		if !hasWorker {
			for wn := range activeWorkers {
				if len(wn) >= 4 && strings.Contains(lowText, wn) {
					hasWorker = true
					break
				}
			}
		}
		if hasWorker {
			continue
		}
		hasTimer := false
		for _, tt := range timerTexts {
			if strings.Contains(tt, lowSlug) {
				hasTimer = true
				break
			}
		}
		if hasTimer {
			continue
		}

		watchdogState.Lock()
		last := watchdogState.orphanSent[slug]
		if !last.IsZero() && now.Sub(last) < orphanTaskCooldown {
			watchdogState.Unlock()
			continue
		}
		watchdogState.orphanSent[slug] = now
		watchdogState.Unlock()

		sp.mu.Lock()
		msg := fmt.Sprintf("[watchdog] 进行中任务 memory/%s 已超过 %d 分钟未更新，且当前无活跃 Worker、无定时跟进、也未标注等待主人操作。请立刻核查：能继续推进则 spawn Worker 执行，已结束则移入 tasks/done/，确需主人介入则用 --opt/--check 汇报。",
			f.Name, int(now.Sub(f.Mod).Minutes()))
		sp.ingestLocked("system", mainThread, "watchdog", msg, "", "", "")
		sp.mu.Unlock()
		alerted = append(alerted, slug)
	}
	return alerted
}

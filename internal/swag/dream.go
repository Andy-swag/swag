package swag

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	dreamRetryMu    sync.Mutex
	dreamNextTry    time.Time
	dreamFailStreak int
	dreamFailDay    string
)

func dreamCanTry(now time.Time) bool {
	dreamRetryMu.Lock()
	defer dreamRetryMu.Unlock()
	return !now.Before(dreamNextTry)
}

func dreamRecordSuccess() {
	dreamRetryMu.Lock()
	dreamNextTry = time.Time{}
	dreamFailStreak = 0
	dreamFailDay = ""
	dreamRetryMu.Unlock()
}

func dreamRecordFailure(now time.Time, nightly bool) (suppressAlert, giveUpTonight bool) {
	dreamRetryMu.Lock()
	defer dreamRetryMu.Unlock()
	if !nightly {
		return false, false
	}
	day := now.Format("2006-01-02")
	if dreamFailDay != day {
		dreamFailDay = day
		dreamFailStreak = 0
	}
	dreamFailStreak++
	switch dreamFailStreak {
	case 1:
		dreamNextTry = now.Add(15 * time.Minute)
		return false, false
	case 2:
		dreamNextTry = now.Add(45 * time.Minute)
		return true, false
	default:
		dreamNextTry = now.Add(6 * time.Hour)
		return true, true
	}
}

// Dreaming: while Swag sleeps (nightly, or on demand) a separate session
// reviews what happened since the last dream (the chat with the human and
// Swag's own session logs) and tidies memory/. It replaces stale entries
// and records patterns that only show across many sessions. Swag then wakes
// up in a fresh session.

const (
	dreamName  = "swag-dream"
	dreamLabel = "swag · dream"
)

type dreamRun struct {
	at        time.Time
	started   bool // the dream session is up and has its prompt
	worked    bool
	idleAt    time.Time
	reason    string
	blockedAt time.Time
	told      bool
	machine   string // "" = hub, otherwise remote node ID
}

func (sp *Super) dreaming() bool {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.dream != nil
}

func (sp *Super) lastDream() time.Time {
	var raw string
	if sp.db.QueryRow(`SELECT v FROM kv WHERE k='last_dream'`).Scan(&raw) == nil {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (sp *Super) setLastDream(t time.Time) {
	sp.db.Exec(`INSERT INTO kv(k,v) VALUES('last_dream',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, t.Format(time.RFC3339))
}

// dreamAtLocked parses the nightly time ("04:00" by default; "off" disables).
func (sp *Super) dreamAtLocked() (h, m int, ok bool) {
	v := sp.st.Settings.DreamAt
	if v == "" {
		v = "04:00"
	}
	if v == "off" {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(v, "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 4, 0, true
	}
	return h, m, true
}

// nextDream is when the nightly dream is due next (zero when off).
func (sp *Super) nextDream(now time.Time) time.Time {
	sp.mu.Lock()
	h, m, ok := sp.dreamAtLocked()
	sp.mu.Unlock()
	if !ok {
		return time.Time{}
	}
	t := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if !sp.lastDream().Before(t) || now.After(t.Add(3*time.Hour)) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// somethingHappenedLocked: was there any conversation since t?
func (sp *Super) somethingHappenedLocked(t time.Time) bool {
	for i := len(sp.st.Entries) - 1; i >= 0; i-- {
		e := sp.st.Entries[i]
		if e.At.Before(t) {
			return false
		}
		if e.Role == "user" || e.Role == "swag" {
			return true
		}
	}
	return false
}

// checkDream runs every tick: drives a dream in progress, or starts the nightly one.
func (sp *Super) checkDream(ctx context.Context, live map[string]liveAgent, queued bool) {
	sp.mu.Lock()
	d := sp.dream
	sp.mu.Unlock()
	if d != nil {
		sp.driveDream(ctx, d, live)
		return
	}
	now := time.Now()
	if !dreamCanTry(now) {
		return
	}
	next := sp.nextDream(now)
	if next.IsZero() || now.Before(next) || now.After(next.Add(3*time.Hour)) {
		return
	}
	a, alive := live[routerName]
	sp.mu.Lock()
	busy := sp.handover != nil || sp.st.Settings.Paused || queued || !sp.routerAskedAt.IsZero() ||
		(alive && a.Status != "idle" && a.Status != "done")
	happened := sp.somethingHappenedLocked(sp.lastDream())
	sp.mu.Unlock()
	if busy {
		return
	}
	if !happened {
		sp.setLastDream(now) // a quiet day: nothing to dream about
		return
	}
	sp.StartDream(ctx, "nightly")
}

// StartDream puts Swag to sleep and starts the dream session.
func (sp *Super) StartDream(ctx context.Context, reason string) error {
	sp.mu.Lock()
	if sp.dream != nil || sp.handover != nil {
		sp.mu.Unlock()
		return errors.New("Swag 正在整理记忆")
	}
	sp.dream = &dreamRun{at: time.Now(), reason: reason}
	st := sp.st.Settings
	kind := st.DreamKind
	if _, ok := kinds[kind]; !ok {
		kind = st.RouterKind
	}
	args := sp.dreamArgsLocked(kind)
	dreamMachineWant := strings.TrimSpace(st.DreamMachine)
	dreamProfile := strings.TrimSpace(st.DreamAuthProfile)
	if dreamProfile == "" {
		dreamProfile = strings.TrimSpace(st.RouterAuthProfile)
	}
	at := sp.dream.at
	entry := sp.addEntryLocked("event", mainThread, "Swag 睡一会儿，回顾最近的对话、整理记忆（做梦）", "", "info")
	sp.mu.Unlock()

	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		fail := func(err error) {
			log.Printf("dream: %v", err)
			suppress, giveUp := dreamRecordFailure(time.Now(), reason == "nightly")
			if giveUp {
				sp.setLastDream(time.Now())
			}
			sp.mu.Lock()
			sp.dream = nil
			if !suppress {
				sp.addEntryLocked("event", mainThread, "整理记忆没能开始："+err.Error(), "", "error")
			}
			sp.mu.Unlock()
		}
		// Swag's current session ends here; its log is part of what gets reviewed.
		if live, ok := sp.liveAgents(bg); ok {
			if a, ok := live[routerName]; ok && a.Session != "" {
				k := a.Kind
				if k == "antigravity" {
					k = "agy"
				}
				e := &Ended{Kind: k, Cwd: sp.routerDir(), Name: routerName, Session: a.Session, Ended: time.Now(),
					Title: "Swag · 做梦前的会话（" + time.Now().Format("01-02 15:04") + "）"}
				sp.s.remember(e)
				sp.mu.Lock()
				entry.Ref = "h:" + e.ID
				sp.changed()
				sp.mu.Unlock()
			}
		}
		sp.closeWorkspaces(bg, routerLabel)
		sp.closeWorkspaces(bg, dreamLabel)
		sp.PullRemoteRouterMemory(bg, true)
		if err := sp.writeRouterFiles(); err != nil {
			log.Printf("router files: %v", err)
		}
		if err := sp.writeDreamInput(); err != nil {
			fail(err)
			return
		}
		os.MkdirAll(filepath.Join(sp.routerDir(), "dreams"), 0o700)
		targetMachine := ""
		workDir := sp.routerDir()
		if dreamMachineWant != "" && dreamMachineWant != "hub" {
			if mid := sp.s.machineByName(dreamMachineWant); mid != "" {
				if dir, err := sp.pushDreamWork(bg, mid); err == nil && dir != "" {
					targetMachine = mid
					workDir = dir
					sp.mu.Lock()
					if sp.dream != nil {
						sp.dream.machine = mid
					}
					sp.mu.Unlock()
				} else {
					log.Printf("dream push to %s failed (falling back to hub): %v", dreamMachineWant, err)
				}
			} else {
				log.Printf("dream machine %s offline (falling back to hub)", dreamMachineWant)
			}
		}
		paneID, _, err := sp.s.startAgentWithProfile(bg, targetMachine, kinds[kind], dreamName, workDir, dreamLabel, args, dreamProfile)
		if err != nil {
			var he *HerdrError
			if !(errors.As(err, &he) && he.Code == "agent_not_ready") {
				fail(err)
				return
			}
		}
		prompt := strings.ReplaceAll(dreamPrompt, "{{DATE}}", journalName(at))
		prompt = strings.ReplaceAll(prompt, "{{BIN}}", sp.bin)
		prompt = strings.ReplaceAll(prompt, "{{BUDGET}}", fmt.Sprint(upFrontBudget>>10))
		migrate := ""
		if sp.legacyMemory() {
			migrate = "\nFirst: " + migrateNote + "\n"
		}
		prompt = strings.ReplaceAll(prompt, "{{MIGRATE}}", migrate)
		deadline := time.Now().Add(90 * time.Second)
		for {
			err := sp.s.herdr.Prompt(bg, dreamName, prompt)
			var he *HerdrError
			if err == nil {
				break
			}
			if errors.As(err, &he) && he.Code == "agent_blocked" && paneID != "" && !time.Now().After(deadline) {
				sp.dismissRoutineDreamDialog(bg, paneID, workDir)
				if sleepCtx(bg, 2*time.Second) != nil {
					fail(bg.Err())
					return
				}
				continue
			}
			if !errors.As(err, &he) || (he.Code != "agent_not_ready" && he.Code != "agent_not_found") || time.Now().After(deadline) {
				sp.closeWorkspaces(context.Background(), dreamLabel)
				fail(err)
				return
			}
			if sleepCtx(bg, 2*time.Second) != nil {
				fail(bg.Err())
				return
			}
		}
		dreamRecordSuccess()
		sp.mu.Lock()
		if sp.dream != nil {
			sp.dream.started = true
		}
		sp.mu.Unlock()
	}()
	return nil
}

func (sp *Super) dismissRoutineDreamDialog(ctx context.Context, pane, workDir string) {
	if pane == "" || sp.s == nil || sp.s.herdr == nil {
		return
	}
	if res, err := sp.s.herdr.Read(ctx, pane, "recent_unwrapped", "text", 40); err == nil {
		low := strings.ToLower(res.Text)
		if strings.Contains(low, "update available") && strings.Contains(low, "skip") {
			_ = sp.s.herdr.Call(ctx, "pane.send_keys", map[string]any{"pane_id": pane, "keys": []string{"Escape"}}, nil)
			log.Printf("dream: skipped Codex update dialog on %s", pane)
			return
		}
	}
	if workDir != "" && sp.answerTrust(ctx, pane, workDir) {
		log.Printf("dream: trusted working directory %s on %s", workDir, pane)
		return
	}
	sp.trustOwnDir(ctx, pane)
}

func (sp *Super) driveDream(ctx context.Context, d *dreamRun, live map[string]liveAgent) {
	sp.mu.Lock()
	started := d.started
	sp.mu.Unlock()
	if !started {
		if time.Since(d.at) > 5*time.Minute { // the launcher goroutine is stuck
			sp.finishDream(ctx, d, false)
		}
		return
	}
	a, alive := live[dreamName]
	if alive && a.Status == "blocked" {
		sp.dismissRoutineDreamDialog(ctx, a.Pane, "")
		sp.mu.Lock()
		if d.blockedAt.IsZero() {
			d.blockedAt = time.Now()
		} else if !d.told && time.Since(d.blockedAt) > time.Minute {
			d.told = true
			sp.addEntryLocked("event", mainThread, "Swag 整理记忆时停在一个确认上，点这里看看", dreamName, "blocked")
		}
		sp.mu.Unlock()
	} else {
		sp.mu.Lock()
		d.blockedAt = time.Time{}
		sp.mu.Unlock()
	}
	sp.mu.Lock()
	switch {
	case alive && a.Status == "working":
		d.worked, d.idleAt = true, time.Time{}
	case alive && (a.Status == "idle" || a.Status == "done") && d.idleAt.IsZero():
		d.idleAt = time.Now()
	}
	worked, idleAt := d.worked, d.idleAt
	sp.mu.Unlock()
	wait := time.Since(d.at)
	switch {
	case worked && !idleAt.IsZero() && time.Since(idleAt) > 6*time.Second:
		sp.finishDream(ctx, d, true)
	case !alive && worked, wait > 25*time.Minute, !worked && !idleAt.IsZero() && time.Since(idleAt) > 2*time.Minute:
		sp.finishDream(ctx, d, worked)
	}
}

func (sp *Super) finishDream(ctx context.Context, d *dreamRun, ok bool) {
	if ok && d.machine != "" {
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := sp.pullDreamWork(pctx, d.machine); err != nil {
			log.Printf("dream pull from %s: %v", d.machine, err)
			ok = false
		}
		cancel()
	}
	sp.closeWorkspaces(ctx, dreamLabel)
	summary, skills, fixes, scripts := "", "", "", ""
	if ok {
		dreamRecordSuccess()
		sp.setLastDream(time.Now())
		jName := journalName(d.at)
		journal := filepath.Join(sp.routerDir(), "dreams", jName+".md")
		summary = dreamSummary(journal)
		skills = dreamLine(journal, dreamSkillsPrefix)
		fixes = dreamLine(journal, dreamFixPrefix)
		scripts = dreamLine(journal, dreamScriptsPrefix)
		addDreamProposalTodos(jName, skills, fixes, scripts)
	} else if d.reason == "nightly" {
		_, giveUp := dreamRecordFailure(time.Now(), true)
		if giveUp {
			sp.setLastDream(time.Now())
		}
	}
	sp.mu.Lock()
	sp.dream = nil
	sp.routerLaunchAt = time.Time{}
	if ok && len(sp.st.Settings.DreamPinned) > 0 {
		sp.st.Settings.DreamPinned = nil
		sp.changed()
	}
	syncMirror := ok && sp.st.Settings.SyncMemoryMirror
	switch {
	case !ok:
		sp.addEntryLocked("event", mainThread, "这次整理记忆没有完成，Swag 先醒过来了", "", "error")
	case summary != "":
		sp.addEntryLocked("event", mainThread, "Swag 醒了。整理记忆："+summary, "", "info")
	default:
		sp.addEntryLocked("event", mainThread, "Swag 醒了，记忆整理好了", "", "info")
	}
	if skills != "" {
		// Yours to decide, so it is said to you, not left in the journal.
		sp.addEntryLocked("swag", mainThread, "整理记忆时发现这几件事做熟了，可以做成 skill，以后哪个 agent 都能直接用："+skills+"。要做的话告诉我放在哪（你自己的 skills，还是某个项目的仓库）。", "", "")
	}
	if fixes != "" {
		sp.addEntryLocked("swag", mainThread, "整理记忆时发现已有的 skill 有地方不好用："+fixes+"。要改的话说一声，我派人去改。", "", "")
	}
	if scripts != "" {
		sp.addEntryLocked("swag", mainThread, "整理记忆时发现这几个高频流程适合固化成脚本或流水线："+scripts+"。确认要落地的话说一声，我直接派人写成脚本并补单测。", "", "")
	}
	sp.mu.Unlock()
	if syncMirror {
		go sp.syncMemoryMirrors(context.Background(), false)
	}
	sp.ensureRouter(ctx)
}

// dreamSummary is the journal's first line of prose.
// dreamSkillsPrefix starts the journal line naming how-tos ripe for a skill.
const dreamSkillsPrefix = "可做成 skill："

// dreamFixPrefix starts the line naming existing skills that need fixing.
const dreamFixPrefix = "可改进 skill："

// dreamScriptsPrefix starts the line naming repeated flows ripe for a script or pipeline.
const dreamScriptsPrefix = "可固化脚本/流水线："

// journalName names one dream's journal: one file per run, so the hub reads
// this run's summary, never an earlier one's from the same day.
func journalName(at time.Time) string { return at.Format("2006-01-02-1504") }

// dreamArgsLocked: dreaming runs as Swag's agent but on the model its
// workers use. Swag itself is tuned for quick routing; tidying memory is
// judgement work that runs once a day with nobody waiting.
func (sp *Super) dreamArgsLocked(kind string) []string {
	st := sp.st.Settings
	model, effort := st.DreamModel, st.DreamEffort
	if model == "" && kinds[kind] == kinds[st.WorkerKind] && st.WorkerModel != "" {
		model, effort = st.WorkerModel, st.WorkerEffort
	} else if model == "" && kinds[kind] == kinds[st.RouterKind] {
		model, effort = st.RouterModel, st.RouterEffort
	}
	var args []string
	if kinds[kind] == kinds[st.RouterKind] {
		args = append(args, st.RouterArgs...)
	}
	args = append(args, modelArgs(kind, model, effort)...)
	return withBypass(args, kind, st.AskPermissions)
}

// dreamLine is what the journal's line starting with prefix lists, or "".
func dreamLine(path, prefix string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for i, l := range strings.Split(string(b), "\n") {
		if i > 8 {
			break // it is up top, not in the reasons below
		}
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), ">-* "))
		if s, ok := strings.CutPrefix(l, prefix); ok {
			return strings.TrimRight(strings.TrimSpace(s), "。.")
		}
	}
	return ""
}

func dreamSummary(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "#") || strings.HasPrefix(l, dreamSkillsPrefix) || strings.HasPrefix(l, dreamFixPrefix) || strings.HasPrefix(l, dreamScriptsPrefix) {
			continue // the title and structured proposal lines, surfaced on their own
		}
		l = strings.TrimSpace(strings.TrimLeft(l, ">-* "))
		if l != "" {
			if r := []rune(l); len(r) > 160 {
				l = string(r[:160]) + "…"
			}
			return l
		}
	}
	return ""
}

func (sp *Super) closeWorkspaces(ctx context.Context, label string) {
	wss, err := sp.s.herdr.Workspaces(ctx)
	if err != nil {
		return
	}
	for _, ws := range wss {
		if ws.Label == label {
			sp.s.herdr.Call(ctx, "workspace.close", map[string]any{"workspace_id": ws.ID}, nil)
		}
	}
}

// writeDreamInput condenses what happened since the last dream into dream/input.md.
func (sp *Super) writeDreamInput() error {
	since := sp.lastDream()
	var b strings.Builder
	fmt.Fprintf(&b, "# What happened since the last dream (%s)\n\n", func() string {
		if since.IsZero() {
			return "never"
		}
		return since.Format("2006-01-02 15:04")
	}())

	b.WriteString("## Chat with the human and Swag's event log\n\n")
	sp.mu.Lock()
	var lines []string
	for _, e := range sp.st.Entries {
		if e.At.Before(since) {
			continue
		}
		who := map[string]string{"user": "human", "swag": "Swag"}[e.Role]
		if who == "" {
			who = "event"
		}
		lines = append(lines, fmt.Sprintf("- %s %s: %s", e.At.Format("01-02 15:04"), who, clip(oneLine(e.Text), 600)))
	}
	sp.mu.Unlock()
	if len(lines) > 500 {
		lines = lines[len(lines)-500:]
	}
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n\n## Swag's own sessions (its reasoning and the commands it ran)\n")

	history.Lock()
	sp.s.loadHistory()
	var sess []*Ended
	for _, h := range history.list {
		if h.Name == routerName && h.Ended.After(since) {
			sess = append(sess, h)
		}
	}
	history.Unlock()
	sort.Slice(sess, func(i, j int) bool { return sess[i].Ended.Before(sess[j].Ended) })
	const budget = 240 << 10
	var parts []string
	for _, h := range sess {
		path, format := sp.s.findTranscript(h.Kind, "id", h.Session, "", "")
		if path == "" {
			continue
		}
		tr := newTranscript(path, format)
		tr.update()
		var p strings.Builder
		fmt.Fprintf(&p, "\n### %s (ended %s)\n\n", h.Title, h.Ended.Format("01-02 15:04"))
		for _, m := range tr.msgs {
			switch m.Kind {
			case "user":
				fmt.Fprintf(&p, "IN: %s\n\n", clip(m.Text, 1500))
			case "assistant":
				fmt.Fprintf(&p, "SWAG: %s\n\n", clip(m.Text, 1500))
			case "tool":
				fmt.Fprintf(&p, "TOOL %s: %s → %s\n\n", m.Name, clip(oneLine(m.Input), 300), clip(oneLine(m.Output), 300))
			}
		}
		parts = append(parts, p.String())
	}
	// Newest sessions matter most: drop the oldest when over budget.
	total := 0
	keep := 0
	for i := len(parts) - 1; i >= 0; i-- {
		if total+len(parts[i]) > budget && keep > 0 {
			break
		}
		total += len(parts[i])
		keep++
	}
	if keep < len(parts) {
		fmt.Fprintf(&b, "\n(%d older sessions left out for length.)\n", len(parts)-keep)
	}
	for _, p := range parts[len(parts)-keep:] {
		if len(p) > budget {
			p = "…(start cut)…\n" + p[len(p)-budget:]
		}
		b.WriteString(p)
	}
	if len(parts) == 0 {
		b.WriteString("\n(no session logs found)\n")
	}
	if ext := sp.collectExternalDreamInput(context.Background(), since); ext != "" {
		b.WriteString("\n## External sessions opted into Dream\n")
		b.WriteString(ext)
	}
	dir := filepath.Join(sp.routerDir(), "dream")
	os.MkdirAll(dir, 0o700)
	return os.WriteFile(filepath.Join(dir, "input.md"), []byte(b.String()), 0o600)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

const dreamPrompt = `[swag] Dream. You are Swag's memory keeper and engineering curator, running while Swag sleeps. Swag is the agent described in ROUTER.md: read it for context and for the layout of memory/; your job is to keep its memory accurate, lean, and actionable. Your inputs, all in this directory:
- memory/: Swag's memory, laid out as ROUTER.md describes.
- dream/input.md: what happened since the last dream: the chat with the human, Swag's session logs, and opted-in external sessions.
- dreams/: earlier dream journals, if any.
Run ` + "`{{BIN}} list`" + ` to see which agents and machines are actually alive right now.
{{MIGRATE}}
Tidy memory/ and maintain verified engineering knowledge. Every session reads the up-front files (instructions.md, notes/index.md, open tasks) before anything else, so each line there has to earn its place: keep them under ~{{BUDGET}} KB together, and smaller is better.
1. Task progress & active stale/wrong-fact pruning:
   - Update active tasks in memory/tasks/ with verified latest progress, blockers, and next steps from the logs.
   - Move tasks that are finished, abandoned, or whose agents are gone to memory/tasks/done/ with a clear closing line on how they ended.
   - Actively audit dated facts across memory/notes/ (host roles, commands, file paths, endpoints, model mappings) by checking ` + "`{{BIN}} list`" + ` and the filesystem: fix what changed and delete what is obsolete or wrong so stale lies never accumulate.
2. Rewrite instructions.md line by line. Each line is one rule or preference, at most about 80 Chinese characters: what to do, not why, not the incidents behind it, not the steps. Move procedures, examples, dates and history into the note the line points to ("见 notes/x.md"). A routine a timer already runs keeps its how-to in the timer and its note, not here. Merge duplicate rules.
3. Cross-session patterns & pitfalls (memory/notes/pitfalls.md):
   - Capture hard-won lessons, recurring errors, root causes, gotchas, and exact verified commands/flags from today's logs into memory/notes/pitfalls.md (or the matching topic note), removing outdated workarounds once fixed.
   - Look for patterns across sessions: repeated requests, corrections, unstated preferences, or mistakes Swag made more than once. Write lasting behavioral adjustments into instructions.md (or a note), marked "(inferred)", backed by logs.
   - Record what the logs show about which agent or model worked or failed on which computer in memory/notes/agents.md.
4. Reusable knowledge & pointers (memory/notes/):
   - File reusable findings by topic so the next similar job finds them quickly. Prefer pointers (paths, thread ids, URLs) over bulky copies; merge duplicates. Every note gets one accurate line in memory/notes/index.md.
5. Crystallize repeatable flows into Skills, Scripts, or Pipelines:
   - Check existing skills (notes/skills.md says where they live). Shrink any note already covered by a skill to a pointer.
   - "可做成 skill：": mature how-tos used across multiple jobs that no skill covers yet, separated by 、 (e.g. "SQL 只读查表").
   - "可改进 skill：": existing skills that agents worked around, failed with, or fixed by hand, as "name（what to fix）", separated by 、.
   - "可固化脚本/流水线：": repeated multi-step command sequences, health/data checks, or deploy/eval routines worth turning into a reusable script or automated pipeline (as "name（target repo/path & 1-line scope）", separated by 、). Optionally record high-value candidates via ` + "`{{BIN}} todo add --source agent --title \"...\" --detail \"...\"`" + ` so the human can approve and dispatch them in one click.
   Put any non-empty proposal lines right under the journal's first line; omit empty ones.
6. Opt-in local workspace knowledge & scratch hygiene (memory/notes/dream-workspaces.md):
   - If memory/notes/dream-workspaces.md exists and lists local project workspaces present on this computer, maintain each listed workspace strictly within its declared scope:
     a) Update/prune stale project knowledge docs (e.g. docs/lessons/, docs/agents/, runbooks, glossary) with today's verified findings and remove wrong/obsolete statements.
     b) Clean expired, untracked temporary scratch files (e.g. tmp/ files older than the configured retention days and NOT referenced by any open file in memory/tasks/).
   - Hard safety redlines: NEVER edit or delete uncommitted source code, NEVER delete tracked repo files without approval, NEVER run git push or destructive git resets, and NEVER touch live production services during Dream. List every workspace doc updated or scratch artifact cleaned in the dream journal.
7. Keep every explicit instruction from the human unless a later one overrides it (tightening wording is fine). memory/messages.md is written by the hub; read it as input only.

Then write a new file dreams/{{DATE}}.md. Its first line: one concise sentence in Chinese summarizing what you learned, cleaned, and updated; the human sees it. Next, any non-empty proposal lines ("可做成 skill：" / "可改进 skill：" / "可固化脚本/流水线："). Then, in under 40 lines:
- 新增经验与避坑
- 清理的过时/错误信息与任务归档
- 工作区文档更新与临时文件清理（如有）
- 观察到但暂未改动的模式

Your work is the files alone (plus optional ` + "`{{BIN}} todo add --source agent`" + ` for script/pipeline proposals): when finished, stop. Swag reports your findings to the human.`

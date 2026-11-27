package swag

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Super is Swag's super-agent: one resident Router session (the herdr agent
// named "swag") that talks to the human and dispatches work, plus isolated
// Worker sessions (other herdr agents) that do it. Everything that reaches the
// system is a Trigger in a durable inbox; Swag filters, routes, batches and
// delivers triggers whenever the target session is free.
type Super struct {
	s   *Server
	db  *sql.DB
	bin string
	dir string // ~/.config/swag

	mu            sync.Mutex
	st            *State
	dirty         bool
	deletedTimers []string
	subs          map[chan struct{}]bool

	routerLaunching bool
	rereadTrig      *Trigger // the pending "re-read ROUTER.md", if any
	routerLaunchAt  time.Time
	routerFresh     bool
	routerAskedAt   time.Time // last delivery to the router not yet followed by a reply or check
	routerRetries   int       // turns in a row that ended on an API error, each retried
	routerNudged    bool      // the watchdog interrupted this turn once already
	routerUsage     ctxUsage
	// The model and effort the running router was launched with (unknown after a hub restart).
	routerRun   *[2]string
	handover    *handover            // set while the router writes its memory before a fresh session
	dream       *dreamRun            // set while Swag sleeps and a dream session tidies its memory
	lastTrust   map[string]time.Time // pane → when its trust dialog was last answered
	lastAnswer  map[string]time.Time // pane → when a dialog there was just answered
	lastScreen  map[string]screenMark
	transcripts map[string]*transcript
	noAgent     map[string]time.Time // worker → since when its pane has had no agent in it
	pinnedCache map[string]*Entry    // older pinned entries resolved from SQLite
	notifyBuf   []notice
	notifyAt    time.Time
	lastReap    time.Time
	seenAt      time.Time // when a page of Swag was last open in front of you
	seenURL     string    // where that page was, for the link in a notification
}

type screenMark struct {
	hash  string
	since time.Time
	flag  bool
}

const (
	routerName  = "swag"
	routerLabel = "swag · router"
)
const mainThread = "chat:main"

func newSuper(s *Server, dir string) (*Super, error) {
	bin, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(bin); err == nil {
		bin = r
	}
	bin = stableBin(bin, dir)
	sp := &Super{
		s: s, dir: dir, bin: bin,
		subs:        map[chan struct{}]bool{},
		lastScreen:  map[string]screenMark{},
		transcripts: map[string]*transcript{},
		noAgent:     map[string]time.Time{},
	}
	db, err := openDB(filepath.Join(dir, "swag.db")) // ~/.swag/legacy.db belongs to another app
	if err != nil {
		return nil, err
	}
	sp.db = db
	if sp.st, err = loadState(db, s.home); err != nil {
		return nil, err
	}
	return sp, nil
}

// ---------------------------------------------------------------- state plumbing

func (sp *Super) changed() {
	sp.dirty = true
	for ch := range sp.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (sp *Super) subscribe() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	sp.mu.Lock()
	sp.subs[ch] = true
	sp.mu.Unlock()
	return ch, func() { sp.mu.Lock(); delete(sp.subs, ch); sp.mu.Unlock() }
}

func (sp *Super) flush() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if !sp.dirty {
		return
	}
	if err := saveState(sp.db, sp.st, sp.deletedTimers); err != nil {
		log.Printf("save state: %v", err)
		return
	}
	sp.st.trim() // history beyond the working set stays in SQLite
	sp.dirty, sp.deletedTimers = false, nil
}

func (sp *Super) addEntryLocked(role, thread, text, ref, tone string) *Entry {
	return sp.addEntryReplyLocked(role, thread, text, ref, tone, nil, nil, false)
}

func (sp *Super) addEntryReLocked(role, thread, text, ref, tone string, re *Quote) *Entry {
	return sp.addEntryReplyLocked(role, thread, text, ref, tone, re, nil, false)
}

func (sp *Super) addEntryReplyLocked(role, thread, text, ref, tone string, re *Quote, opts []string, check bool) *Entry {
	e := &Entry{ID: sp.st.nextID("e"), Role: role, Thread: thread, Text: text, At: time.Now(), Ref: ref, Tone: tone, Re: re, Options: opts, Check: check}
	sp.st.Entries = append(sp.st.Entries, e)
	if role == "swag" || tone == "blocked" || tone == "error" {
		before := len(sp.notifyBuf)
		sp.queueNotifyLocked(role, text, tone, re, check, opts...)
		if len(sp.notifyBuf) > before {
			sp.notifyBuf[len(sp.notifyBuf)-1].entryID = e.ID
		}
	}
	sp.changed()
	return e
}

const maxReplyOptions = 6

func cleanOptions(raw []string) []string {
	var out []string
	for _, s := range raw {
		s = clip(oneLine(s), 80)
		if s == "" || slices.Contains(out, s) {
			continue
		}
		out = append(out, s)
		if len(out) >= maxReplyOptions {
			break
		}
	}
	return out
}

// Reply posts Swag's message to a conversation thread, with optional quote,
// quick-reply buttons or --check review flag, and returns any soft warning for the CLI.
func (sp *Super) Reply(thread, text, reID string, options []string, check ...bool) (*Entry, string) {
	text = strings.TrimSpace(text)
	if thread == "" {
		thread = mainThread
	}
	opts := cleanOptions(options)
	chk := len(check) > 0 && check[0]
	sp.mu.Lock()
	limit := sp.st.Settings.ReplyChars
	re := sp.quoteLocked(reID)
	e := sp.addEntryReplyLocked("swag", thread, text, "", "", re, opts, chk)
	sp.mu.Unlock()
	var warn string
	if strings.TrimSpace(reID) != "" && re == nil {
		warn = "--re " + reID + " is not an event or message I know; the reply went out without it"
	}
	if n := replyLen(text); limit > 0 && n > limit {
		warn = fmt.Sprintf("reply is %d characters; chat replies are usually ≤%d. Fine if the human asked for detail or the result needs a list; otherwise keep the next one shorter", n, limit)
	}
	return e, warn
}

func (sp *Super) threadLocked(id string) *Thread {
	th := sp.st.Threads[id]
	if th == nil {
		th = &Thread{ID: id, Subscribers: []string{}, Triggers: []string{}}
		sp.st.Threads[id] = th
	}
	return th
}

// Ingest puts one trigger in the inbox after the fast-path policy check.
// It returns the trigger (nil when it was a duplicate).
func (sp *Super) Ingest(source, thread, from, text, key, target, ref string) *Trigger {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.ingestLocked(source, thread, from, text, key, target, ref)
}

func (sp *Super) ingestLocked(source, thread, from, text, key, target, ref string) *Trigger {
	if key != "" {
		for i := len(sp.st.Triggers) - 1; i >= 0 && i > len(sp.st.Triggers)-400; i-- {
			if sp.st.Triggers[i].Key == key {
				return nil
			}
		}
	}
	if thread == "" {
		thread = mainThread
	}
	pol := sp.st.policyFor(source)
	action := pol.Action
	if len(pol.Allow) > 0 && !contains(pol.Allow, from) {
		action = "ignore"
	}
	t := &Trigger{
		ID: sp.st.nextID("t"), Source: source, Thread: thread, From: from, Text: text,
		At: time.Now(), Action: action, Status: "queued", Key: key, Ref: ref,
	}
	if action == "ignore" {
		t.Status = "ignored"
	} else if target != "" {
		t.Targets = []string{target}
	}
	sp.st.Triggers = append(sp.st.Triggers, t)
	th := sp.threadLocked(thread)
	th.LastAt = t.At
	th.Triggers = append(th.Triggers, t.ID)
	sp.changed()
	return t
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- run loop

func (sp *Super) Run(ctx context.Context) {
	// A new build may change the router's instructions: the next session
	// (a handover, a dream, a restart) reads them fresh.
	if err := sp.writeRouterFiles(); err != nil {
		log.Printf("router files: %v", err)
	}
	tick := time.NewTicker(1500 * time.Millisecond)
	defer tick.Stop()
	save := time.NewTicker(2 * time.Second)
	defer save.Stop()
	for {
		select {
		case <-ctx.Done():
			sp.flush()
			return
		case <-save.C:
			sp.flush()
			sp.sendNotifications()
		case <-tick.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("super tick panic: %v", r)
					}
				}()
				sp.tick(ctx)
			}()
		}
	}
}

type liveAgent struct {
	Pane    string
	Status  string
	Ready   bool
	Kind    string
	Session string
}

var uuidRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// sessionID is what an agent's conversation is resumed by. herdr gives some
// (Codex) as the path of their log, whose name ends in the id.
func sessionID(kind, value string) string {
	if kind != "path" {
		return value
	}
	ids := uuidRe.FindAllString(filepath.Base(value), -1)
	if len(ids) == 0 {
		return ""
	}
	return ids[len(ids)-1]
}

func (sp *Super) liveAgents(ctx context.Context) (map[string]liveAgent, bool) {
	if sp.s == nil || sp.s.herdr == nil {
		return nil, false
	}
	list, err := sp.s.herdr.Agents(ctx)
	if err != nil {
		return nil, false
	}
	m := map[string]liveAgent{}
	for _, a := range list {
		if a.Name != nil {
			la := liveAgent{Pane: a.PaneID, Status: a.AgentStatus, Ready: a.Ready, Kind: deref(a.Agent)}
			if a.Session != nil {
				la.Session = sessionID(a.Session.Kind, a.Session.Value)
			}
			m[*a.Name] = la
		}
	}
	return m, true
}

func (sp *Super) tick(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	live, ok := sp.liveAgents(ctx)
	if !ok {
		return // herdr unavailable; keep everything queued
	}
	now := time.Now()

	type finished struct {
		w       *Worker
		blocked bool
	}
	var fin []finished
	var dead []*Worker // the agent never started, or quit, leaving a shell
	deliveries := map[string][]*Trigger{}

	sp.mu.Lock()
	paused := sp.st.Settings.Paused
	// 1. Timers → triggers.
	for _, t := range sp.st.Timers {
		if t.Enabled && !t.NextRun.After(now) {
			at := now
			t.LastRun = &at
			t.advance(now)
			text := t.Text
			var meta []string
			if t.Machine != "" {
				meta = append(meta, "machine="+t.Machine)
			}
			if t.Group != "" {
				meta = append(meta, "group="+t.Group)
			}
			if t.Role != "" {
				meta = append(meta, "role="+t.Role)
			}
			if len(meta) > 0 {
				text = "[timer dispatch: " + strings.Join(meta, " ") + "] " + text
			}
			sp.ingestLocked("timer", t.Thread, t.ID, text, "", t.Target, t.ID)
		}
	}
	// 2. Worker lifecycle → triggers for the router.
	for _, w := range sp.st.Workers {
		if w.Ended {
			// herdr restored its pane (a herdr restart or update): it's back.
			if a, ok := live[w.Name]; ok && w.Status == "gone" && w.PaneID != "" && a.Pane == w.PaneID {
				w.Ended, w.Status = false, a.Status
				sp.ingestLocked("worker", w.Thread, w.Name, fmt.Sprintf("worker %s is back: herdr restored its pane (status %s).", w.Name, a.Status), "", "", w.Name)
				sp.addEntryLocked("event", w.Thread, w.Name+" 回来了（herdr 恢复了它的窗格）", w.Name, "info")
				sp.changed()
			}
			continue
		}
		a, alive := live[w.Name]
		if !alive {
			if w.Status != "starting" && w.Status != "failed" {
				w.Ended, w.Status = true, "gone"
				sp.ingestLocked("worker", w.Thread, w.Name, fmt.Sprintf("worker %s exited (its herdr pane is gone).", w.Name), "", "", w.Name)
				sp.addEntryLocked("event", w.Thread, w.Name+" 已退出", w.Name, "info")
			}
			continue
		}
		// No agent in the pane for a while: it failed to start (a broken
		// install, a bad flag) or quit. Messages for it would wait forever.
		if a.Kind == "" {
			since, seen := sp.noAgent[w.Name]
			if !seen {
				sp.noAgent[w.Name], since = now, now
			}
			if now.Sub(since) > 30*time.Second && now.Sub(w.CreatedAt) > 45*time.Second {
				delete(sp.noAgent, w.Name)
				w.Ended, w.Status, w.PaneID = true, "failed", a.Pane
				for _, t := range sp.st.Triggers {
					if t.Status == "queued" && len(t.Targets) == 1 && t.Targets[0] == w.Name {
						t.Status = "ignored"
					}
				}
				dead = append(dead, w)
				sp.changed()
			}
			continue
		}
		delete(sp.noAgent, w.Name)
		if a.Session != "" && a.Session != w.Session {
			w.Session = a.Session
			sp.dirty = true
		}
		prev := w.Status
		if a.Status == "blocked" && now.Sub(sp.lastAnswer[a.Pane]) < 2*time.Second {
			continue // give a just-answered dialog a moment to clear (or show the next one)
		}
		if a.Status == "done" || a.Status == "idle" {
			if w.IdleSince.IsZero() {
				w.IdleSince, sp.dirty = now, true
			}
		} else if !w.IdleSince.IsZero() {
			w.IdleSince, sp.dirty = time.Time{}, true
		}
		if a.Status != prev {
			w.Status = a.Status
			w.PaneID = a.Pane
			sp.changed()
			switch {
			case a.Status == "blocked":
				fin = append(fin, finished{w, true})
			case prev == "working" && (a.Status == "done" || a.Status == "idle"):
				if now.Sub(w.ReportAt) > 90*time.Second {
					fin = append(fin, finished{w, false})
				}
			}
		}
	}
	// 3. Route queued triggers to sessions: subscribed workers first (fast path), router otherwise.
	for _, t := range sp.st.Triggers {
		if t.Status != "queued" {
			continue
		}
		if len(t.Targets) == 0 {
			if t.Source != "worker" && t.Source != "chat" {
				if th := sp.st.Threads[t.Thread]; th != nil {
					for _, sub := range th.Subscribers {
						if w := sp.st.Workers[sub]; w != nil && !w.Ended && sub != t.From {
							t.Targets = append(t.Targets, sub)
						}
					}
				}
			}
			if len(t.Targets) == 0 {
				t.Targets = []string{routerName}
			}
		}
		for _, target := range t.Targets {
			deliveries[target] = append(deliveries[target], t)
		}
	}
	routerAlive := false
	_, routerAlive = live[routerName]
	sp.mu.Unlock()

	// Worker completion summaries need file/screen IO: do it outside the lock.
	for _, f := range fin {
		sp.workerSettled(ctx, f.w, f.blocked)
	}
	for _, w := range dead {
		sp.workerDead(ctx, w)
	}

	if paused {
		return
	}

	// 4. Deliver batches to free sessions.
	for target, list := range deliveries {
		if target == routerName && (sp.handingOver() || sp.dreaming()) {
			continue // events wait for the fresh session
		}
		a, alive := live[target]
		if !alive {
			if target == routerName {
				sp.ensureRouter(ctx)
			} else {
				sp.rerouteToRouter(target, list)
			}
			continue
		}
		if target == routerName && a.Status == "blocked" {
			sp.trustOwnDir(ctx, a.Pane)
		}
		// Nothing is typed over a dialog or into an agent that is still
		// starting. Otherwise a message goes in mid-turn: Claude, Codex and agy
		// queue typed input and pick it up between steps, so corrections and
		// questions arrive now rather than after the current step. Swag's
		// other events (reports, timers) batch up while it works; the human's
		// own messages don't wait.
		if a.Status == "blocked" || (!a.Ready && a.Status == "unknown") {
			continue
		}
		if a.Status == "working" && target == routerName {
			if list = fromHuman(list); len(list) == 0 {
				continue
			}
		}
		sp.deliver(ctx, target, list)
	}

	if a, ok := live[routerName]; ok && (a.Status == "done" || a.Status == "idle") {
		sp.routerSettled(ctx, a.Pane)
	}
	sp.checkHandover(ctx, live, len(deliveries[routerName]) > 0)
	sp.checkDream(ctx, live, len(deliveries[routerName]) > 0)

	// 5. Watchdog & idle worker auto-archiver.
	sp.checkWedgedRouter(ctx, live, deliveries[routerName])
	sp.watchdog(ctx, live, routerAlive)
	sp.maybeReapIdleWorkers(ctx, now)
}

// fromHuman keeps what the human said in the chat.
func fromHuman(list []*Trigger) []*Trigger {
	var out []*Trigger
	for _, t := range list {
		if t.Source == "chat" || (t.Source == "system" && t.From == "recall") {
			out = append(out, t)
		}
	}
	return out
}

func (sp *Super) rerouteToRouter(target string, list []*Trigger) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	w := sp.st.Workers[target]
	if w != nil && !w.Ended && (w.Status == "starting" || time.Since(w.CreatedAt) < 2*time.Minute) {
		return // still launching
	}
	for _, t := range list {
		t.Targets = replace(t.Targets, target, routerName)
	}
	sp.changed()
}

func replace(list []string, from, to string) []string {
	out := []string{}
	for _, x := range list {
		if x == from {
			x = to
		}
		if !contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// deliver submits all pending triggers for one session as a single prompt.
func (sp *Super) deliver(ctx context.Context, target string, list []*Trigger) {
	sort.Slice(list, func(i, j int) bool { return list[i].At.Before(list[j].At) })
	if len(list) > 20 {
		list = list[:20]
	}
	sp.mu.Lock()
	fresh := target == routerName && sp.routerFresh
	words := sp.st.Settings.ReplyChars
	sp.mu.Unlock()

	sessionStart := ""
	if fresh {
		sessionStart = sp.sessionStart()
	}
	err := sp.s.herdr.Prompt(ctx, target, formatDeliveryPrompt(target, sessionStart, list, words))
	var he *HerdrError
	if err != nil && errors.As(err, &he) && (he.Code == "agent_blocked" || he.Code == "agent_not_ready") {
		return
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if err != nil {
		log.Printf("deliver to %s: %v", target, err)
		return
	}
	if fresh {
		sp.routerFresh = false
	}
	if target == routerName && sp.routerAskedAt.IsZero() {
		sp.routerAskedAt = time.Now() // a message mid-turn keeps the turn's start
	}
	for _, t := range list {
		t.Targets = remove(t.Targets, target)
		if len(t.Targets) == 0 {
			t.Status = "delivered"
		}
	}
	sp.changed()
}

func remove(list []string, v string) []string {
	out := list[:0:0]
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func indent(s, p string) string {
	return p + strings.ReplaceAll(s, "\n", "\n"+p)
}

// workerSettled turns a worker reaching done/blocked into a router trigger,
// carrying a short summary from the worker's transcript or screen.
// workerDead tells the router and the human that a worker's agent isn't
// running, with what its screen says (usually the error it died with).
func (sp *Super) workerDead(ctx context.Context, w *Worker) {
	tail := ""
	if res, err := sp.s.herdr.Read(ctx, w.PaneID, "recent_unwrapped", "text", 40); err == nil {
		var keep []string
		for _, l := range strings.Split(res.Text, "\n") {
			if l = strings.TrimRight(l, " "); strings.TrimSpace(l) != "" {
				keep = append(keep, l)
			}
		}
		if len(keep) > 12 {
			keep = keep[len(keep)-12:]
		}
		tail = strings.Join(keep, "\n")
	}
	why := deathReason(tail)
	if len([]rune(why)) > 160 {
		why = string([]rune(why)[:160]) + "…"
	}
	log.Printf("worker %s: no agent in its pane %s; marked failed", w.Name, w.PaneID)
	if sp.tryNativeFallback(w, tail, why) {
		return
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.ingestLocked("worker", w.Thread, w.Name, fmt.Sprintf(
		"worker %s failed: %s isn't running in its pane (it never started, or it quit), so its task was not delivered. Last lines on its screen:\n%s\nFix the cause or tell the human; spawn a new worker to retry.",
		w.Name, w.Kind, tail), "", "", w.Name)
	msg := w.Name + " 的 " + w.Kind + " 没有跑起来（或已退出），任务没发出去"
	if why != "" {
		msg += "：" + why
	}
	sp.addEntryLocked("event", w.Thread, msg, w.Name, "error")
	sp.changed()
}

// deathReason picks the line that says why: "Error: …" over a stack frame
// like "throw new Error(".
func deathReason(screen string) string {
	var loose string
	for _, l := range strings.Split(screen, "\n") {
		t := strings.TrimSpace(l)
		low := strings.ToLower(t)
		if strings.Contains(low, "error:") || strings.HasPrefix(low, "fatal") || strings.Contains(low, "panic:") ||
			strings.Contains(low, "command not found") || strings.Contains(low, "no such file") {
			return t
		}
		if loose == "" && strings.Contains(low, "error") {
			loose = t
		}
	}
	return loose
}

func (sp *Super) workerSettled(ctx context.Context, w *Worker, blocked bool) {
	// The human chose to bypass permissions in settings, so a worker's
	// "trust this folder?" (the folder Swag started it in) isn't theirs to
	// click. Anything else still goes to them.
	sp.mu.Lock()
	ask := sp.st.Settings.AskPermissions
	sp.mu.Unlock()
	if blocked && !ask && sp.answerTrust(ctx, w.PaneID, w.Cwd) {
		sp.mu.Lock()
		sp.noteAnsweredLocked(w)
		sp.mu.Unlock()
		log.Printf("worker %s: trusted its folder %s", w.Name, w.Cwd)
		return
	}
	summary := sp.lastAssistant(ctx, w)
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if blocked {
		sp.ingestLocked("worker", w.Thread, w.Name,
			fmt.Sprintf("worker %s is BLOCKED on a dialog in its terminal (the human also sees it as a card in chat). Screen:\n%s%s\nIf the answer follows from what the human asked, or is routine, answer it yourself: `swag answer %s <choice>`. Leave it to the human only when it is theirs to decide (see ROUTER.md).", w.Name, summary, sp.choicesText(ctx, w.PaneID), w.Name), "", "", w.Name)
		sp.addEntryLocked("event", w.Thread, w.Name+" 等你确认", w.Name, "blocked")
		return
	}
	if msg := sp.workerAgentError(ctx, w, summary); msg != "" {
		// Its agent stopped it (out of quota, logged out, the API down):
		// the turn ended, the work did not.
		sp.ingestLocked("worker", w.Thread, w.Name,
			fmt.Sprintf("worker %s stopped on an error from its agent, not because it was done:\n%s\nLast message before it:\n%s\nIf this is an auth (401/403), usage limit, or API outage error, retry with `--auth-profile swag-pool --model tier-standard` or another working authenticated machine/profile (NEVER switch to an unauthenticated agent engine like claude if not logged in), then tell the human in one line what happened and who carries on.", w.Name, msg, summary), "", "", w.Name)
		sp.addEntryLocked("event", w.Thread, w.Name+" 出错了："+msg, w.Name, "error")
		return
	}
	if sp.handleWorkerNonProgressLocked(w, summary) {
		return
	}
	sp.ingestLocked("worker", w.Thread, w.Name,
		fmt.Sprintf("worker %s finished a turn. Last message:\n%s", w.Name, summary), "", "", w.Name)
	sp.addEntryLocked("event", w.Thread, w.Name+" 完成了一轮", w.Name, "done")
}

// workerQuotaRe is an agent CLI saying it can't go on: a usage limit, the
// login, the API. Narrower than agentErrRe: a worker's screen is full of its
// work, and "error:" there is mostly the work's own.
var workerQuotaRe = regexp.MustCompile(`(?i)(usage limit|limit reached|hit your( \w+)? limit|reached your( \w+)? limit|quota exceeded|exceeded your( current)? quota|resource.?exhausted|daily limit|rate.?limit(ed)?\b|credit balance|out of credits|please run /login|not logged in|invalid api key|api error|overloaded|unexpected status \d{3}|401 unauthorized|403 forbidden|try again (at|in) )`)
var workerHardAuthRe = regexp.MustCompile(`(?i)(unexpected status (401|403|429|5\d\d)|401 unauthorized|403 forbidden|invalid api key|not logged in|please run /login)`)

// workerAgentError reads the bottom of a worker's terminal after a turn:
// what its agent printed when it gave up, or "". A line that is also in its
// last message is the work talking about such things, not the agent.
func (sp *Super) workerAgentError(ctx context.Context, w *Worker, summary string) string {
	if w.PaneID == "" {
		return ""
	}
	rr, err := sp.s.herdr.Read(ctx, w.PaneID, "recent", "text", 40)
	if err != nil {
		return ""
	}
	return agentStopLine(rr.Text, summary)
}

func agentStopLine(screen, summary string) string {
	var tail []string
	for _, l := range strings.Split(screen, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			tail = append(tail, t)
		}
	}
	if len(tail) > 12 {
		tail = tail[len(tail)-12:]
	}
	for _, l := range tail {
		trimmed := strings.Trim(l, "⎿⏺●•✗✘⚠■│ ")
		if workerQuotaRe.MatchString(l) && (workerHardAuthRe.MatchString(l) || !strings.Contains(summary, trimmed)) {
			return clip(trimmed, 200)
		}
	}
	return ""
}

func (sp *Super) lastAssistant(ctx context.Context, w *Worker) string {
	if pane, machine := split(w.PaneID); machine != "" {
		// Its transcript lives on that computer: ask its node.
		if c, err := sp.s.herdr.Client(machine); err == nil {
			req, _ := http.NewRequestWithContext(ctx, "GET", "http://node/api/panes/"+url.PathEscape(pane)+"/last", nil)
			if resp, err := c.Do(req); err == nil {
				var out struct {
					Text string `json:"text"`
				}
				json.NewDecoder(resp.Body).Decode(&out)
				resp.Body.Close()
				if out.Text != "" {
					return clip(out.Text, 1200)
				}
			}
		}
	} else if w.PaneID != "" {
		var info struct {
			Agent agentDetail `json:"agent"`
		}
		if sp.s.herdr.Call(ctx, "agent.get", map[string]any{"target": w.Name}, &info) == nil {
			if tr := sp.s.transcriptFor(w.Name, w.PaneID, info.Agent, sp.transcripts); tr != nil {
				tr.update()
				msgs := tr.view()
				for i := len(msgs) - 1; i >= 0; i-- {
					if msgs[i].Kind == "assistant" {
						return clip(msgs[i].Text, 1200)
					}
				}
			}
		}
		if res, err := sp.s.herdr.Read(ctx, w.PaneID, "detection", "text", 0); err == nil {
			return tailLines(res.Text, 14)
		}
	}
	return "(no output captured)"
}

// ---------------------------------------------------------------- router

func (sp *Super) routerDir() string { return filepath.Join(sp.dir, "router") }

func (sp *Super) ensureRouter(ctx context.Context) {
	if sp.s == nil || sp.s.herdr == nil {
		return
	}
	sp.mu.Lock()
	if sp.routerLaunching || time.Since(sp.routerLaunchAt) < 45*time.Second {
		sp.mu.Unlock()
		return
	}
	sp.routerLaunching, sp.routerLaunchAt = true, time.Now()
	kind := sp.st.Settings.RouterKind
	args := sp.routerArgsLocked()
	routerMachineWant := strings.TrimSpace(sp.st.Settings.RouterMachine)
	routerProfile := strings.TrimSpace(sp.st.Settings.RouterAuthProfile)
	sp.setRouterRun([2]string{sp.st.Settings.RouterModel, sp.st.Settings.RouterEffort})
	sp.mu.Unlock()

	go func() {
		defer func() { sp.mu.Lock(); sp.routerLaunching = false; sp.mu.Unlock() }()
		sp.closeStaleRouterWorkspaces(context.Background())
		if err := sp.writeRouterFiles(); err != nil {
			log.Printf("router files: %v", err)
		}
		sp.routerReadsFresh()
		lctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		targetMachine := ""
		workDir := sp.routerDir()
		if routerMachineWant != "" && routerMachineWant != "hub" {
			if mid := sp.s.machineByName(routerMachineWant); mid != "" {
				if dir, err := sp.pushDreamWork(lctx, mid); err == nil && dir != "" {
					targetMachine = mid
					workDir = dir
				} else {
					log.Printf("router machine %s sync failed (falling back to hub): %v", routerMachineWant, err)
				}
			}
		}
		_, _, err := sp.s.startAgentWithProfile(lctx, targetMachine, kinds[kind], routerName, workDir, routerLabel, args, routerProfile)
		sp.mu.Lock()
		defer sp.mu.Unlock()
		var he *HerdrError
		if err != nil && !(errors.As(err, &he) && he.Code == "agent_not_ready") {
			msg := "Swag 启动失败：" + err.Error()
			if strings.Contains(err.Error(), "没装") {
				msg += "。装上它，或在 自动化 → 设置 → Swag 自己 里换一个 agent"
			}
			sp.addEntryLocked("event", mainThread, msg, routerName, "error")
			return
		}
		sp.routerFresh = true
		where := ""
		if targetMachine != "" {
			where = " @ " + routerMachineWant
		}
		sp.addEntryLocked("event", mainThread, "Swag 已唤醒（"+kind+where+"）", routerName, "info")
	}()
}

func (sp *Super) RestartRouter(ctx context.Context) error {
	if live, ok := sp.liveAgents(ctx); ok {
		if a, ok := live[routerName]; ok {
			var p Pane
			if err := sp.s.herdr.Call(ctx, "pane.get", map[string]any{"pane_id": a.Pane}, &struct {
				Pane *Pane `json:"pane"`
			}{&p}); err == nil && p.WorkspaceID != "" {
				sp.s.herdr.Call(ctx, "workspace.close", map[string]any{"workspace_id": p.WorkspaceID}, nil)
			}
		}
	}
	sp.mu.Lock()
	sp.routerLaunchAt = time.Time{}
	sp.routerAskedAt = time.Time{}
	sp.routerNudged = false
	sp.routerRetries = 0
	sp.mu.Unlock()
	sp.ensureRouter(ctx)
	return nil
}

// followLocked is the soonest one-off timer Swag set itself ("in 30m",
// "at …"): a follow-up. Routines like a weekday check are not one.
func (sp *Super) followLocked() *followView {
	var f *followView
	for _, t := range sp.st.Timers {
		if t.Enabled && t.Owner != "you" && (strings.HasPrefix(t.Spec, "in ") || strings.HasPrefix(t.Spec, "at ")) && t.NextRun.After(time.Now()) {
			if f == nil || t.NextRun.Before(f.Next) {
				f = &followView{Next: t.NextRun, NextText: clip(firstLine(t.Text), 80)}
			}
		}
	}
	return f
}

// routerReadFile keeps a copy of the ROUTER.md the running Swag read.
const routerReadFile = ".router-read.md"

// routerDocChanged: ROUTER.md was just written (a new build, new settings).
// The running Swag read it when its session began; if it now says
// something else, have it read it again.
func (sp *Super) routerDocChanged(dir string, doc []byte) {
	read, err := os.ReadFile(filepath.Join(dir, routerReadFile))
	if err != nil || bytes.Equal(read, doc) {
		return // no session has read it yet, or nothing changed
	}
	os.WriteFile(filepath.Join(dir, routerReadFile), doc, 0o600)
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if t := sp.rereadTrig; t != nil && t.Status == "queued" {
		return // already asked
	}
	sp.rereadTrig = sp.ingestLocked("system", mainThread, "swag", "ROUTER.md changed (a new Swag build or new settings). Re-read it now and work by it from here on. Nothing to tell the human.", "", "", "")
}

// routerReadsFresh: a new session is starting and reads ROUTER.md as it is.
func (sp *Super) routerReadsFresh() {
	dir := sp.routerDir()
	if b, err := os.ReadFile(filepath.Join(dir, "ROUTER.md")); err == nil {
		os.WriteFile(filepath.Join(dir, routerReadFile), b, 0o600)
	}
	sp.mu.Lock()
	if t := sp.rereadTrig; t != nil && t.Status == "queued" {
		t.Status = "ignored" // it reads them anyway
	}
	sp.rereadTrig = nil
	sp.mu.Unlock()
}

func (sp *Super) writeRouterFiles() error {
	dir := sp.routerDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	sp.mu.Lock()
	words := sp.st.Settings.ReplyChars
	wkind := sp.st.Settings.WorkerKind
	wcwd := sp.s.tilde(sp.st.Settings.WorkerCwd)
	remoteRouter := sp.st.Settings.RouterMachine != "" && sp.st.Settings.RouterMachine != "hub"
	sp.mu.Unlock()
	binName := sp.bin
	if remoteRouter {
		binName = "swag"
	}
	layout := "   " + strings.ReplaceAll(memoryLayout, "\n", "\n   ")
	doc := strings.NewReplacer("{{LAYOUT}}", layout, "{{BIN}}", binName, "{{WORDS}}", fmt.Sprint(words), "{{WKIND}}", wkind, "{{WCWD}}", wcwd, "{{BUDGET}}", fmt.Sprint(upFrontBudget>>10)).Replace(routerDoc)
	for _, name := range []string{"ROUTER.md", "CLAUDE.md", "AGENTS.md", "GEMINI.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(doc), 0o600); err != nil {
			return err
		}
	}
	sp.routerDocChanged(dir, []byte(doc))
	// Claude Code asks before every shell command; the router's whole job is
	// running the swag CLI and keeping memory/, so allow exactly that.
	perms, _ := json.MarshalIndent(map[string]any{"permissions": map[string]any{
		"allow": deckAllow(sp.bin, append(memoryRules(dir),
			// Read-only peeks at a worker's terminal.
			"Bash(herdr agent read:*)", "Bash(herdr agent list:*)", "Bash(herdr agent get:*)", "Bash(herdr pane read:*)")...),
	}}, "", "  ")
	os.MkdirAll(filepath.Join(dir, ".claude"), 0o700)
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), perms, 0o600); err != nil {
		return err
	}
	sp.ensureMemory()
	return nil
}

// ---------------------------------------------------------------- workers

var workerNameRe = regexp.MustCompile(`[^a-z0-9_-]+`)

// Spawn starts a worker session asynchronously (start() returns at once; the
// outcome arrives later as a trigger — the async ReAct pattern).
// model and effort are launch choices for this worker; "" falls back to the
// default worker settings when kind is the default kind, else the agent's own.
// resumeFrom is what `spawn --resume` carries over from a stopped worker:
// where it ran and the conversation to open again.
func (sp *Super) resumeFrom(name string) (*Worker, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	w := sp.st.Workers[name]
	switch {
	case w == nil:
		return nil, fmt.Errorf("no worker named %s", name)
	case !w.Ended:
		return nil, fmt.Errorf("%s is still running: `send` it instead", name)
	case w.Session == "":
		return nil, fmt.Errorf("%s left no conversation to resume; spawn a fresh worker", name)
	}
	c := *w
	return &c, nil
}

// SpawnOptions carries optional role/group/auth-profile overrides for SpawnWithOptions.
type SpawnOptions struct {
	Role        string
	Group       string
	AuthProfile string
	MachineSet  bool // whether caller explicitly provided machine
	ForceNative bool
}

func (sp *Super) pickMachineForGroupLocked(group string) (string, error) {
	group = strings.TrimSpace(group)
	for _, m := range sp.s.herdr.Machines() {
		if !m.Local && !m.Online {
			continue
		}
		pol := sp.st.Settings.PolicyFor(m.ID, m.Local)
		if !pol.DispatchEnabled {
			continue
		}
		if group == "" || strings.EqualFold(pol.Group, group) {
			return m.ID, nil
		}
	}
	return "", fmt.Errorf("no online dispatch-enabled machine in group %q (enable dispatch in 设备设置)", group)
}

func (sp *Super) Spawn(kind, cwd, task, thread, name, by, machine, model, effort string, subscribe bool, resume string) (*Worker, <-chan error, error) {
	return sp.SpawnWithOptions(kind, cwd, task, thread, name, by, machine, model, effort, subscribe, resume, SpawnOptions{MachineSet: machine != ""})
}

func (sp *Super) SpawnWithOptions(kind, cwd, task, thread, name, by, machine, model, effort string, subscribe bool, resume string, opts SpawnOptions) (*Worker, <-chan error, error) {
	sp.mu.Lock()
	var roleAsk *bool
	authProfile := strings.TrimSpace(opts.AuthProfile)
	group := strings.TrimSpace(opts.Group)
	roleName := strings.TrimSpace(opts.Role)
	if roleName != "" {
		if rp, ok := sp.st.Settings.Roles[roleName]; ok {
			if kind == "" && rp.Kind != "" {
				kind = rp.Kind
			}
			if cwd == "" && rp.Cwd != "" {
				cwd = rp.Cwd
			}
			if model == "" && rp.Model != "" {
				model = rp.Model
			}
			if effort == "" && rp.Effort != "" {
				effort = rp.Effort
			}
			if authProfile == "" && rp.AuthProfile != "" {
				authProfile = rp.AuthProfile
			}
			if !opts.MachineSet && machine == "" {
				if rp.Machine != "" {
					machine = sp.s.machineByName(rp.Machine)
				} else if group == "" && rp.Group != "" {
					group = rp.Group
				}
			}
			roleAsk = rp.AskPermissions
		}
	}
	if authProfile == "" && !opts.ForceNative {
		authProfile = strings.TrimSpace(sp.st.Settings.WorkerAuthProfile)
	}
	nativeFallbackTriggered := false
	if !opts.ForceNative && resolveNativeFallback(&authProfile, &model) {
		nativeFallbackTriggered = true
	}
	if !opts.MachineSet && group != "" && resume == "" {
		picked, err := sp.pickMachineForGroupLocked(group)
		if err != nil {
			sp.mu.Unlock()
			return nil, nil, err
		}
		machine = picked
	}
	sp.mu.Unlock()

	m, err := sp.s.herdr.machine(machine)
	if err != nil {
		return nil, nil, fmt.Errorf("machine %q is not connected", machine)
	}
	sp.mu.Lock()
	// Enforce machine dispatch pool for automated dispatch (Router/Timers).
	pol := sp.st.Settings.PolicyFor(m.ID, m.Local)
	if by == routerName && resume == "" && !pol.DispatchEnabled {
		sp.mu.Unlock()
		return nil, nil, fmt.Errorf("机器「%s」当前未加入派活池（group=%s），请先在「设备」页开启允许自动派活", m.Name, pol.Group)
	}
	if kind == "" {
		kind = sp.st.Settings.WorkerKind
	}
	if _, ok := kinds[kind]; !ok {
		sp.mu.Unlock()
		return nil, nil, fmt.Errorf("unknown kind %q (claude, codex, antigravity)", kind)
	}
	// Say so now, while the router can still pick another computer or kind.
	have := m.Agents
	if m.Local {
		have = localAgents(sp.s.home, false)
	}
	if have != nil && !slices.Contains(have, kinds[kind]) {
		sp.mu.Unlock()
		return nil, nil, fmt.Errorf("%s has no %s installed (it has: %s); pick another computer or kind", m.Name, kind, strings.Join(append(have, "none")[:max(len(have), 1)], ", "))
	}
	if cwd == "" && (m.ID == sp.st.Settings.WorkerMachine || (m.Local && sp.st.Settings.WorkerMachine == "")) {
		cwd = sp.st.Settings.WorkerCwd
	}
	if thread == "" {
		thread = mainThread
	}
	if name == "" {
		base := kind
		if base == "antigravity" {
			base = "agy"
		}
		for i := 1; ; i++ {
			n := fmt.Sprintf("%s-%d", base, i)
			if w := sp.st.Workers[n]; w == nil || w.Ended {
				name = n
				break
			}
		}
	}
	name = strings.Trim(workerNameRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if !nameRe.MatchString(name) || name == routerName {
		sp.mu.Unlock()
		return nil, nil, fmt.Errorf("invalid worker name %q", name)
	}
	if w := sp.st.Workers[name]; w != nil && !w.Ended {
		sp.mu.Unlock()
		return nil, nil, fmt.Errorf("worker %s already exists", name)
	}
	dir := expandIn(m.Home, cwd)
	if m.Local && !isDir(dir) {
		sp.mu.Unlock()
		return nil, nil, fmt.Errorf("directory not found: %s", dir)
	}
	w := &Worker{Name: name, Kind: kind, Model: model, Effort: effort, Role: roleName, AuthProfile: authProfile, FallbackUsed: nativeFallbackTriggered || opts.ForceNative, Machine: m.ID, Cwd: dir, Task: task, Thread: thread, CreatedBy: by, CreatedAt: time.Now(), Status: "starting", Session: resume}
	if nativeFallbackTriggered {
		sp.addEntryLocked("event", thread, "模型池暂无可用上游，"+name+" 已自动切换原生订阅直连兜底", name, "info")
	}
	sp.st.Workers[name] = w
	if subscribe {
		th := sp.threadLocked(thread)
		if !contains(th.Subscribers, name) {
			th.Subscribers = append(th.Subscribers, name)
		}
	}
	where := ""
	if !m.Local {
		where = " @ " + m.Name
	}
	verb := "派出"
	if resume != "" {
		verb = "续上"
	}
	sp.addEntryLocked("event", thread, fmt.Sprintf("%s %s（%s%s）：%s", verb, name, kind, where, clip(firstLine(task), 120)), name, "spawn")
	sp.mu.Unlock()

	// The default model applies to the default kind of worker.
	sp.mu.Lock()
	if kinds[kind] == kinds[sp.st.Settings.WorkerKind] {
		if model == "" {
			model = sp.st.Settings.WorkerModel
		}
		if effort == "" {
			effort = sp.st.Settings.WorkerEffort
		}
	}
	w.Run = &[2]string{model, effort}
	ask := sp.st.Settings.AskPermissions
	if roleAsk != nil {
		ask = *roleAsk
	}
	sp.mu.Unlock()
	wargs := withBypass(modelArgs(kind, model, effort), kind, ask)

	// started answers whoever is still waiting on the spawn (superSpawn waits
	// a few seconds): a failure it hears it can say at once, as the spawn's
	// own error, instead of as an event after it already said "started".
	started := make(chan error)
	tell := func(err error) bool {
		select {
		case started <- err:
			return true
		default:
			return false
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		bin := sp.bin
		if !m.Local && m.Bin != "" {
			bin = m.Bin
		} else if !m.Local {
			bin = "swag"
		}
		// codex's resume is a subcommand: it goes first, the flags after it.
		args := append(resumeArgs(kinds[kind], resume), wargs...)
		if kinds[kind] == "claude" {
			// Reporting back is part of the job; don't make it a permission prompt.
			args = append(args, "--allowedTools", strings.Join(deckAllow(bin), ","))
		}
		pane, ws, err := sp.s.startAgentWithProfile(ctx, m.ID, kinds[kind], name, dir, name, args, authProfile)
		if pane != "" {
			sp.s.herdr.RememberRun(pane, model, effort)
		}
		sp.mu.Lock()
		w.PaneID, w.WorkspaceID = pane, ws
		var he *HerdrError
		blocked := err != nil && errors.As(err, &he) && he.Code == "agent_not_ready"
		if err != nil && !blocked {
			w.Status, w.Ended, w.Note = "failed", true, err.Error()
			sp.addEntryLocked("event", thread, name+" 启动失败："+err.Error(), name, "error")
			sp.mu.Unlock()
			if !tell(err) {
				sp.Ingest("worker", thread, name, fmt.Sprintf("worker %s failed to start: %v", name, err), "", "", name)
			}
			return
		}
		w.Status = "idle"
		if blocked {
			w.Status = "blocked"
		}
		sp.changed()
		sp.mu.Unlock()
		tell(nil)
		// The task brief is delivered like any other trigger, so it waits
		// politely if the worker is stuck on a startup dialog.
		sp.Ingest("system", thread, routerName, workerBrief(bin, w), "", name, name)
	}()
	return w, started, nil
}

// choicesText lists a dialog's choices for the router, numbered as `swag
// answer` takes them.
func (sp *Super) choicesText(ctx context.Context, pane string) string {
	rr, err := sp.s.herdr.Read(ctx, pane, "detection", "text", 0)
	if err != nil || !screenAsks(rr.Text) {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nChoices:")
	for i, c := range parseApproval(rr.Text).Choices {
		fmt.Fprintf(&b, "\n  %d. %s", i+1, c.Label)
	}
	return b.String()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func workerBrief(bin string, w *Worker) string {
	return fmt.Sprintf(`You are worker %q (thread %s), started locally by the machine owner via their Swag control deck to execute this user-authorized task:

%s

Notes:
- Use local CLI %s report "…" to send your progress/result back to the owner's local Swag UI when done.
- When blocked on a human decision or missing access, report "BLOCKED: <what you need> · tried: <what you tried> · I'd pick: <your choice>".
- Follow-ups from the owner arrive prefixed with [swag].`, w.Name, w.Thread, strings.TrimSpace(w.Task), bin)
}

func (sp *Super) Report(name, text string) error {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	w := sp.st.Workers[name]
	if w == nil {
		return fmt.Errorf("unknown worker %s", name)
	}
	w.LastReport, w.ReportAt = text, time.Now()
	sp.ingestLocked("worker", w.Thread, name, fmt.Sprintf("report from %s:\n%s", name, text), "", "", name)
	sp.addEntryLocked("event", w.Thread, name+" 回报："+clip(text, 160), name, "done")
	return nil
}

func (sp *Super) workerByPane(pane string) string {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	for _, w := range sp.st.Workers {
		if w.PaneID == pane && !w.Ended {
			return w.Name
		}
	}
	return ""
}

// ---------------------------------------------------------------- watchdog

// watchdog is the external control loop: it restarts the router when work is
// waiting and flags workers that look stuck (screen unchanged while working).
func (sp *Super) watchdog(ctx context.Context, live map[string]liveAgent, routerAlive bool) {
	sp.mu.Lock()
	stall := time.Duration(sp.st.Settings.StallMinutes) * time.Minute
	var watch []*Worker
	for _, w := range sp.st.Workers {
		if !w.Ended && w.Status == "working" {
			watch = append(watch, w)
		}
	}
	sp.mu.Unlock()
	for _, w := range watch {
		a, ok := live[w.Name]
		if !ok {
			continue
		}
		res, err := sp.s.herdr.Read(ctx, a.Pane, "detection", "text", 0)
		if err != nil {
			continue
		}
		h := progressHash(res.Text)
		m := sp.lastScreen[w.Name]
		if m.hash != h {
			sp.lastScreen[w.Name] = screenMark{hash: h, since: time.Now()}
			continue
		}
		if !m.flag && stall > 0 && time.Since(m.since) > stall {
			m.flag = true
			sp.lastScreen[w.Name] = m
			sp.Ingest("system", w.Thread, "watchdog",
				fmt.Sprintf("watchdog: worker %s has shown no visible progress for %d min while 'working'. Inspect with `swag show %s`; send guidance, stop it, or tell the human.", w.Name, int(stall.Minutes()), w.Name), "", "", w.Name)
		}
	}
	sp.watchRouter(ctx, live, stall)
}

// watchRouter catches Swag itself hanging mid-turn (a stream that never
// ends, a tool that never returns): nothing but the timer moves on its screen.
// The first time it interrupts and tells Swag to carry on; if that turn
// hangs again, it tells the human instead of looping.
func (sp *Super) watchRouter(ctx context.Context, live map[string]liveAgent, stall time.Duration) {
	a, ok := live[routerName]
	if !ok || stall <= 0 || a.Status != "working" {
		sp.mu.Lock()
		delete(sp.lastScreen, routerName)
		if ok && (a.Status == "done" || a.Status == "idle") {
			sp.routerNudged = false
		}
		sp.mu.Unlock()
		return
	}
	res, err := sp.s.herdr.Read(ctx, a.Pane, "detection", "text", 0)
	if err != nil {
		return
	}
	h := progressHash(res.Text)
	sp.mu.Lock()
	m := sp.lastScreen[routerName]
	if m.hash != h {
		sp.lastScreen[routerName] = screenMark{hash: h, since: time.Now()}
		sp.mu.Unlock()
		return
	}
	if m.flag || time.Since(m.since) < stall {
		sp.mu.Unlock()
		return
	}
	m.flag = true
	sp.lastScreen[routerName] = m
	mins := int(time.Since(m.since).Minutes())
	if sp.routerNudged {
		sp.addEntryLocked("event", mainThread, fmt.Sprintf("Swag 又卡住了（%d 分钟没有动静，自动打断过一次没好）；可以在终端里看看它，或在 自动化 → 设置 里给 Swag 换一个 agent", mins), routerName, "error")
		sp.mu.Unlock()
		return
	}
	sp.routerNudged = true
	sp.addEntryLocked("event", mainThread, fmt.Sprintf("Swag 卡住了 %d 分钟没有动静，已自动打断让它继续", mins), "", "info")
	sp.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		sp.s.herdr.Call(ctx, "pane.send_keys", map[string]any{"pane_id": a.Pane, "keys": []string{"esc"}}, nil)
		time.Sleep(2 * time.Second)
		if err := sp.s.herdr.Prompt(ctx, routerName, fmt.Sprintf("%s: nothing on your screen changed for %d min, so Swag interrupted it. Carry on with what you were doing; the events you had are still yours to handle. A command that hangs is a job for a worker: hand it over.", routerRetryMark, mins)); err != nil {
			log.Printf("router nudge: %v", err)
			return
		}
		sp.mu.Lock()
		if sp.routerAskedAt.IsZero() {
			sp.routerAskedAt = time.Now()
		}
		sp.mu.Unlock()
	}()
}

// progressHash fingerprints a pane's screen for the watchdog, leaving out what
// moves on its own while an agent waits: the spinner line ("✻ Thinking… (3m
// 21s · esc to interrupt)", "Working (12s • esc to interrupt)") and numbers.
func progressHash(screen string) string {
	var b strings.Builder
	for _, l := range strings.Split(screen, "\n") {
		if strings.Contains(strings.ToLower(l), "esc to interrupt") || strings.Contains(strings.ToLower(l), "esc to cancel") {
			continue
		}
		b.WriteString(strings.Map(func(r rune) rune {
			if unicode.IsDigit(r) {
				return -1
			}
			return r
		}, l))
		b.WriteByte('\n')
	}
	return fmt.Sprintf("%x", sha1.Sum([]byte(b.String())))
}

// ---------------------------------------------------------------- outbox

func (sp *Super) queueNotifyLocked(role, text, tone string, re *Quote, check bool, opts ...string) {
	if sp.st.Settings.NotifyWebhook == "" && !sp.st.Settings.NotifyLark {
		return
	}
	if sp.st.Settings.NotifyLevel == "quiet" && role == "swag" && len(opts) == 0 && !check {
		return
	}
	sp.notifyBuf = append(sp.notifyBuf, notice{tone: tone, text: text, re: re, options: opts, check: check})
}

// A notice is one thing to tell you: what Swag said (and what about), or
// what needs you.
type notice struct {
	tone, text string
	entryID    string
	re         *Quote
	options    []string
	optionURLs []string
	check      bool
}

func (n notice) needsYou() bool {
	return n.tone == "blocked" || len(n.options) > 0 || n.check
}

// quoteLine is a Quote as one line: "你：首页加一个暗色模式…".
func (q *Quote) line() string {
	t := strings.Join(strings.Fields(q.Text), " ")
	return q.Who + "：" + clip(t, 60)
}

// quoteLocked finds what a reply's --re points at: an event Swag was given
// (#t942), or an entry. Unknown ids give nil: the reply goes out anyway.
func (sp *Super) quoteLocked(id string) *Quote {
	id = strings.TrimPrefix(strings.TrimSpace(id), "#")
	if id == "" {
		return nil
	}
	entry := func(eid string) *Quote {
		for i := len(sp.st.Entries) - 1; i >= 0; i-- {
			if e := sp.st.Entries[i]; e.ID == eid && e.Role == "user" {
				return &Quote{Entry: e.ID, Who: "你", Text: humanText(e.Text)}
			}
		}
		return nil
	}
	if q := entry(id); q != nil {
		return q
	}
	t := sp.st.trigger(id)
	if t == nil {
		return nil
	}
	switch t.Source {
	case "chat":
		if q := entry(t.Ref); q != nil {
			return q
		}
		return &Quote{Who: "你", Text: humanText(t.Text)}
	case "timer":
		return &Quote{Who: "定时", Text: t.Text}
	}
	name := t.Ref
	if name == "" {
		name = t.From
	}
	if w := sp.st.Workers[name]; w != nil {
		return &Quote{Worker: w.Name, Who: w.Name, Text: w.Task}
	}
	return nil
}

// deckQuoteLocked is a message of Swag's you answer, as a Quote.
func (sp *Super) deckQuoteLocked(id string) *Quote {
	if id == "" {
		return nil
	}
	for i := len(sp.st.Entries) - 1; i >= 0; i-- {
		if e := sp.st.Entries[i]; e.ID == id && e.Role == "swag" {
			return &Quote{Entry: e.ID, Who: "Swag", Text: clip(e.Text, 600)}
		}
	}
	if sp.db != nil {
		var raw string
		var e Entry
		if sp.db.QueryRow(`SELECT data FROM entries WHERE id = ?`, id).Scan(&raw) == nil &&
			json.Unmarshal([]byte(raw), &e) == nil && e.Role == "swag" {
			return &Quote{Entry: e.ID, Who: "Swag", Text: clip(e.Text, 600)}
		}
	}
	return nil
}

// pendingMaxAge bounds unanswered quick-option prompts so forgotten topics from
// past weeks never pile up as stale badges.
const pendingMaxAge = 7 * 24 * time.Hour

// PendingItem is one Swag message in chat:main (--opt or --check) that still
// awaits the human's answer, acknowledgement or dismissal.
type PendingItem struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Summary string    `json:"summary"`
	Options []string  `json:"options,omitempty"`
	Check   bool      `json:"check,omitempty"`
}

// PinnedItem is one message in chat:main manually pinned by the human. Unlike
// PendingItem, pins never expire on a 7-day timer and stay until unpinned.
type PinnedItem struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Summary string    `json:"summary"`
	Role    string    `json:"role"`
}

func pinnableEntry(e *Entry) bool {
	return e != nil && e.ID != "" && (e.Thread == "" || e.Thread == mainThread) && (e.Role == "swag" || e.Role == "user")
}

func (sp *Super) entryForPinLocked(id string) *Entry {
	for i := len(sp.st.Entries) - 1; i >= 0; i-- {
		if e := sp.st.Entries[i]; e.ID == id {
			if pinnableEntry(e) {
				return e
			}
			return nil
		}
	}
	if e, ok := sp.pinnedCache[id]; ok {
		return e
	}
	if sp.db != nil {
		var raw string
		var e Entry
		if sp.db.QueryRow(`SELECT data FROM entries WHERE id = ?`, id).Scan(&raw) == nil &&
			json.Unmarshal([]byte(raw), &e) == nil && pinnableEntry(&e) {
			if sp.pinnedCache == nil {
				sp.pinnedCache = map[string]*Entry{}
			}
			sp.pinnedCache[id] = &e
			return &e
		}
	}
	return nil
}

// Pin idempotently pins or unpins one chat:main message and persists it.
func (sp *Super) Pin(id string, pinned bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("id is required")
	}
	sp.mu.Lock()
	idx := slices.Index(sp.st.Pinned, id)
	changed := false
	if pinned {
		if idx < 0 {
			if sp.entryForPinLocked(id) == nil {
				sp.mu.Unlock()
				return errors.New("找不到这条可钉住的消息")
			}
			sp.st.Pinned = append(sp.st.Pinned, id)
			if n := len(sp.st.Pinned); n > maxPinned {
				for _, drop := range sp.st.Pinned[:n-maxPinned] {
					delete(sp.pinnedCache, drop)
				}
				sp.st.Pinned = append([]string(nil), sp.st.Pinned[n-maxPinned:]...)
			}
			changed = true
		}
	} else if idx >= 0 {
		sp.st.Pinned = slices.Delete(sp.st.Pinned, idx, idx+1)
		delete(sp.pinnedCache, id)
		changed = true
	}
	if changed {
		sp.changed()
	}
	hasDB := sp.db != nil
	sp.mu.Unlock()
	if changed && hasDB {
		sp.flush()
	}
	return nil
}

func (sp *Super) pinnedLocked() []PinnedItem {
	if len(sp.st.Pinned) == 0 {
		return []PinnedItem{}
	}
	out := make([]PinnedItem, 0, len(sp.st.Pinned))
	for _, id := range sp.st.Pinned {
		e := sp.entryForPinLocked(id)
		if e == nil {
			continue
		}
		sum := pendingSummary(e.Text, e.Options)
		if e.Role == "user" {
			sum = pendingSummary(humanText(e.Text), nil)
		}
		out = append(out, PinnedItem{
			ID:      e.ID,
			At:      e.At,
			Summary: sum,
			Role:    e.Role,
		})
	}
	return out
}

// Dismiss marks one pending message as acknowledged/ignored by the human and persists it.
func (sp *Super) Dismiss(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	sp.mu.Lock()
	if sp.st.Dismissed == nil {
		sp.st.Dismissed = map[string]bool{}
	}
	changed := !sp.st.Dismissed[id]
	if changed {
		sp.st.Dismissed[id] = true
		sp.changed()
	}
	hasDB := sp.db != nil
	sp.mu.Unlock()
	if changed && hasDB {
		sp.flush()
	}
}

// pendingLocked computes unanswered --opt and --check Swag messages in chat:main.
// An item resolves when:
//  1. the human tapped「已看」or × (/api/super/dismiss),
//  2. the human tapped one of its options or quoted it (re.entry == e.id),
//  3. the human sent a plain unquoted message right after it with no other
//     role=="swag" reply in between (system/worker events do not interrupt).
func (sp *Super) pendingLocked(now time.Time) []PendingItem {
	es := sp.st.Entries
	var out []PendingItem
	for i, e := range es {
		if e.Role != "swag" || (e.Thread != "" && e.Thread != mainThread) || (len(e.Options) == 0 && !e.Check) || e.ID == "" {
			continue
		}
		if sp.st.Dismissed[e.ID] {
			continue
		}
		if !now.IsZero() && !e.At.IsZero() && now.Sub(e.At) > pendingMaxAge {
			continue
		}
		opts := cleanOptions(e.Options)
		if len(opts) == 0 && !e.Check {
			continue
		}
		resolved, otherDeck := false, false
		for j := i + 1; j < len(es); j++ {
			next := es[j]
			if next.Thread != "" && next.Thread != mainThread {
				continue
			}
			switch next.Role {
			case "user":
				reID := ""
				if next.Re != nil {
					reID = next.Re.Entry
				}
				if reID == e.ID || (reID == "" && !otherDeck) {
					resolved = true
				}
			case "swag":
				otherDeck = true
			}
			if resolved {
				break
			}
		}
		if !resolved {
			out = append(out, PendingItem{
				ID:      e.ID,
				At:      e.At,
				Summary: pendingSummary(e.Text, opts),
				Options: opts,
				Check:   e.Check,
			})
		}
	}
	return out
}

var (
	pendingFenceRe = regexp.MustCompile("(?s)```.*?```")
	pendingImgRe   = regexp.MustCompile(`\[image:[^\]]+\]`)
	pendingLinkRe  = regexp.MustCompile(`\[([^\]\n]+)\]\([^)\n]+\)`)
	pendingListRe  = regexp.MustCompile(`^\s*(?:[#>*•\-]+|\d+[.)])\s+`)
)

func pendingSummary(text string, opts []string) string {
	s := pendingFenceRe.ReplaceAllString(text, " ")
	s = pendingImgRe.ReplaceAllString(s, " ")
	s = pendingLinkRe.ReplaceAllString(s, "$1")
	var parts []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(pendingListRe.ReplaceAllString(l, ""))
		l = strings.TrimSpace(mdMarks.Replace(l))
		if l != "" {
			parts = append(parts, l)
		}
	}
	out := oneLine(strings.Join(parts, " "))
	if out == "" {
		out = strings.Join(opts, " / ")
	}
	if out == "" {
		out = oneLine(text)
	}
	return clip(out, 120)
}

// humanText is your message as you wrote it: without the attachment list and
// the notes the hub adds for Swag.
func humanText(t string) string {
	if i := strings.Index(t, "\n\n[The human picked"); i >= 0 {
		t = t[:i]
	}
	if i := strings.LastIndex(t, "附件：\n"); i >= 0 {
		t = strings.TrimSpace(t[:i]) + " 📎"
	}
	return strings.TrimSpace(t)
}



// trustOwnDir answers the "do you trust this folder?" dialog for the router's
// own working directory. Swag created that directory and wrote every file in
// it, so asking the human adds friction without adding safety. Any other
// dialog (and any other directory) is left for the human.
func (sp *Super) trustOwnDir(ctx context.Context, pane string) {
	if sp.answerTrust(ctx, pane, sp.routerDir()) {
		log.Printf("router: trusted its own directory %s", sp.routerDir())
	}
}

func isTrustChoice(label string) bool {
	l := strings.ToLower(strings.TrimSpace(label))
	return (strings.HasPrefix(l, "yes") && strings.Contains(l, "trust")) || strings.HasPrefix(l, "trust and continue")
}

func (sp *Super) noteAnsweredLocked(w *Worker) {
	if sp.lastAnswer == nil {
		sp.lastAnswer = map[string]time.Time{}
	}
	if w != nil {
		if w.PaneID != "" {
			sp.lastAnswer[w.PaneID] = time.Now()
		}
		if w.Status == "blocked" {
			w.Status = "idle"
		}
	}
}

// answerTrust picks "Yes, I trust this folder" / "Trust and continue" when
// pane shows a folder-trust dialog for dir, the folder Swag started that
// agent in. It reports whether it answered. Nothing else is ever answered this way.
func (sp *Super) answerTrust(ctx context.Context, pane, dir string) bool {
	sp.mu.Lock()
	if sp.lastTrust == nil {
		sp.lastTrust = map[string]time.Time{}
	}
	if time.Since(sp.lastTrust[pane]) < 5*time.Second {
		sp.mu.Unlock()
		return false
	}
	sp.lastTrust[pane] = time.Now()
	sp.mu.Unlock()

	rr, err := sp.s.herdr.Read(ctx, pane, "detection", "text", 0)
	if err != nil || !trustDialogFor(rr.Text, dir, sp.s.tilde(dir)) {
		return false
	}
	for _, c := range parseApproval(rr.Text).Choices {
		if isTrustChoice(c.Label) {
			return sp.choose(ctx, pane, c) == nil
		}
	}
	return false
}

// choose answers a dialog in pane with one of its choices: cursor movement and
// Enter when the menu has a cursor, else its digit for a cursorless numbered menu.
func (sp *Super) choose(ctx context.Context, pane string, c Choice) error {
	if len(c.Keys) > 0 {
		return sp.s.herdr.Call(ctx, "pane.send_keys", map[string]any{"pane_id": pane, "keys": c.Keys}, nil)
	}
	if c.Text != "" {
		return sp.s.herdr.Call(ctx, "pane.send_text", map[string]any{"pane_id": pane, "text": c.Text}, nil)
	}
	return sp.s.herdr.Call(ctx, "pane.send_keys", map[string]any{"pane_id": pane, "keys": []string{"enter"}}, nil)
}

// pickChoice finds the choice the router named: its number as shown ("3"),
// its position, or words of its label.
func pickChoice(a Approval, want string) (Choice, bool) {
	want = strings.TrimSpace(want)
	for _, c := range a.Choices {
		if c.Text != "" && c.Text == want {
			return c, true
		}
	}
	if n, err := strconv.Atoi(want); err == nil && n >= 1 && n <= len(a.Choices) {
		return a.Choices[n-1], true
	}
	var hit []Choice
	for _, c := range a.Choices {
		if want != "" && strings.Contains(strings.ToLower(c.Label), strings.ToLower(want)) {
			hit = append(hit, c)
		}
	}
	if len(hit) == 1 {
		return hit[0], true
	}
	return Choice{}, false
}

// trustDialogFor: the screen asks whether to trust a folder, and names dir
// (as given, with ~, or by its last part: the screen may show the path a
// symlink resolves to).
func trustDialogFor(screen, dir, tilde string) bool {
	if !strings.Contains(strings.ToLower(screen), "trust") || dir == "" {
		return false
	}
	if strings.Contains(screen, dir) || strings.Contains(screen, tilde) {
		return true
	}
	return regexp.MustCompile(`(?m)/` + regexp.QuoteMeta(path.Base(dir)) + `/?\s*$`).MatchString(screen)
}

// closeStaleRouterWorkspaces removes router workspaces whose agent has exited,
// so a crashed router does not leave an orphan shell behind.
func (sp *Super) closeStaleRouterWorkspaces(ctx context.Context) {
	wss, err := sp.s.herdr.Workspaces(ctx)
	if err != nil {
		return
	}
	for _, ws := range wss {
		if _, m := split(ws.ID); ws.Label == routerLabel && m == "" {
			sp.s.herdr.Call(ctx, "workspace.close", map[string]any{"workspace_id": ws.ID}, nil)
		}
	}
}

// agentErrRe matches what an agent CLI prints when it can't run (the API
// refused, the login expired, the quota ran out), not Swag's own words about
// a task: "the backup failed twice" and "its error output" are work, not errors.
var agentErrRe = regexp.MustCompile(`(?i)(api error|please run /login|invalid api key|quota exceeded|daily limit|rate.?limit(ed)?\b|overloaded|unauthori[sz]ed|credit balance|unexpected status \d{3}|^[\W]*(⚠|■|error\b|error:))`)

// routerSettled runs once after the router finishes a turn that we prompted.
// If it went quiet without a `swag reply` and its screen shows an error
// (bad credentials, region block, quota…), surface that in the chat instead of
// leaving the human staring at silence.
func (sp *Super) routerSettled(ctx context.Context, pane string) {
	sp.mu.Lock()
	asked := sp.routerAskedAt
	if asked.IsZero() || time.Since(asked) < 4*time.Second {
		sp.mu.Unlock()
		return
	}
	sp.routerAskedAt = time.Time{}
	for i := len(sp.st.Entries) - 1; i >= 0; i-- {
		e := sp.st.Entries[i]
		if e.At.Before(asked) {
			break
		}
		if e.Role == "swag" {
			sp.routerRetries = 0
			sp.checkEmptyPromiseLocked(asked, e)
			sp.mu.Unlock()
			return
		}
	}
	sp.mu.Unlock()

	rr, err := sp.s.herdr.Read(ctx, pane, "recent", "text", 60)
	if err != nil {
		return
	}
	lines := strings.Split(rr.Text, "\n")
	// Only look at what the agent printed after our prompt, never at the event text itself.
	start := 0
	for i, l := range lines {
		if strings.Contains(l, "swag reply") || strings.Contains(l, "yourself.") || strings.Contains(l, routerRetryMark) {
			start = i + 1
		}
	}
	for i := start; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if l == "" || !isAgentFatalError(l) {
			continue
		}
		// Messages wrap in the terminal: keep following lines of the same block.
		msg := l
		for j := i + 1; j < len(lines) && j <= i+3; j++ {
			next := strings.TrimSpace(lines[j])
			if next == "" || strings.HasPrefix(next, "─") || strings.HasPrefix(next, "✻") || strings.HasPrefix(next, "Error ID") {
				break
			}
			msg += " " + next
		}
		if r := []rune(msg); len(r) > 240 {
			msg = string(r[:240]) + "…"
		}
		sp.mu.Lock()
		// Most of these pass (a region check, a busy API, a dropped
		// connection): try again before calling it an error.
		if n := sp.routerRetries; n < len(routerRetryWait) {
			sp.routerRetries++
			wait := routerRetryWait[n]
			sp.addEntryLocked("event", mainThread, fmt.Sprintf("Swag 遇到 API 错误（%s），%s后自动重试", clip(msg, 80), waitText(wait)), "", "info")
			sp.mu.Unlock()
			go sp.retryRouter(wait, msg)
			return
		}
		sp.routerRetries = 0
		text := "Swag 出错了：" + msg + "（自动重试了 " + fmt.Sprint(len(routerRetryWait)) + " 次都没好；可以在终端里让它继续，或在 自动化 → 设置 里给 Swag 换一个 agent）"
		for i := len(sp.st.Entries) - 1; i >= 0 && i >= len(sp.st.Entries)-6; i-- {
			if e := sp.st.Entries[i]; e.Text == text && time.Since(e.At) < 10*time.Minute {
				sp.mu.Unlock()
				return // same failure again; one notice is enough
			}
		}
		sp.addEntryLocked("event", mainThread, text, routerName, "error")
		sp.mu.Unlock()
		return
	}
	sp.mu.Lock()
	sp.routerRetries = 0 // a quiet turn, no error: it is working again
	sp.surfaceSilentRouterReplyLocked(asked, lines, start)
	remoteRouter := sp.st.Settings.RouterMachine != "" && sp.st.Settings.RouterMachine != "hub"
	sp.mu.Unlock()
	if remoteRouter {
		go sp.PullRemoteRouterMemory(context.Background(), false)
	}
}

// routerRetryWait: how long to wait before each retry of a turn that ended
// on an API error.
var routerRetryWait = []time.Duration{20 * time.Second, time.Minute, 3 * time.Minute}

// routerRetryMark starts the retry prompt; the error scan reads from there.
const routerRetryMark = "[swag] Your last turn stopped"

func waitText(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	}
	return fmt.Sprintf("%d 分钟", int(d.Minutes()))
}

// retryRouter nudges Swag to carry on after an API error, unless something
// else has got it going meanwhile (your next message, a worker's event).
func (sp *Super) retryRouter(wait time.Duration, msg string) {
	time.Sleep(wait)
	sp.mu.Lock()
	busy := !sp.routerAskedAt.IsZero() || sp.routerLaunching || sp.st.Settings.Paused || sp.handover != nil || sp.dream != nil
	sp.mu.Unlock()
	if busy {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := sp.s.herdr.Prompt(ctx, routerName, routerRetryMark+" on an error from your model's API ("+msg+"). It may have passed: carry on with what you were doing; the events you had are still yours to handle."); err != nil {
		log.Printf("router retry: %v", err)
		return
	}
	sp.mu.Lock()
	if sp.routerAskedAt.IsZero() {
		sp.routerAskedAt = time.Now() // so this turn is checked like any other
	}
	sp.mu.Unlock()
}

// stableBin returns a path to this binary that agents can keep calling. A
// `go run` build lives in a temp dir that vanishes on restart, so in that case
// copy the executable to ~/.swag/bin/swag and hand out that path instead.
func stableBin(exe, dir string) string {
	if !strings.Contains(exe, "go-build") {
		return exe
	}
	dst := filepath.Join(dir, "bin", "swag")
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return exe
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		return exe
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, 0o755); err != nil {
		return exe
	}
	if err := os.Rename(tmp, dst); err != nil {
		return exe
	}
	return dst
}

func (sp *Super) settings() Settings {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.st.Settings
}

// deckAllow is the Claude Code permission rule set for calling the swag
// CLI, by its absolute path and by name.
func deckAllow(bin string, extra ...string) []string {
	rules := []string{"Bash(swag:*)", "Bash(swag *)", "Read(~/.swag/uploads/**)"}
	// Claude matches the resolved path too; home is often a symlink.
	if up, err := filepath.EvalSymlinks(filepath.Join(swagDir(), "uploads")); err == nil {
		rules = append(rules, "Read(/"+up+"/**)")
	}
	if bin != "" && bin != "swag" {
		rules = append(rules, "Bash("+bin+":*)", "Bash("+bin+" *)")
	}
	return append(rules, extra...)
}

// routerArgsLocked: the extra launch flags plus the chosen model and effort.
func (sp *Super) routerArgsLocked() []string {
	st := sp.st.Settings
	args := append(append([]string{}, st.RouterArgs...), modelArgs(st.RouterKind, st.RouterModel, st.RouterEffort)...)
	return withBypass(args, st.RouterKind, st.AskPermissions)
}

// The model and effort the router was launched with, kept across hub restarts.
func (sp *Super) setRouterRun(run [2]string) {
	sp.routerRun = &run
	b, _ := json.Marshal(run)
	sp.db.Exec(`INSERT INTO kv(k,v) VALUES('router_run',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, string(b))
}

func (sp *Super) routerRunLocked() *[2]string {
	if sp.routerRun == nil {
		var raw string
		var run [2]string
		if sp.db.QueryRow(`SELECT v FROM kv WHERE k='router_run'`).Scan(&raw) == nil && json.Unmarshal([]byte(raw), &run) == nil {
			sp.routerRun = &run
		}
	}
	return sp.routerRun
}

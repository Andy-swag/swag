package swag

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

const (
	idleWorkerReapAfter      = 60 * time.Minute
	idleWorkerReapCheckEvery = time.Minute
)

func (sp *Super) workerHasSubscriptionLocked(name string) bool {
	for _, th := range sp.st.Threads {
		for _, sub := range th.Subscribers {
			if sub == name {
				return true
			}
		}
	}
	return false
}

func (sp *Super) workerHasQueuedTriggerLocked(name string) bool {
	for _, t := range sp.st.Triggers {
		if t.Status != "queued" {
			continue
		}
		for _, target := range t.Targets {
			if target == name {
				return true
			}
		}
	}
	return false
}

func (sp *Super) workerHasActiveTimerLocked(name string) bool {
	for _, tm := range sp.st.Timers {
		if tm.Enabled && tm.Target == name {
			return true
		}
	}
	return false
}

func (sp *Super) workerCanReapLocked(w *Worker, now time.Time, minIdle time.Duration) bool {
	if w == nil || w.Ended {
		return false
	}
	if w.Status != "done" && w.Status != "idle" {
		return false
	}
	if sp.workerHasSubscriptionLocked(w.Name) || sp.workerHasQueuedTriggerLocked(w.Name) || sp.workerHasActiveTimerLocked(w.Name) {
		return false
	}
	if minIdle <= 0 {
		// Still protect workers that just launched (brief may not be queued yet).
		if !w.CreatedAt.IsZero() && now.Sub(w.CreatedAt) < 90*time.Second {
			return false
		}
		return true
	}
	since := w.IdleSince
	if since.IsZero() {
		since = w.ReportAt
	}
	if since.IsZero() {
		since = w.CreatedAt
	}
	return !since.IsZero() && now.Sub(since) >= minIdle
}

func (sp *Super) reapIdleWorkers(ctx context.Context, now time.Time, minIdle time.Duration, reason string) []string {
	type target struct {
		name        string
		paneID      string
		workspaceID string
		hasSession  bool
	}
	sp.mu.Lock()
	var targets []target
	for _, w := range sp.st.Workers {
		if sp.workerCanReapLocked(w, now, minIdle) {
			targets = append(targets, target{
				name:        w.Name,
				paneID:      w.PaneID,
				workspaceID: w.WorkspaceID,
				hasSession:  w.Session != "",
			})
		}
	}
	sp.mu.Unlock()
	if len(targets) == 0 {
		return nil
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].name < targets[j].name })

	var reaped []string
	for _, tg := range targets {
		var discoveredSession string
		if !tg.hasSession && tg.paneID != "" && sp.s != nil {
			local, m := split(tg.paneID)
			if ps := sp.s.titlesFor(m, ctx)[local]; ps.Session != "" {
				discoveredSession = ps.Session
			}
		}
		// Re-check status before closing so a worker that just resumed working is never interrupted.
		sp.mu.Lock()
		wk := sp.st.Workers[tg.name]
		if !sp.workerCanReapLocked(wk, now, minIdle) {
			sp.mu.Unlock()
			continue
		}
		if discoveredSession != "" && wk.Session == "" {
			wk.Session = discoveredSession
		}
		wsID := wk.WorkspaceID
		sp.mu.Unlock()

		if wsID != "" && sp.s != nil && sp.s.herdr != nil {
			_ = sp.s.herdr.Call(ctx, "workspace.close", map[string]any{"workspace_id": wsID}, nil)
		}

		sp.mu.Lock()
		if wk = sp.st.Workers[tg.name]; wk != nil && !wk.Ended {
			wk.Ended, wk.Status, wk.PaneID = true, "gone", ""
			for _, th := range sp.st.Threads {
				th.Subscribers = remove(th.Subscribers, wk.Name)
			}
			msg := wk.Name + " 已归档"
			if reason != "" {
				msg += "（" + reason + "）"
			}
			sp.addEntryLocked("event", wk.Thread, msg, wk.Name, "info")
			sp.notifyOrphanOnReapLocked(wk, reason)
			sp.changed()
			reaped = append(reaped, wk.Name)
		}
		sp.mu.Unlock()
	}
	return reaped
}

func (sp *Super) maybeReapIdleWorkers(ctx context.Context, now time.Time) {
	sp.mu.Lock()
	if !sp.lastReap.IsZero() && now.Sub(sp.lastReap) < idleWorkerReapCheckEvery {
		sp.mu.Unlock()
		return
	}
	sp.lastReap = now
	sp.mu.Unlock()
	sp.reapIdleWorkers(ctx, now, idleWorkerReapAfter, "空闲超 60 分钟自动归档")
	sp.checkOrphanOpenTasks(now)
}

func (s *Server) reapManualIdlePanes(ctx context.Context) []string {
	if s.herdr == nil {
		return nil
	}
	agents, err := s.herdr.Agents(ctx)
	if err != nil {
		return nil
	}
	swagPanes := map[string]bool{}
	swagNames := map[string]bool{routerName: true, dreamName: true}
	if s.sup != nil {
		s.sup.mu.Lock()
		for _, wk := range s.sup.st.Workers {
			if wk != nil && !wk.Ended {
				swagNames[wk.Name] = true
				if wk.PaneID != "" {
					swagPanes[wk.PaneID] = true
				}
			}
		}
		s.sup.mu.Unlock()
	}
	closedWS := map[string]bool{}
	var closed []string
	for _, a := range agents {
		if a.LaunchPending || a.WorkspaceID == "" || closedWS[a.WorkspaceID] {
			continue
		}
		if a.AgentStatus != "idle" && a.AgentStatus != "done" {
			continue
		}
		name := deref(a.Name)
		if swagNames[name] || swagPanes[a.PaneID] {
			continue
		}
		if err := s.herdr.Call(ctx, "workspace.close", map[string]any{"workspace_id": a.WorkspaceID}, nil); err == nil {
			closedWS[a.WorkspaceID] = true
			label := name
			if label == "" {
				label = a.TerminalTitle
			}
			if label == "" {
				label = a.PaneID
			}
			closed = append(closed, label)
		}
	}
	return closed
}

func (s *Server) superWorkersReap(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IncludeManual bool `json:"include_manual"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	reaped := s.sup.reapIdleWorkers(r.Context(), time.Now(), 0, "手动一键归档")
	var manual []string
	if body.IncludeManual {
		manual = s.reapManualIdlePanes(r.Context())
		reaped = append(reaped, manual...)
	}
	if reaped == nil {
		reaped = []string{}
	}
	if manual == nil {
		manual = []string{}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "reaped": reaped, "manual_reaped": manual, "count": len(reaped)})
}

func (sp *Super) dupRecentUserSayLocked(text string, re *Quote) *Entry {
	wantRe := ""
	if re != nil {
		wantRe = re.Entry
	}
	now := time.Now()
	for i := len(sp.st.Entries) - 1; i >= 0; i-- {
		e := sp.st.Entries[i]
		if now.Sub(e.At) > 3*time.Second {
			break
		}
		if e.Role != "user" {
			continue
		}
		gotRe := ""
		if e.Re != nil {
			gotRe = e.Re.Entry
		}
		if e.Text == text && gotRe == wantRe {
			return e
		}
	}
	return nil
}

package swag

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

func todoRandID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "td-" + hex.EncodeToString(b[:])
}

type TodoItem struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Detail       string    `json:"detail,omitempty"`
	Source       string    `json:"source"` // feishu | manual | agent
	Status       string    `json:"status"` // open | dispatched | done
	Priority     string    `json:"priority,omitempty"`
	Link         string    `json:"link,omitempty"`
	Due          string    `json:"due,omitempty"`
	DispatchedAt time.Time `json:"dispatched_at,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func todoIsOpen(status string) bool {
	return status == "" || status == "open"
}

func todoStatusRank(status string) int {
	switch status {
	case "", "open":
		return 0
	case "dispatched":
		return 1
	default:
		return 2
	}
}

var todoMu sync.Mutex

func todosPath() string    { return filepath.Join(swagDir(), "todos.json") }
func inboxAckPath() string { return filepath.Join(swagDir(), "inbox-ack.json") }

func loadTodos() []TodoItem {
	todoMu.Lock()
	defer todoMu.Unlock()
	b, err := os.ReadFile(todosPath())
	if err != nil {
		return []TodoItem{}
	}
	var list []TodoItem
	if json.Unmarshal(b, &list) != nil || list == nil {
		return []TodoItem{}
	}
	return list
}

func saveTodos(list []TodoItem) error {
	todoMu.Lock()
	defer todoMu.Unlock()
	if err := os.MkdirAll(swagDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := todosPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, todosPath())
}

func loadInboxAck() map[string]bool {
	todoMu.Lock()
	defer todoMu.Unlock()
	out := map[string]bool{}
	b, err := os.ReadFile(inboxAckPath())
	if err != nil {
		return out
	}
	var ids []string
	if json.Unmarshal(b, &ids) == nil {
		for _, id := range ids {
			if id != "" {
				out[id] = true
			}
		}
	}
	return out
}

func saveInboxAck(m map[string]bool) error {
	todoMu.Lock()
	defer todoMu.Unlock()
	if err := os.MkdirAll(swagDir(), 0o700); err != nil {
		return err
	}
	ids := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			ids = append(ids, k)
		}
	}
	sort.Strings(ids)
	if len(ids) > 500 {
		ids = ids[len(ids)-500:]
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	tmp := inboxAckPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, inboxAckPath())
}

func isFeishuTrigger(t *Trigger) bool {
	if t == nil {
		return false
	}
	fromLow := strings.ToLower(t.From)
	low := strings.ToLower(t.Text)
	return strings.Contains(fromLow, "feishu") || strings.Contains(t.Text, "【工作日飞书只读 Todo") || strings.Contains(low, "feishu-todo")
}

func isBlockedTriggerText(low string) bool {
	return strings.Contains(low, "is blocked") || strings.Contains(low, "blocked:") || strings.Contains(low, "block:")
}

func isFailedTriggerText(low string) bool {
	return strings.Contains(low, "failed to start") || strings.Contains(low, "marked failed") || (strings.Contains(low, "worker ") && strings.Contains(low, "failed:"))
}

// inboxNeedReasonsLocked returns triggerID -> reason for inbox items that still
// need the human's attention. Both the sidebar/tab badge and Inbox "需要你"
// list use this single server-side projection so counts never drift.
func (sp *Super) inboxNeedReasonsLocked(now time.Time) map[string]string {
	acked := loadInboxAck()
	ts := sp.st.Triggers
	if len(ts) > 120 {
		ts = ts[len(ts)-120:]
	}
	activeWorkers := map[string]*Worker{}
	for _, wk := range sp.st.Workers {
		if !wk.Ended {
			activeWorkers[wk.Name] = wk
		}
	}
	out := map[string]string{}
	for _, t := range ts {
		if acked[t.ID] {
			continue
		}
		low := strings.ToLower(t.Text)
		w := activeWorkers[t.From]
		switch {
		case t.Status == "queued" && sp.st.Settings.Paused:
			out[t.ID] = "自动化已暂停，排队等待处理"
		case t.Action == "candidate":
			out[t.ID] = "外部事件，等你决定"
		case w != nil && w.Status == "blocked" && isBlockedTriggerText(low):
			out[t.ID] = "当前阻塞中，需去终端确认"
		case w != nil && isFailedTriggerText(low):
			out[t.ID] = "运行异常，需查看"
		}
	}
	return out
}

func (sp *Super) todoOpenCountLocked(now time.Time) int {
	n := len(sp.pendingLocked(now))
	for _, t := range loadTodos() {
		if todoIsOpen(t.Status) {
			n++
		}
	}
	if entries, err := os.ReadDir(filepath.Join(sp.memDir(), "tasks")); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				n++
			}
		}
	}
	return n
}

func (sp *Super) broadcastSubs() {
	if sp == nil {
		return
	}
	sp.mu.Lock()
	for ch := range sp.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	sp.mu.Unlock()
}

func (s *Server) superTodos(w http.ResponseWriter, r *http.Request) {
	sp := s.sup
	sp.PullRemoteRouterMemory(r.Context(), false)
	todos := loadTodos()
	sort.Slice(todos, func(i, j int) bool {
		ri, rj := todoStatusRank(todos[i].Status), todoStatusRank(todos[j].Status)
		if ri != rj {
			return ri < rj
		}
		return todos[i].UpdatedAt.After(todos[j].UpdatedAt)
	})
	type liveTaskWorker struct {
		Name    string `json:"name"`
		PaneID  string `json:"pane_id"`
		Status  string `json:"status"`
		Kind    string `json:"kind"`
		Machine string `json:"machine,omitempty"`
	}
	type liveTask struct {
		Name    string           `json:"name"`
		Title   string           `json:"title"`
		Summary string           `json:"summary"`
		Text    string           `json:"text,omitempty"`
		Mod     time.Time        `json:"mod"`
		Workers []liveTaskWorker `json:"workers,omitempty"`
	}
	sp.mu.Lock()
	rawPend := sp.pendingLocked(time.Now())
	entryText := map[string]string{}
	for _, e := range sp.st.Entries {
		entryText[e.ID] = e.Text
	}
	var activeW []liveTaskWorker
	workerTasks := map[string]string{}
	for _, wk := range sp.st.Workers {
		if wk != nil && !wk.Ended && wk.PaneID != "" {
			activeW = append(activeW, liveTaskWorker{Name: wk.Name, PaneID: wk.PaneID, Status: wk.Status, Kind: wk.Kind, Machine: wk.Machine})
			workerTasks[wk.Name] = strings.ToLower(wk.Task)
		}
	}
	sp.mu.Unlock()
	sort.Slice(activeW, func(i, j int) bool { return activeW[i].Name < activeW[j].Name })
	var tasks []liveTask
	for _, f := range sp.memFiles() {
		if !strings.HasPrefix(f.Name, "tasks/") || strings.HasPrefix(f.Name, "tasks/done/") {
			continue
		}
		slug := strings.TrimSuffix(strings.TrimPrefix(f.Name, "tasks/"), ".md")
		title := slug
		summary := ""
		for _, line := range strings.Split(f.Text, "\n") {
			t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#>-*• "))
			if t == "" {
				continue
			}
			if summary == "" && strings.HasPrefix(strings.TrimSpace(line), "#") {
				title = clip(t, 60)
				continue
			}
			summary = clip(t, 140)
			break
		}
		lowText := strings.ToLower(f.Text + "\n" + slug)
		lowSlug := strings.ToLower(slug)
		var matched []liveTaskWorker
		for _, aw := range activeW {
			ln := strings.ToLower(aw.Name)
			if strings.Contains(lowText, ln) || (len(lowSlug) >= 4 && strings.Contains(workerTasks[aw.Name], lowSlug)) {
				matched = append(matched, aw)
			}
		}
		tasks = append(tasks, liveTask{Name: f.Name, Title: title, Summary: summary, Text: clip(strings.TrimSpace(f.Text), 1500), Mod: f.Mod, Workers: matched})
	}
	type pendingView struct {
		ID      string    `json:"id"`
		At      time.Time `json:"at"`
		Summary string    `json:"summary"`
		Text    string    `json:"text"`
		Options []string  `json:"options,omitempty"`
		Check   bool      `json:"check,omitempty"`
	}
	pending := make([]pendingView, 0, len(rawPend))
	for _, p := range rawPend {
		full := entryText[p.ID]
		if full == "" {
			full = p.Summary
		}
		pending = append(pending, pendingView{
			ID: p.ID, At: p.At, Summary: p.Summary, Text: full, Options: p.Options, Check: p.Check,
		})
	}
	openTodos := 0
	for _, t := range todos {
		if todoIsOpen(t.Status) {
			openTodos++
		}
	}
	deliverables := sp.collectDeliverables()
	writeJSON(w, 200, map[string]any{
		"todos":        todos,
		"live_tasks":   tasks,
		"pending":      pending,
		"deliverables": deliverables,
		"open_count":   openTodos + len(tasks) + len(pending),
	})
}

func (s *Server) superTodoUpsert(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Detail   string `json:"detail"`
		Source   string `json:"source"`
		Status   string `json:"status"`
		Priority string `json:"priority"`
		Link     string `json:"link"`
		Due      string `json:"due"`
	}
	if !decode(w, r, &body) {
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		writeJSON(w, 400, map[string]any{"error": "待办标题不能为空"})
		return
	}
	src := strings.TrimSpace(body.Source)
	if src == "" {
		src = "manual"
	}
	wantStatus := strings.TrimSpace(body.Status)
	if wantStatus != "open" && wantStatus != "dispatched" && wantStatus != "done" {
		wantStatus = "open"
	}
	now := time.Now()
	list := loadTodos()
	for i := range list {
		if (body.ID != "" && list[i].ID == body.ID) || (body.ID == "" && list[i].Status != "done" && list[i].Source == src && list[i].Title == title) {
			list[i].Title = title
			if strings.TrimSpace(body.Detail) != "" {
				list[i].Detail = strings.TrimSpace(body.Detail)
			}
			list[i].Source = src
			if strings.TrimSpace(body.Status) != "" {
				list[i].Status = wantStatus
				if wantStatus == "dispatched" {
					list[i].DispatchedAt = now
				}
			}
			if strings.TrimSpace(body.Priority) != "" {
				list[i].Priority = strings.TrimSpace(body.Priority)
			}
			if strings.TrimSpace(body.Link) != "" {
				list[i].Link = strings.TrimSpace(body.Link)
			}
			if strings.TrimSpace(body.Due) != "" {
				list[i].Due = strings.TrimSpace(body.Due)
			}
			list[i].UpdatedAt = now
			_ = saveTodos(list)
			s.sup.broadcastSubs()
			writeJSON(w, 200, map[string]any{"ok": true, "todo": list[i]})
			return
		}
	}
	item := TodoItem{
		ID:        todoRandID(),
		Title:     title,
		Detail:    strings.TrimSpace(body.Detail),
		Source:    src,
		Status:    wantStatus,
		Priority:  strings.TrimSpace(body.Priority),
		Link:      strings.TrimSpace(body.Link),
		Due:       strings.TrimSpace(body.Due),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if wantStatus == "dispatched" {
		item.DispatchedAt = now
	}
	list = append([]TodoItem{item}, list...)
	if err := saveTodos(list); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	s.sup.broadcastSubs()
	writeJSON(w, 200, map[string]any{"ok": true, "todo": item})
}

func upsertTodoInternal(title, detail, source, priority, link, due string) {
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	if source == "" {
		source = "agent"
	}
	now := time.Now()
	list := loadTodos()
	for i := range list {
		if list[i].Status != "done" && list[i].Source == source && list[i].Title == title {
			if strings.TrimSpace(detail) != "" {
				list[i].Detail = strings.TrimSpace(detail)
			}
			if strings.TrimSpace(priority) != "" {
				list[i].Priority = strings.TrimSpace(priority)
			}
			list[i].UpdatedAt = now
			_ = saveTodos(list)
			return
		}
	}
	item := TodoItem{
		ID:        todoRandID(),
		Title:     title,
		Detail:    strings.TrimSpace(detail),
		Source:    source,
		Status:    "open",
		Priority:  strings.TrimSpace(priority),
		Link:      strings.TrimSpace(link),
		Due:       strings.TrimSpace(due),
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = saveTodos(append([]TodoItem{item}, list...))
}

func splitDreamProposalItem(raw string) (name, note string) {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "（"); i > 0 && strings.HasSuffix(raw, "）") {
		return strings.TrimSpace(raw[:i]), strings.TrimSpace(strings.TrimSuffix(raw[i+len("（"):], "）"))
	}
	if i := strings.Index(raw, "("); i > 0 && strings.HasSuffix(raw, ")") {
		return strings.TrimSpace(raw[:i]), strings.TrimSpace(strings.TrimSuffix(raw[i+1:], ")"))
	}
	return raw, ""
}

func addDreamProposalTodos(journalDate, skills, fixes, scripts string) int {
	added := 0
	emit := func(prefix, raw, fallbackDetail string) {
		if strings.TrimSpace(raw) == "" {
			return
		}
		for _, part := range strings.Split(raw, "、") {
			name, note := splitDreamProposalItem(part)
			if name == "" {
				continue
			}
			detail := fallbackDetail
			if note != "" {
				detail = note + "（来自 " + journalDate + " Dream 提案）"
			} else {
				detail = fallbackDetail + "（来自 " + journalDate + " Dream 提案）"
			}
			upsertTodoInternal(prefix+name, detail, "agent", "medium", "", "")
			added++
		}
	}
	emit("沉淀 Skill：", skills, "高频流程已成熟，可点「派给 Swag」指定保存位置并封装为 Skill")
	emit("改进 Skill：", fixes, "已有 Skill 在实战中存在需要修正的痛点")
	emit("固化脚本/流水线：", scripts, "高频重复流程建议固化为可复用脚本或流水线")
	return added
}

func (s *Server) superTodoAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Status string `json:"status"`
		Delete bool   `json:"delete"`
	}
	if !decode(w, r, &body) {
		return
	}
	list := loadTodos()
	var next []TodoItem
	found := false
	for _, t := range list {
		if t.ID != id {
			next = append(next, t)
			continue
		}
		found = true
		if body.Delete {
			continue
		}
		if body.Status == "done" || body.Status == "open" || body.Status == "dispatched" {
			t.Status = body.Status
		} else if t.Status == "done" {
			t.Status = "open"
		} else {
			t.Status = "done"
		}
		now := time.Now()
		if t.Status == "dispatched" {
			t.DispatchedAt = now
		}
		t.UpdatedAt = now
		next = append(next, t)
	}
	if !found {
		writeJSON(w, 404, map[string]any{"error": "未找到该待办"})
		return
	}
	if err := saveTodos(next); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	s.sup.broadcastSubs()
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) superInboxAck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if !decode(w, r, &body) {
		return
	}
	ack := loadInboxAck()
	for _, id := range body.IDs {
		if id = strings.TrimSpace(id); id != "" {
			ack[id] = true
		}
	}
	if err := saveInboxAck(ack); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	s.sup.broadcastSubs()
	writeJSON(w, 200, map[string]any{"ok": true, "acked": len(ack)})
}

func (s *Server) superTodoBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs    []string `json:"ids"`
		Action string   `json:"action"` // done | open | dispatched | delete
	}
	if !decode(w, r, &body) {
		return
	}
	want := map[string]bool{}
	for _, id := range body.IDs {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	if len(want) == 0 {
		writeJSON(w, 400, map[string]any{"error": "未选择待办"})
		return
	}
	if body.Action != "done" && body.Action != "open" && body.Action != "dispatched" && body.Action != "delete" {
		writeJSON(w, 400, map[string]any{"error": "不支持的批量操作"})
		return
	}
	list := loadTodos()
	next := make([]TodoItem, 0, len(list))
	now := time.Now()
	changed := 0
	for _, t := range list {
		if !want[t.ID] {
			next = append(next, t)
			continue
		}
		changed++
		if body.Action == "delete" {
			continue
		}
		t.Status = body.Action
		if body.Action == "dispatched" {
			t.DispatchedAt = now
		}
		t.UpdatedAt = now
		next = append(next, t)
	}
	if err := saveTodos(next); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	s.sup.broadcastSubs()
	writeJSON(w, 200, map[string]any{"ok": true, "changed": changed})
}

func (s *Server) superRecall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
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
	idx := -1
	var recalled *Entry
	for i, e := range sp.st.Entries {
		if e.ID == id {
			idx = i
			recalled = e
			break
		}
	}
	if idx < 0 {
		sp.mu.Unlock()
		writeJSON(w, 404, map[string]any{"error": "未找到该消息"})
		return
	}
	if recalled.Role != "user" {
		sp.mu.Unlock()
		writeJSON(w, 400, map[string]any{"error": "仅支持撤回自己发出的消息"})
		return
	}
	text := recalled.Text
	recent := time.Since(recalled.At) < 10*time.Minute
	sp.st.Entries = append(sp.st.Entries[:idx], sp.st.Entries[idx+1:]...)
	if pi := slices.Index(sp.st.Pinned, id); pi >= 0 {
		sp.st.Pinned = append(sp.st.Pinned[:pi], sp.st.Pinned[pi+1:]...)
	}
	delete(sp.pinnedCache, id)
	if sp.db != nil {
		_, _ = sp.db.Exec(`DELETE FROM entries WHERE id = ?`, id)
	}
	cancelledQueued := false
	alreadyDelivered := false
	for _, t := range sp.st.Triggers {
		if t.Source == "chat" && t.Ref == id {
			if t.Status == "queued" {
				t.Status = "ignored"
				t.Action = "recalled"
				cancelledQueued = true
			} else {
				alreadyDelivered = true
			}
		}
	}
	if alreadyDelivered && recent {
		sp.ingestLocked("system", mainThread, "recall", "The human recalled their previous message (\""+clip(text, 120)+"\"). Ignore it and do not act on it unless asked again.", "", "", id)
	}
	sp.changed()
	sp.mu.Unlock()
	writeJSON(w, 200, map[string]any{
		"ok":                true,
		"text":              text,
		"cancelled_queued":  cancelledQueued,
		"already_delivered": alreadyDelivered,
	})
}

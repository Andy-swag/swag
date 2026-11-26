package swag

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type DeliverableItem struct {
	Title        string    `json:"title"`
	URL          string    `json:"url"`
	DocType      string    `json:"doc_type"`
	Source       string    `json:"source"`
	Summary      string    `json:"summary,omitempty"`
	TaskFile     string    `json:"task_file,omitempty"`
	TaskTitle    string    `json:"task_title,omitempty"`
	RelatedTasks []string  `json:"related_tasks,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	Week         string    `json:"week,omitempty"`
	WeekLabel    string    `json:"week_label,omitempty"`
	Text         string    `json:"text,omitempty"`
	HasDoc       bool      `json:"has_doc"`
	UpdatedAt    time.Time `json:"updated_at"`
}

var (
	feishuDocLinkRe = regexp.MustCompile(`https?://[^\s)）>"'\]]*(?:larkoffice\.com|feishu\.cn)/(?:docx|wiki|docs|sheets|base|slides)/[A-Za-z0-9_-]+`)
	mdLinkDocRe     = regexp.MustCompile(`\[([^\]]{1,120})\]\((https?://[^\s)]*(?:larkoffice\.com|feishu\.cn)/(?:docx|wiki|docs|sheets|base|slides)/[A-Za-z0-9_-]+[^)]*)\)`)
	doneTitleTrimRe = regexp.MustCompile(`\s*[（(]\s*(?:进行中|已完成|已归档)\s*[)）]\s*$`)
	isoWeekKeyRe    = regexp.MustCompile(`\b(20\d{2})-W(0[1-9]|[1-4]\d|5[0-3])\b`)
	ymdDateRe       = regexp.MustCompile(`\b(20\d{2})-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])\b`)
)

func isoWeekStart(year, week int, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	jan4 := time.Date(year, time.January, 4, 0, 0, 0, 0, loc)
	wd := int(jan4.Weekday())
	if wd == 0 {
		wd = 7
	}
	w1Mon := jan4.AddDate(0, 0, -(wd - 1))
	return w1Mon.AddDate(0, 0, (week-1)*7)
}

func formatISOWeek(year, week int, loc *time.Location) (string, string) {
	mon := isoWeekStart(year, week, loc)
	sun := mon.AddDate(0, 0, 6)
	key := fmt.Sprintf("%04d-W%02d", year, week)
	label := fmt.Sprintf("W%02d (%02d/%02d–%02d/%02d)", week, mon.Month(), mon.Day(), sun.Month(), sun.Day())
	return key, label
}

func isoWeekFromTime(t time.Time) (string, string) {
	if t.IsZero() {
		t = time.Now()
	}
	lt := t.In(time.Local)
	y, w := lt.ISOWeek()
	return formatISOWeek(y, w, lt.Location())
}

func parseWeekFromText(text string, fallback time.Time) (key, label string, anchor time.Time, explicit bool) {
	if fallback.IsZero() {
		fallback = time.Now()
	}
	if m := isoWeekKeyRe.FindStringSubmatch(text); len(m) == 3 {
		y, _ := strconv.Atoi(m[1])
		w, _ := strconv.Atoi(m[2])
		if y >= 2020 && w >= 1 && w <= 53 {
			k, lbl := formatISOWeek(y, w, time.Local)
			sun := isoWeekStart(y, w, time.Local).AddDate(0, 0, 6).Add(18 * time.Hour)
			return k, lbl, sun, true
		}
	}
	var best time.Time
	for _, m := range ymdDateRe.FindAllStringSubmatch(text, -1) {
		if dt, err := time.ParseInLocation("2006-01-02", m[0], time.Local); err == nil {
			if dt.After(best) {
				best = dt
			}
		}
	}
	if !best.IsZero() {
		k, lbl := isoWeekFromTime(best)
		return k, lbl, best.Add(18 * time.Hour), true
	}
	k, lbl := isoWeekFromTime(fallback)
	return k, lbl, fallback, false
}

func feishuDocType(u string) string {
	low := strings.ToLower(u)
	switch {
	case strings.Contains(low, "/wiki/"):
		return "知识库"
	case strings.Contains(low, "/sheets/"):
		return "表格"
	case strings.Contains(low, "/base/"):
		return "多维表格"
	case strings.Contains(low, "/slides/"):
		return "幻灯片"
	default:
		return "飞书文档"
	}
}

func cleanDeliverableURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), ".,;，。；'\"`")
	if i := strings.IndexByte(u, '?'); i > 0 {
		u = u[:i]
	}
	if i := strings.IndexByte(u, '#'); i > 0 {
		u = u[:i]
	}
	return u
}

func isRoutineDoneTask(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	if base == "" || base == ".keep" || !strings.HasSuffix(base, ".md") {
		return true
	}
	for _, p := range []string{"feishu-todo-", "night-reclaim-", "heartbeat-", "cron-check-"} {
		if strings.HasPrefix(base, p) {
			return true
		}
	}
	return false
}

func isWeeklyRollupMarked(text string) bool {
	for _, raw := range strings.Split(text, "\n") {
		l := strings.TrimSpace(raw)
		for _, kw := range []string{"复盘归属：周复盘", "复盘归属:周复盘", "复盘归属：无需单独复盘", "复盘归属:无需单独复盘", "无需单独复盘"} {
			if strings.Contains(l, kw) {
				return true
			}
		}
	}
	return false
}

func weeklyMarkerLine(text string) string {
	for _, raw := range strings.Split(text, "\n") {
		if isWeeklyRollupMarked(raw) {
			return raw
		}
	}
	return ""
}

func isExplicitReviewLine(line, linkTitle string) bool {
	for _, neg := range []string{"目标：依照", "目标:依照", "目标：按", "参考文档", "原文档", "输入文档", "需求文档", "外部文档"} {
		if strings.Contains(line, neg) {
			return false
		}
	}
	for _, kw := range []string{"复盘文档", "交付文档", "总结文档", "飞书复盘", "周复盘", "周总复盘", "完结复盘", "复盘链接", "交付链接", "复盘报告"} {
		if strings.Contains(line, kw) {
			return true
		}
	}
	if linkTitle != "" {
		for _, kw := range []string{"复盘", "交付", "总结", "周报"} {
			if strings.Contains(linkTitle, kw) {
				return true
			}
		}
	}
	return false
}

func taskDoneTitle(name, text string) string {
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "#") {
			t := strings.TrimSpace(strings.TrimLeft(l, "# "))
			t = strings.TrimSpace(doneTitleTrimRe.ReplaceAllString(t, ""))
			if t != "" {
				return clip(t, 72)
			}
		}
	}
	base := strings.TrimSuffix(filepath.Base(name), ".md")
	return clip(base, 72)
}

func taskDoneSummary(text string) string {
	var bullets []string
	for _, raw := range strings.Split(text, "\n") {
		l := strings.TrimSpace(raw)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if (isExplicitReviewLine(l, "") && feishuDocLinkRe.MatchString(l)) || isWeeklyRollupMarked(l) {
			continue
		}
		l = strings.TrimSpace(strings.TrimLeft(l, "-*•> "))
		if l == "" {
			continue
		}
		bullets = append(bullets, l)
	}
	if len(bullets) == 0 {
		return ""
	}
	return clip(bullets[len(bullets)-1], 180)
}

func appendUniqueStr(xs []string, v string) []string {
	if v == "" {
		return xs
	}
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

func extractMarkedDeliverables(text, fallbackTitle, source string, at time.Time) []DeliverableItem {
	if text == "" {
		return nil
	}
	mdTitles := map[string]string{}
	for _, m := range mdLinkDocRe.FindAllStringSubmatch(text, -1) {
		u := cleanDeliverableURL(m[2])
		t := strings.TrimSpace(m[1])
		if t != "" && !strings.HasPrefix(strings.ToLower(t), "http") {
			mdTitles[u] = clip(t, 80)
		}
	}
	seen := map[string]bool{}
	var out []DeliverableItem
	for _, line := range strings.Split(text, "\n") {
		urls := feishuDocLinkRe.FindAllString(line, -1)
		if len(urls) == 0 {
			continue
		}
		cleanLine := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#>-*• "))
		cleanLine = feishuDocLinkRe.ReplaceAllString(cleanLine, "")
		cleanLine = strings.TrimSpace(strings.Trim(cleanLine, "：:()（）[]- "))
		for _, raw := range urls {
			u := cleanDeliverableURL(raw)
			if seen[u] {
				continue
			}
			linkTitle := mdTitles[u]
			if !isExplicitReviewLine(line, linkTitle) {
				continue
			}
			seen[u] = true
			title := linkTitle
			if title == "" {
				title = fallbackTitle
			}
			if title == "" {
				title = clip(cleanLine, 72)
			}
			if title == "" {
				title = feishuDocType(u)
			}
			dt := feishuDocType(u)
			if strings.Contains(line, "周总复盘") || strings.Contains(line, "周复盘") || strings.Contains(title, "周复盘") || strings.Contains(title, "周工作总复盘") {
				dt = "周总复盘"
			}
			wk, wl, anchor, explicit := parseWeekFromText(line, at)
			itemAt := at
			if explicit {
				modWk, _ := isoWeekFromTime(at)
				if wk != modWk {
					itemAt = anchor
				}
			} else {
				wk, wl = "", ""
			}
			out = append(out, DeliverableItem{
				Title:     title,
				URL:       u,
				DocType:   dt,
				Source:    source,
				Summary:   clip(cleanLine, 160),
				Scope:     "doc",
				Week:      wk,
				WeekLabel: wl,
				HasDoc:    true,
				UpdatedAt: itemAt,
			})
		}
	}
	return out
}

func (sp *Super) collectDeliverables() []DeliverableItem {
	var out []DeliverableItem
	byURLIdx := map[string]int{}

	for _, f := range sp.memFiles() {
		if strings.HasPrefix(f.Name, "notes/weekly") && strings.HasSuffix(f.Name, ".md") {
			for _, d := range extractMarkedDeliverables(f.Text, taskDoneTitle(f.Name, f.Text), f.Name, f.Mod) {
				if d.Week == "" {
					d.Week, d.WeekLabel = isoWeekFromTime(f.Mod)
				}
				d.DocType = "周总复盘"
				if idx, ok := byURLIdx[d.URL]; ok {
					if d.Week > out[idx].Week {
						out[idx].Week = d.Week
						out[idx].WeekLabel = d.WeekLabel
					}
					if d.UpdatedAt.After(out[idx].UpdatedAt) {
						out[idx].UpdatedAt = d.UpdatedAt
					}
					out[idx].DocType = "周总复盘"
					continue
				}
				byURLIdx[d.URL] = len(out)
				out = append(out, d)
			}
			continue
		}
		if !strings.HasPrefix(f.Name, "tasks/done/") || isRoutineDoneTask(f.Name) {
			continue
		}
		if strings.TrimSpace(f.Text) == "" {
			continue
		}
		tTitle := taskDoneTitle(f.Name, f.Text)
		tSummary := taskDoneSummary(f.Text)
		tText := clip(strings.TrimSpace(f.Text), 2400)
		slug := strings.TrimSuffix(filepath.Base(f.Name), ".md")
		wkSrc := weeklyMarkerLine(f.Text)
		if wkSrc == "" {
			wkSrc = f.Text
		}
		tWeek, tWeekLabel, _, _ := parseWeekFromText(wkSrc, f.Mod)
		docs := extractMarkedDeliverables(f.Text, tTitle, f.Name, f.Mod)
		if len(docs) == 0 {
			scope := "missing"
			docType := "待补复盘"
			if isWeeklyRollupMarked(f.Text) {
				scope = "weekly"
				docType = "已归入周复盘"
			}
			out = append(out, DeliverableItem{
				Title:        tTitle,
				DocType:      docType,
				Source:       f.Name,
				Summary:      tSummary,
				TaskFile:     f.Name,
				TaskTitle:    tTitle,
				RelatedTasks: []string{slug},
				Scope:        scope,
				Week:         tWeek,
				WeekLabel:    tWeekLabel,
				Text:         tText,
				HasDoc:       false,
				UpdatedAt:    f.Mod,
			})
			continue
		}
		for _, d := range docs {
			if d.Week == "" {
				d.Week = tWeek
				d.WeekLabel = tWeekLabel
			}
			if idx, ok := byURLIdx[d.URL]; ok {
				out[idx].RelatedTasks = appendUniqueStr(out[idx].RelatedTasks, slug)
				if d.Week > out[idx].Week {
					out[idx].Week = d.Week
					out[idx].WeekLabel = d.WeekLabel
				}
				if d.UpdatedAt.After(out[idx].UpdatedAt) {
					out[idx].UpdatedAt = d.UpdatedAt
				}
				if out[idx].TaskFile == "" || len(tText) > len(out[idx].Text) {
					out[idx].TaskFile = f.Name
					out[idx].TaskTitle = tTitle
					out[idx].Text = tText
					if tSummary != "" {
						out[idx].Summary = tSummary
					}
				}
				continue
			}
			d.TaskFile = f.Name
			d.TaskTitle = tTitle
			d.RelatedTasks = []string{slug}
			d.Text = tText
			if tSummary != "" {
				d.Summary = tSummary
			}
			byURLIdx[d.URL] = len(out)
			out = append(out, d)
		}
	}

	sp.mu.Lock()
	entries := make([]*Entry, 0, len(sp.st.Entries))
	start := max(len(sp.st.Entries)-200, 0)
	entries = append(entries, sp.st.Entries[start:]...)
	sp.mu.Unlock()
	for _, e := range entries {
		if e.Role != "swag" {
			continue
		}
		for _, d := range extractMarkedDeliverables(e.Text, "", "对话汇报", e.At) {
			if d.Week == "" {
				d.Week, d.WeekLabel = isoWeekFromTime(e.At)
			}
			if idx, ok := byURLIdx[d.URL]; ok {
				if d.UpdatedAt.After(out[idx].UpdatedAt) {
					out[idx].UpdatedAt = d.UpdatedAt
				}
				if (out[idx].Title == out[idx].DocType || len(out[idx].Title) < 6) && len(d.Title) > len(out[idx].Title) {
					out[idx].Title = d.Title
				}
				continue
			}
			byURLIdx[d.URL] = len(out)
			out = append(out, d)
		}
	}

	scopeRank := func(d DeliverableItem) int {
		if d.DocType == "周总复盘" {
			return 0
		}
		if d.HasDoc {
			return 1
		}
		if d.Scope == "missing" {
			return 2
		}
		return 3
	}
	sortItems := func(xs []DeliverableItem) {
		sort.Slice(xs, func(i, j int) bool {
			if xs[i].Week != xs[j].Week {
				return xs[i].Week > xs[j].Week
			}
			ri, rj := scopeRank(xs[i]), scopeRank(xs[j])
			if ri != rj {
				return ri < rj
			}
			if !xs[i].UpdatedAt.Equal(xs[j].UpdatedAt) {
				return xs[i].UpdatedAt.After(xs[j].UpdatedAt)
			}
			return xs[i].Title < xs[j].Title
		})
	}
	sortItems(out)
	const maxDeliverables = 120
	if len(out) > maxDeliverables {
		var docs, rest []DeliverableItem
		for _, it := range out {
			if it.HasDoc {
				docs = append(docs, it)
			} else {
				rest = append(rest, it)
			}
		}
		if len(docs) >= maxDeliverables {
			out = docs[:maxDeliverables]
		} else {
			out = append(docs, rest[:maxDeliverables-len(docs)]...)
			sortItems(out)
		}
	}
	return out
}

func (s *Server) superDeliverableScope(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TaskFile string `json:"task_file"`
		Scope    string `json:"scope"`
	}
	if !decode(w, r, &body) {
		return
	}
	rel := filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(body.TaskFile))))
	if !strings.HasPrefix(rel, "tasks/done/") || !strings.HasSuffix(rel, ".md") || strings.Contains(rel, "..") {
		writeJSON(w, 400, map[string]any{"error": "仅支持操作 tasks/done/*.md"})
		return
	}
	p := filepath.Join(s.sup.memDir(), filepath.FromSlash(rel))
	raw, err := os.ReadFile(p)
	if err != nil {
		s.sup.PullRemoteRouterMemory(r.Context(), true)
		raw, err = os.ReadFile(p)
		if err != nil {
			writeJSON(w, 404, map[string]any{"error": "找不到归档任务文件"})
			return
		}
	}
	mod := time.Now()
	if fi, err := os.Stat(p); err == nil {
		mod = fi.ModTime()
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if isWeeklyRollupMarked(line) {
			continue
		}
		kept = append(kept, line)
	}
	if body.Scope == "weekly" {
		wk, _, _, _ := parseWeekFromText(strings.Join(kept, "\n"), mod)
		kept = append(kept, "", fmt.Sprintf("- 复盘归属：周复盘（%s）", wk))
	}
	text := strings.TrimRight(strings.Join(kept, "\n"), "\n") + "\n"
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		fail(w, err)
		return
	}
	s.sup.PushRemoteRouterMemoryFile(r.Context(), rel, text)
	writeJSON(w, 200, map[string]any{"ok": true, "task_file": rel, "scope": body.Scope})
}

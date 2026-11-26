package swag

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type DeliverableItem struct {
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	DocType   string    `json:"doc_type"`
	Source    string    `json:"source"`
	Summary   string    `json:"summary,omitempty"`
	TaskFile  string    `json:"task_file,omitempty"`
	TaskTitle string    `json:"task_title,omitempty"`
	Text      string    `json:"text,omitempty"`
	HasDoc    bool      `json:"has_doc"`
	UpdatedAt time.Time `json:"updated_at"`
}

var (
	feishuDocLinkRe = regexp.MustCompile(`https?://[^\s)）>"'\]]*(?:larkoffice\.com|feishu\.cn)/(?:docx|wiki|docs|sheets|base|slides)/[A-Za-z0-9_-]+`)
	mdLinkDocRe     = regexp.MustCompile(`\[([^\]]{1,120})\]\((https?://[^\s)]*(?:larkoffice\.com|feishu\.cn)/(?:docx|wiki|docs|sheets|base|slides)/[A-Za-z0-9_-]+[^)]*)\)`)
	doneTitleTrimRe = regexp.MustCompile(`\s*[（(]\s*(?:进行中|已完成|已归档)\s*[)）]\s*$`)
)

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

func isExplicitReviewLine(line, linkTitle string) bool {
	for _, neg := range []string{"目标：依照", "目标:依照", "目标：按", "参考文档", "原文档", "输入文档", "需求文档", "外部文档"} {
		if strings.Contains(line, neg) {
			return false
		}
	}
	for _, kw := range []string{"复盘文档", "交付文档", "总结文档", "飞书复盘", "周复盘", "完结复盘", "复盘链接", "交付链接", "复盘报告"} {
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
		if isExplicitReviewLine(l, "") && feishuDocLinkRe.MatchString(l) {
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
	// Done task logs are append-only, so the latest closing bullet best summarizes the outcome.
	return clip(bullets[len(bullets)-1], 180)
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
			out = append(out, DeliverableItem{
				Title:     title,
				URL:       u,
				DocType:   feishuDocType(u),
				Source:    source,
				Summary:   clip(cleanLine, 160),
				HasDoc:    true,
				UpdatedAt: at,
			})
		}
	}
	return out
}

func (sp *Super) collectDeliverables() []DeliverableItem {
	var out []DeliverableItem
	byURLIdx := map[string]int{}

	for _, f := range sp.memFiles() {
		if !strings.HasPrefix(f.Name, "tasks/done/") || isRoutineDoneTask(f.Name) {
			continue
		}
		if strings.TrimSpace(f.Text) == "" {
			continue
		}
		tTitle := taskDoneTitle(f.Name, f.Text)
		tSummary := taskDoneSummary(f.Text)
		tText := clip(strings.TrimSpace(f.Text), 2400)
		docs := extractMarkedDeliverables(f.Text, tTitle, f.Name, f.Mod)
		if len(docs) == 0 {
			out = append(out, DeliverableItem{
				Title:     tTitle,
				DocType:   "已完结任务",
				Source:    f.Name,
				Summary:   tSummary,
				TaskFile:  f.Name,
				TaskTitle: tTitle,
				Text:      tText,
				HasDoc:    false,
				UpdatedAt: f.Mod,
			})
			continue
		}
		for _, d := range docs {
			d.TaskFile = f.Name
			d.TaskTitle = tTitle
			d.Text = tText
			if tSummary != "" {
				d.Summary = tSummary
			}
			if idx, ok := byURLIdx[d.URL]; ok {
				if d.UpdatedAt.After(out[idx].UpdatedAt) {
					out[idx].UpdatedAt = d.UpdatedAt
				}
				continue
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

	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		if out[i].HasDoc != out[j].HasDoc {
			return out[i].HasDoc
		}
		return out[i].Title < out[j].Title
	})
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

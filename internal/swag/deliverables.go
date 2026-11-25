package swag

import (
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
	UpdatedAt time.Time `json:"updated_at"`
}

var (
	feishuDocLinkRe = regexp.MustCompile(`https?://[^\s)）>"'\]]*(?:larkoffice\.com|feishu\.cn)/(?:docx|wiki|docs|sheets|base|slides)/[A-Za-z0-9_-]+`)
	mdLinkDocRe     = regexp.MustCompile(`\[([^\]]{1,120})\]\((https?://[^\s)]*(?:larkoffice\.com|feishu\.cn)/(?:docx|wiki|docs|sheets|base|slides)/[A-Za-z0-9_-]+[^)]*)\)`)
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

func extractDeliverablesFromText(text, fallbackTitle, source string, at time.Time) []DeliverableItem {
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
			seen[u] = true
			title := mdTitles[u]
			if title == "" {
				title = clip(cleanLine, 72)
			}
			if title == "" {
				title = fallbackTitle
			}
			if title == "" {
				title = feishuDocType(u)
			}
			out = append(out, DeliverableItem{
				Title:     title,
				URL:       u,
				DocType:   feishuDocType(u),
				Source:    source,
				Summary:   clip(cleanLine, 140),
				UpdatedAt: at,
			})
		}
	}
	return out
}

func (sp *Super) collectDeliverables() []DeliverableItem {
	byURL := map[string]DeliverableItem{}
	merge := func(items []DeliverableItem) {
		for _, it := range items {
			prev, ok := byURL[it.URL]
			if !ok {
				byURL[it.URL] = it
				continue
			}
			if it.UpdatedAt.After(prev.UpdatedAt) {
				prev.UpdatedAt = it.UpdatedAt
			}
			if (prev.Title == prev.DocType || len(prev.Title) < 6) && len(it.Title) > len(prev.Title) {
				prev.Title = it.Title
			}
			if prev.Summary == "" && it.Summary != "" {
				prev.Summary = it.Summary
			}
			byURL[it.URL] = prev
		}
	}

	for _, f := range sp.memFiles() {
		if !strings.HasPrefix(f.Name, "tasks/") && !strings.HasPrefix(f.Name, "notes/") {
			continue
		}
		fb := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(f.Name, "tasks/done/"), "tasks/"), ".md")
		for _, line := range strings.Split(f.Text, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				fb = clip(strings.TrimSpace(strings.TrimLeft(line, "# ")), 64)
				break
			}
		}
		merge(extractDeliverablesFromText(f.Text, fb, f.Name, f.Mod))
	}

	sp.mu.Lock()
	entries := make([]*Entry, 0, len(sp.st.Entries))
	start := max(len(sp.st.Entries)-200, 0)
	entries = append(entries, sp.st.Entries[start:]...)
	sp.mu.Unlock()
	for _, e := range entries {
		if e.Role != "swag" && e.Role != "event" {
			continue
		}
		merge(extractDeliverablesFromText(e.Text, "", "对话汇报", e.At))
	}

	out := make([]DeliverableItem, 0, len(byURL))
	for _, it := range byURL {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].URL < out[j].URL
	})
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

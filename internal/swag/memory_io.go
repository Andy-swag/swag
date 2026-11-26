package swag

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	memZipMaxBytes     = 50 << 20 // 50 MB uncompressed cap
	memZipMaxFileBytes = 2 << 20  // 2 MB per file cap
	memZipMaxFiles     = 2000
)

var allowedMemoryExts = map[string]bool{
	".md": true, ".txt": true, ".json": true, ".yaml": true, ".yml": true,
}

// MemoryImportResult summarizes a dry-run or applied memory ZIP import.
type MemoryImportResult struct {
	OK        bool     `json:"ok"`
	DryRun    bool     `json:"dry_run,omitempty"`
	Mode      string   `json:"mode"`
	Added     []string `json:"added"`
	Updated   []string `json:"updated"`
	Unchanged []string `json:"unchanged"`
	Removed   []string `json:"removed"`
	Skipped   []string `json:"skipped,omitempty"`
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".swag-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func isAllowedMemoryRel(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(strings.TrimPrefix(rel, "/")))
	if rel == "." || rel == "" || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") || filepath.IsAbs(rel) {
		return false
	}
	if rel == "messages.md" {
		return false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasPrefix(seg, ".") {
			return false
		}
	}
	return allowedMemoryExts[strings.ToLower(filepath.Ext(rel))]
}

func exportMemoryZip(memDir string, w io.Writer) (int, error) {
	var rels []string
	err := filepath.WalkDir(memDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(memDir, path)
		if rerr != nil || rel == "." {
			return nil
		}
		slash := filepath.ToSlash(rel)
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !isAllowedMemoryRel(slash) {
			return nil
		}
		fi, serr := d.Info()
		if serr != nil || fi.Size() > memZipMaxFileBytes {
			return nil
		}
		rels = append(rels, slash)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	sort.Strings(rels)

	zw := zip.NewWriter(w)
	count := 0
	for _, rel := range rels {
		full := filepath.Join(memDir, filepath.FromSlash(rel))
		data, rerr := os.ReadFile(full)
		if rerr != nil {
			continue
		}
		fi, _ := os.Stat(full)
		hdr := &zip.FileHeader{
			Name:   "memory/" + rel,
			Method: zip.Deflate,
		}
		if fi != nil {
			hdr.Modified = fi.ModTime()
		}
		fw, cerr := zw.CreateHeader(hdr)
		if cerr != nil {
			zw.Close()
			return count, cerr
		}
		if _, werr := fw.Write(data); werr != nil {
			zw.Close()
			return count, werr
		}
		count++
	}
	if err := zw.Close(); err != nil {
		return count, err
	}
	return count, nil
}

func importMemoryZip(memDir string, raw []byte, replace, dryRun bool) (*MemoryImportResult, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("invalid zip archive: %w", err)
	}
	if len(zr.File) > memZipMaxFiles {
		return nil, fmt.Errorf("zip contains too many entries (%d > %d)", len(zr.File), memZipMaxFiles)
	}
	mode := "merge"
	if replace {
		mode = "replace"
	}
	res := &MemoryImportResult{
		OK: true, DryRun: dryRun, Mode: mode,
		Added: []string{}, Updated: []string{}, Unchanged: []string{}, Removed: []string{},
	}

	incoming := map[string][]byte{}
	var totalBytes int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		name := filepath.ToSlash(strings.TrimPrefix(f.Name, "./"))
		name = strings.TrimPrefix(name, "memory/")
		if !isAllowedMemoryRel(name) {
			res.Skipped = append(res.Skipped, f.Name)
			continue
		}
		if f.UncompressedSize64 > uint64(memZipMaxFileBytes) {
			return nil, fmt.Errorf("file %s exceeds %d MB limit", name, memZipMaxFileBytes>>20)
		}
		rc, oerr := f.Open()
		if oerr != nil {
			return nil, fmt.Errorf("read %s: %w", name, oerr)
		}
		b, rerr := io.ReadAll(io.LimitReader(rc, memZipMaxFileBytes+1))
		rc.Close()
		if rerr != nil {
			return nil, fmt.Errorf("read %s: %w", name, rerr)
		}
		if len(b) > memZipMaxFileBytes {
			return nil, fmt.Errorf("file %s exceeds %d MB limit", name, memZipMaxFileBytes>>20)
		}
		totalBytes += int64(len(b))
		if totalBytes > memZipMaxBytes {
			return nil, fmt.Errorf("archive exceeds %d MB uncompressed limit", memZipMaxBytes>>20)
		}
		incoming[name] = b
	}

	existing := map[string][]byte{}
	_ = filepath.WalkDir(memDir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, rerr := filepath.Rel(memDir, path)
		if rerr != nil {
			return nil
		}
		slash := filepath.ToSlash(rel)
		if !isAllowedMemoryRel(slash) {
			return nil
		}
		if b, rerr := os.ReadFile(path); rerr == nil {
			existing[slash] = b
		}
		return nil
	})

	inKeys := make([]string, 0, len(incoming))
	for k := range incoming {
		inKeys = append(inKeys, k)
	}
	sort.Strings(inKeys)

	for _, rel := range inKeys {
		cur, ok := existing[rel]
		if !ok {
			res.Added = append(res.Added, rel)
		} else if bytes.Equal(cur, incoming[rel]) {
			res.Unchanged = append(res.Unchanged, rel)
		} else {
			res.Updated = append(res.Updated, rel)
		}
	}
	if replace {
		for rel := range existing {
			if _, ok := incoming[rel]; !ok {
				res.Removed = append(res.Removed, rel)
			}
		}
		sort.Strings(res.Removed)
	}

	if dryRun {
		return res, nil
	}
	if err := os.MkdirAll(memDir, 0o700); err != nil {
		return nil, err
	}
	for _, rel := range append(append([]string(nil), res.Added...), res.Updated...) {
		dst := filepath.Join(memDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return nil, err
		}
		if err := writeFileAtomic(dst, incoming[rel], 0o600); err != nil {
			return nil, err
		}
	}
	for _, rel := range res.Removed {
		_ = os.Remove(filepath.Join(memDir, filepath.FromSlash(rel)))
	}
	return res, nil
}

func (s *Server) extraSuperRoutes(mux *http.ServeMux) {
	api := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.auth(h)) }
	api("GET /api/super/memory/export", s.superMemoryExport)
	api("POST /api/super/memory/import", s.superMemoryImport)
	api("POST /api/super/read", s.superReadCursor)
}

func (s *Server) superMemoryExport(w http.ResponseWriter, r *http.Request) {
	sp := s.sup
	sp.PullRemoteRouterMemory(r.Context(), false)
	var buf bytes.Buffer
	if _, err := exportMemoryZip(sp.memDir(), &buf); err != nil {
		fail(w, err)
		return
	}
	fname := "swag-memory-" + time.Now().Format("20060102-150405") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+fname+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) superMemoryImport(w http.ResponseWriter, r *http.Request) {
	sp := s.sup
	q := r.URL.Query()
	replace := q.Get("mode") == "replace"
	dryRun := q.Get("dry_run") == "1" || q.Get("dry_run") == "true"

	var raw []byte
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(memZipMaxBytes); err != nil {
			writeJSON(w, 400, map[string]any{"error": "解析上传文件失败：" + err.Error()})
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": "缺少 file 字段"})
			return
		}
		defer f.Close()
		raw, err = io.ReadAll(io.LimitReader(f, memZipMaxBytes+1))
		if err != nil {
			fail(w, err)
			return
		}
	} else {
		var err error
		raw, err = io.ReadAll(io.LimitReader(r.Body, memZipMaxBytes+1))
		if err != nil {
			fail(w, err)
			return
		}
	}
	if len(raw) == 0 || int64(len(raw)) > memZipMaxBytes {
		writeJSON(w, 400, map[string]any{"error": "ZIP 文件为空或超过 50 MB 上限"})
		return
	}
	sp.PullRemoteRouterMemory(r.Context(), false)
	res, err := importMemoryZip(sp.memDir(), raw, replace, dryRun)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	if !dryRun {
		for _, rel := range append(append([]string(nil), res.Added...), res.Updated...) {
			if b, rerr := os.ReadFile(filepath.Join(sp.memDir(), filepath.FromSlash(rel))); rerr == nil {
				sp.PushRemoteRouterMemoryFile(r.Context(), rel, string(b))
			}
		}
		go sp.SyncMemoryMirrors(context.Background())
	}
	writeJSON(w, 200, res)
}

func (s *Server) superReadCursor(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Cursor string `json:"cursor"`
		Force  bool   `json:"force,omitempty"`
	}
	if !decode(w, r, &body) {
		return
	}
	cur := strings.TrimSpace(body.Cursor)
	if cur == "" {
		writeJSON(w, 400, map[string]any{"error": "cursor?"})
		return
	}
	sp := s.sup
	sp.mu.Lock()
	if body.Force || idNum(cur) > idNum(sp.st.ReadCursor) {
		sp.st.ReadCursor = cur
		sp.changed()
	}
	out := sp.st.ReadCursor
	sp.mu.Unlock()
	writeJSON(w, 200, map[string]any{"ok": true, "read_cursor": out})
}

func cliMemory(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: swag memory export [out.zip] | swag memory import <in.zip> [--replace] [--dry-run]")
	}
	base, tok, err := cliTarget()
	if err != nil {
		return err
	}
	switch args[0] {
	case "export":
		outPath := "swag-memory-" + time.Now().Format("20060102-150405") + ".zip"
		if len(args) > 1 {
			outPath = args[1]
		}
		req, _ := http.NewRequest("GET", base+"/api/super/memory/export", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			b, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("export failed: %s", strings.TrimSpace(string(b)))
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(outPath, b, 0o600); err != nil {
			return err
		}
		fmt.Printf("exported %s (%d bytes)\n", outPath, len(b))
		return nil
	case "import":
		if len(args) < 2 {
			return fmt.Errorf("usage: swag memory import <in.zip> [--replace] [--dry-run]")
		}
		inPath := ""
		mode := "merge"
		dryRun := false
		for _, a := range args[1:] {
			switch a {
			case "--replace":
				mode = "replace"
			case "--dry-run":
				dryRun = true
			default:
				if inPath == "" {
					inPath = a
				}
			}
		}
		if inPath == "" {
			return fmt.Errorf("missing input zip path")
		}
		raw, err := os.ReadFile(inPath)
		if err != nil {
			return err
		}
		u := fmt.Sprintf("%s/api/super/memory/import?mode=%s", base, mode)
		if dryRun {
			u += "&dry_run=1"
		}
		req, _ := http.NewRequest("POST", u, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/zip")
		resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			return fmt.Errorf("%s", strings.TrimSpace(string(out)))
		}
		var res MemoryImportResult
		if json.Unmarshal(out, &res) == nil {
			fmt.Printf("mode=%s dry_run=%v added=%d updated=%d unchanged=%d removed=%d\n",
				res.Mode, res.DryRun, len(res.Added), len(res.Updated), len(res.Unchanged), len(res.Removed))
		}
		return nil
	default:
		return fmt.Errorf("unknown memory subcommand %q (want export|import)", args[0])
	}
}

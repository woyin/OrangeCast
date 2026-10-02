// Package learningbundle renders bounded, deterministic learning snapshots.
// Callers must supply already authorized bodies; this package never fetches data.
package learningbundle

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxObjects bounds snapshot objects; generated index and manifest files are additional.
const MaxObjects = 500

// MaxBytes bounds aggregate uncompressed output, including index and manifest, to 100MiB.
const MaxBytes int64 = 100 << 20

// Snapshot is a caller-authorized, self-contained export scope. Rendering never
// reads a database or retrieves missing objects from external sources.
type Snapshot struct {
	ScopeKind string   `json:"scopeKind"`
	ScopeID   string   `json:"scopeId"`
	Objects   []Object `json:"objects"`
}

// Object preserves one exact revision of an authorized learning item. Body is
// Markdown whose local link destinations are validated during Render; callers
// remain responsible for removing secrets and unauthorized source content.
type Object struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision int    `json:"revision"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Status   string `json:"status"`
	Links    []Link `json:"links"`
}

// Link adds an explicit association or source citation. Path is relative to the
// archive root and is rendered relative to the containing Markdown object.
// Exactly one of Path and URL must be provided.
type Link struct {
	Label string `json:"label"`
	Path  string `json:"path,omitempty"`
	URL   string `json:"url,omitempty"`
}

// File records a generated file's archive path, uncompressed byte size, and
// hexadecimal SHA256 digest so readers can verify extracted content.
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest identifies the export scope and its deterministically sorted file
// inventory. Files excludes manifest.json, whose own hash would be circular. TotalBytes
// is the aggregate uncompressed archive size, including manifest.json.
type Manifest struct {
	Version    int    `json:"version"`
	ScopeKind  string `json:"scopeKind"`
	ScopeID    string `json:"scopeId"`
	Files      []File `json:"files"`
	TotalBytes int64  `json:"totalBytes"`
}

// ObjectPath returns a stable kind/id[-rN].md archive path. It rejects unknown
// kinds, negative revisions, and IDs containing separators or traversal syntax.
func ObjectPath(o Object) (string, error) {
	switch o.Kind {
	case "questions", "understandings", "notes", "articles", "sources", "cases", "keypoints":
	default:
		return "", fmt.Errorf("unsupported kind %q", o.Kind)
	}
	if o.ID == "" || !utf8.ValidString(o.ID) {
		return "", fmt.Errorf("invalid object ID")
	}
	for _, r := range o.ID {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return "", fmt.Errorf("invalid object ID %q", o.ID)
		}
	}
	if o.Revision < 0 {
		return "", fmt.Errorf("negative revision")
	}
	suffix := ""
	if o.Revision > 0 {
		suffix = fmt.Sprintf("-r%d", o.Revision)
	}
	return o.Kind + "/" + o.ID + suffix + ".md", nil
}

func inline(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	return strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "*", "\\*", "_", "\\_", "`", "\\`", "<", "&lt;", ">", "&gt;", "#", "\\#").Replace(s)
}
func validText(s string) bool { return utf8.ValidString(s) && !strings.ContainsRune(s, 0) }

// Render returns the complete uncompressed file set. It enforces limits before
// any archive bytes are written, so validation failures cannot produce a ZIP.
// Body Markdown links must resolve to bundled objects/index or safe external
// HTTP(S) URLs; code spans and fenced examples do not count as links.
func Render(s Snapshot) (map[string][]byte, Manifest, error) {
	m := Manifest{Version: 1, ScopeKind: s.ScopeKind, ScopeID: s.ScopeID, Files: []File{}}
	fail := func(err error) (map[string][]byte, Manifest, error) { return nil, Manifest{}, err }
	if len(s.Objects) > MaxObjects {
		return fail(fmt.Errorf("object limit exceeded: %d", len(s.Objects)))
	}
	if !validText(s.ScopeKind) || !validText(s.ScopeID) {
		return fail(fmt.Errorf("invalid scope text"))
	}
	objects := make(map[string]Object, len(s.Objects))
	inputBytes := int64(len(s.ScopeKind)) + int64(len(s.ScopeID))
	if inputBytes > MaxBytes {
		return fail(fmt.Errorf("byte limit exceeded"))
	}
	for _, o := range s.Objects {
		p, err := ObjectPath(o)
		if err != nil {
			return fail(err)
		}
		if _, ok := objects[p]; ok {
			return fail(fmt.Errorf("duplicate path %s", p))
		}
		for _, t := range []string{o.Kind, o.ID, o.Title, o.Body, o.Status} {
			if !validText(t) {
				return fail(fmt.Errorf("invalid UTF-8 or NUL in %s", p))
			}
			inputBytes += int64(len(t))
		}
		for _, l := range o.Links {
			for _, t := range []string{l.Label, l.Path, l.URL} {
				if !validText(t) {
					return fail(fmt.Errorf("invalid link text in %s", p))
				}
				inputBytes += int64(len(t))
			}
		}
		if inputBytes > MaxBytes {
			return fail(fmt.Errorf("byte limit exceeded"))
		}
		objects[p] = o
	}
	names := make([]string, 0, len(objects))
	for p := range objects {
		names = append(names, p)
	}
	sort.Strings(names)
	files := make(map[string][]byte, len(objects)+2)
	var total int64
	add := func(p string, b []byte) error {
		total += int64(len(b))
		if total > MaxBytes {
			return fmt.Errorf("byte limit exceeded")
		}
		files[p] = b
		return nil
	}
	var index strings.Builder
	fmt.Fprintf(&index, "# 学习成果包\n\n范围：%s / %s\n\n", inline(s.ScopeKind), inline(s.ScopeID))
	for _, p := range names {
		o := objects[p]
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n类别：%s  \n标识：%s  \n版本：%d  \n状态：%s\n\n", inline(o.Title), o.Kind, inline(o.ID), o.Revision, inline(o.Status))
		if o.Kind == "articles" && o.Status != "approved" && o.Status != "published" && o.Status != "passed" && o.Status != "history_passed" {
			b.WriteString("> 草稿：本文尚未通过审核，不应视为已批准的知识文章。\n\n")
		}
		if err := validateBodyLinks(p, o.Body, objects); err != nil {
			return fail(err)
		}
		if o.Kind == "articles" && o.Status == "history_passed" {
			b.WriteString("> 历史通过版本：本文为此前已通过审核的版本。\n\n")
		}
		b.WriteString(o.Body)
		b.WriteString("\n")
		if len(o.Links) > 0 {
			b.WriteString("\n## 关联与来源\n\n")
		}
		for _, l := range o.Links {
			if (l.Path == "") == (l.URL == "") {
				return fail(fmt.Errorf("link must have exactly one target in %s", p))
			}
			target := l.URL
			if l.Path != "" {
				if strings.Contains(l.Path, "\\") || strings.HasPrefix(l.Path, "/") || path.Clean(l.Path) != l.Path {
					return fail(fmt.Errorf("unsafe link path %q", l.Path))
				}
				if _, ok := objects[l.Path]; !ok && l.Path != "index.md" {
					return fail(fmt.Errorf("missing link target %q", l.Path))
				}
				relative, err := filepath.Rel(path.Dir(p), l.Path)
				if err != nil {
					return fail(err)
				}
				target = filepath.ToSlash(relative)
			} else {
				u, err := url.Parse(l.URL)
				if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
					return fail(fmt.Errorf("unsafe link URL"))
				}
			}
			// Angle delimiters and percent encoding prevent Markdown syntax injection.
			target = strings.NewReplacer(" ", "%20", "<", "%3C", ">", "%3E", "\n", "%0A", "\r", "%0D").Replace(target)
			fmt.Fprintf(&b, "- [%s](<%s>)\n", inline(l.Label), target)
		}
		if err := add(p, []byte(b.String())); err != nil {
			return fail(err)
		}
		fmt.Fprintf(&index, "- [%s](<%s>) — %s，版本 %d\n", inline(o.Title), p, inline(o.Status), o.Revision)
	}
	if err := add("index.md", []byte(index.String())); err != nil {
		return fail(err)
	}
	all := append(append([]string{}, names...), "index.md")
	sort.Strings(all)
	for _, p := range all {
		b := files[p]
		h := sha256.Sum256(b)
		m.Files = append(m.Files, File{p, hex.EncodeToString(h[:]), int64(len(b))})
	}
	// TotalBytes appears in the manifest; iterate until its decimal width settles.
	m.TotalBytes = total
	var manifest []byte
	for i := 0; i < 10; i++ {
		b, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return fail(err)
		}
		b = append(b, '\n')
		manifest = b
		n := total + int64(len(b))
		if n == m.TotalBytes {
			break
		}
		m.TotalBytes = n
	}
	if m.TotalBytes > MaxBytes {
		return fail(fmt.Errorf("byte limit exceeded"))
	}
	files["manifest.json"] = manifest
	return files, m, nil
}

// WriteZIP validates the entire snapshot before writing a deterministic ZIP
// with sorted paths, fixed timestamps, and private file modes. Validation errors
// write no bytes; an I/O failure may leave a partial archive in w.
func WriteZIP(w io.Writer, s Snapshot) (Manifest, error) {
	files, m, err := Render(s)
	if err != nil {
		return Manifest{}, err
	}
	names := make([]string, 0, len(files))
	for p := range files {
		names = append(names, p)
	}
	sort.Strings(names)
	zw := zip.NewWriter(w)
	for _, p := range names {
		h := &zip.FileHeader{Name: p, Method: zip.Deflate}
		h.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		h.SetMode(0600)
		f, e := zw.CreateHeader(h)
		if e == nil {
			_, e = f.Write(files[p])
		}
		if e != nil {
			_ = zw.Close()
			return Manifest{}, e
		}
	}
	if err := zw.Close(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

var markdownInlineLink = regexp.MustCompile(`!?\[[^\]\n]*\]\(\s*(?:<([^>\n]*)>|([^\s)]*)(?:\s+["'][^"'\n]*["'])?)\s*\)`)
var markdownReferenceLink = regexp.MustCompile(`^\s{0,3}\[[^\]\n]+\]:\s*(?:<([^>\n]+)>|(\S+))(?:\s+["'(].*)?$`)
var markdownCodeSpan = regexp.MustCompile("`+[^`]*`+")

// validateBodyLinks checks Markdown destinations, resolving local paths against
// the Markdown object's own directory. Fenced and inline code are not links.
func validateBodyLinks(current, body string, objects map[string]Object) error {
	if !strings.Contains(body, "[") {
		return nil
	}
	fence := ""
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		line = markdownCodeSpan.ReplaceAllString(line, "")
		var links [][]string
		for _, loc := range markdownInlineLink.FindAllStringSubmatchIndex(line, -1) {
			backslashes := 0
			for i := loc[0] - 1; i >= 0 && line[i] == '\\'; i-- {
				backslashes++
			}
			if backslashes%2 == 1 {
				continue
			}
			groups := make([]string, 3)
			for i := 0; i < 3; i++ {
				if loc[2*i] >= 0 {
					groups[i] = line[loc[2*i]:loc[2*i+1]]
				}
			}
			links = append(links, groups)
		}
		if ref := markdownReferenceLink.FindStringSubmatch(line); ref != nil {
			links = append(links, ref)
		}
		for _, match := range links {
			target := match[1]
			if target == "" {
				target = match[2]
			}
			if err := validateBodyTarget(current, target, objects); err != nil {
				return err
			}
		}
	}
	return nil
}
func validateBodyTarget(current, target string, objects map[string]Object) error {
	u, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("invalid Markdown link in %s", current)
	}
	if u.IsAbs() {
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return fmt.Errorf("unsafe Markdown URL in %s", current)
		}
		return nil
	}
	if u.Host != "" || u.RawQuery != "" || strings.Contains(u.Path, "\\") || strings.HasPrefix(u.Path, "/") {
		return fmt.Errorf("unsafe Markdown path in %s", current)
	}
	if u.Path == "" {
		return nil
	} // A same-file fragment is valid.
	resolved := path.Clean(path.Join(path.Dir(current), u.Path))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("Markdown path escapes archive in %s", current)
	}
	if _, ok := objects[resolved]; !ok && resolved != "index.md" {
		return fmt.Errorf("missing Markdown link target %q in %s", resolved, current)
	}
	return nil
}

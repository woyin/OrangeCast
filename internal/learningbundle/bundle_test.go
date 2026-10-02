package learningbundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func fixture() Snapshot {
	return Snapshot{ScopeKind: "question", ScopeID: "问题一", Objects: []Object{{Kind: "articles", ID: "article", Revision: 2, Title: "中文文章", Body: "学习正文。", Status: "draft", Links: []Link{{Label: "来源", Path: "notes/note-r1.md"}}}, {Kind: "notes", ID: "note", Revision: 1, Title: "个人笔记", Body: "自己的理解", Status: "active", Links: []Link{{Label: "返回", Path: "index.md"}, {Label: "原站", URL: "https://example.com/a?q=1"}}}}}
}
func TestZIPDeterministicReadableAndVerified(t *testing.T) {
	s := fixture()
	var a, b bytes.Buffer
	m, err := WriteZIP(&a, s)
	if err != nil {
		t.Fatal(err)
	}
	s.Objects[0], s.Objects[1] = s.Objects[1], s.Objects[0]
	if _, err = WriteZIP(&b, s); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("ZIP depends on input ordering")
	}
	zr, err := zip.NewReader(bytes.NewReader(a.Bytes()), int64(a.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	var size int64
	for _, f := range zr.File {
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		files[f.Name] = body
		size += int64(len(body))
		if f.Mode().Perm() != 0600 {
			t.Fatalf("mode %s", f.Mode())
		}
	}
	if size != m.TotalBytes {
		t.Fatalf("total %d != %d", size, m.TotalBytes)
	}
	var embedded Manifest
	if err = json.Unmarshal(files["manifest.json"], &embedded); err != nil {
		t.Fatal(err)
	}
	if embedded.TotalBytes != size {
		t.Fatal("manifest total")
	}
	for _, f := range embedded.Files {
		body, ok := files[f.Path]
		if !ok {
			t.Fatalf("missing %s", f.Path)
		}
		h := sha256.Sum256(body)
		if int64(len(body)) != f.Size || hex.EncodeToString(h[:]) != f.SHA256 {
			t.Fatal("bad hash or size")
		}
	}
	article := string(files["articles/article-r2.md"])
	for _, want := range []string{"中文文章", "学习正文。", "草稿：", "../notes/note-r1.md"} {
		if !strings.Contains(article, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if !strings.Contains(string(files["notes/note-r1.md"]), "../index.md") {
		t.Fatal("index relative link")
	}
}
func TestRenderRejectsUnsafeOrMissingTargets(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Snapshot)
	}{
		{"id traversal", func(s *Snapshot) { s.Objects[0].ID = "../private" }},
		{"slash", func(s *Snapshot) { s.Objects[0].ID = "a/b" }},
		{"backslash", func(s *Snapshot) { s.Objects[0].ID = `a\b` }},
		{"duplicate", func(s *Snapshot) { s.Objects = append(s.Objects, s.Objects[0]) }},
		{"missing", func(s *Snapshot) { s.Objects[0].Links[0].Path = "notes/missing.md" }},
		{"unclean", func(s *Snapshot) { s.Objects[0].Links[0].Path = "notes/../notes/note-r1.md" }},
		{"absolute", func(s *Snapshot) { s.Objects[0].Links[0].Path = "/notes/note-r1.md" }},
		{"javascript", func(s *Snapshot) { s.Objects[0].Links[0] = Link{URL: "javascript:alert(1)"} }},
		{"credentials", func(s *Snapshot) { s.Objects[0].Links[0] = Link{URL: "https://secret@example.com"} }},
		{"both", func(s *Snapshot) { s.Objects[0].Links[0].URL = "https://example.com" }},
		{"utf8", func(s *Snapshot) { s.Objects[0].Body = string([]byte{0xff}) }},
		{"nul", func(s *Snapshot) { s.Objects[0].Body = "a\x00b" }},
		{"negative revision", func(s *Snapshot) { s.Objects[0].Revision = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fixture()
			tt.change(&s)
			var b bytes.Buffer
			if _, err := WriteZIP(&b, s); err == nil {
				t.Fatal("accepted invalid snapshot")
			}
			if b.Len() != 0 {
				t.Fatal("wrote bytes before validation")
			}
		})
	}
}
func TestRenderBounds(t *testing.T) {
	s := fixture()
	s.Objects = make([]Object, MaxObjects+1)
	if _, _, err := Render(s); err == nil {
		t.Fatal("accepted object overflow")
	}
	s = fixture()
	s.Objects[0].Body = strings.Repeat("x", int(MaxBytes)+1)
	if _, _, err := Render(s); err == nil {
		t.Fatal("accepted byte overflow")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("disk unavailable") }
func TestWriteZIPPropagatesWriterFailure(t *testing.T) {
	if _, err := WriteZIP(brokenWriter{}, fixture()); err == nil {
		t.Fatal("lost write error")
	}
}
func TestSafeUnicodeIdentityAndEscapedTitle(t *testing.T) {
	s := Snapshot{Objects: []Object{{Kind: "questions", ID: "问题甲", Title: "标题 [link](evil)\n# forged", Body: "正文", Status: "active"}}}
	files, _, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files["questions/问题甲.md"]), `\[link\]`) {
		t.Fatal("title syntax injection")
	}
}

func TestBoundarySizeIncludesRenderedMetadata(t *testing.T) {
	s := fixture()
	// Raw body fits, but Markdown headers/index/manifest would overflow.
	s.Objects[0].Body = strings.Repeat("x", int(MaxBytes)-100)
	if _, _, err := Render(s); err == nil {
		t.Fatal("rendered overhead escaped byte limit")
	}
	s = Snapshot{ScopeID: strings.Repeat("x", int(MaxBytes)+1)}
	if _, _, err := Render(s); err == nil {
		t.Fatal("scope escaped byte limit")
	}
}
func TestInvalidKindsScopesAndLinks(t *testing.T) {
	for _, o := range []Object{{Kind: "private", ID: "id"}, {Kind: "notes", ID: ""}, {Kind: "notes", ID: string([]byte{0xff})}} {
		if _, err := ObjectPath(o); err == nil {
			t.Fatal("invalid object")
		}
	}
	if _, _, err := Render(Snapshot{ScopeKind: "\x00"}); err == nil {
		t.Fatal("invalid scope")
	}
	for _, l := range []Link{{}, {Path: `notes\x.md`}, {URL: "https:///path"}, {URL: "https://example.com/%invalid"}, {URL: "https://example.com", Label: "\x00"}} {
		s := fixture()
		s.Objects[0].Links = []Link{l}
		if _, _, err := Render(s); err == nil {
			t.Fatalf("invalid link %#v", l)
		}
	}
	s := fixture()
	s.Objects[0].Status = "approved"
	files, _, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(files["articles/article-r2.md"]), "> 草稿") {
		t.Fatal("approved article labeled draft")
	}
}

func TestRenderedExpansionAndManifestBounds(t *testing.T) {
	for _, scope := range []bool{false, true} {
		s := Snapshot{}
		expanded := strings.Repeat("[", int(MaxBytes)/2)
		if scope {
			s.ScopeID = expanded
		} else {
			s.Objects = []Object{{Kind: "notes", ID: "n", Title: expanded}}
		}
		if _, _, err := Render(s); err == nil {
			t.Fatal("escaped Markdown exceeded byte limit")
		}
	}
	s := Snapshot{Objects: []Object{{Kind: "notes", ID: "n", Body: ""}}}
	_, m, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	s.Objects[0].Body = strings.Repeat("x", int(MaxBytes-m.TotalBytes+1))
	if _, _, err := Render(s); err == nil {
		t.Fatal("manifest bytes escaped limit")
	}
}

func TestBodyLinksResolveInsideArchive(t *testing.T) {
	s := fixture()
	s.Objects[0].Status = "passed"
	s.Objects[0].Body = "中文正文不是链接。\n[笔记](../notes/note-r1.md#position-10)\n[站点](https://example.com/a)\n[本页](#section)\n[索引](../index.md)\n[参考][n]\n[n]: ../notes/note-r1.md \"中文说明\"\n`[代码](../../private)`\n```md\n[代码](../../private)\n```"
	files, _, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(files["articles/article-r2.md"]), "> 草稿") {
		t.Fatal("passed labeled draft")
	}
	s.Objects[0].Status = "history_passed"
	files, _, err = Render(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files["articles/article-r2.md"]), "历史通过版本") {
		t.Fatal("history not labeled")
	}
}
func TestBodyRejectsBadMarkdownLinks(t *testing.T) {
	for _, target := range []string{"../../private", "../notes/missing.md", "/app/question/1", "javascript:evil", "file:///private", "//example.com/private", "../notes/%2e%2e/%2e%2e/private", `..%5cprivate`, "https://secret@example.com", "../notes/note-r1.md?q=secret", "https://example.com/%invalid"} {
		t.Run(target, func(t *testing.T) {
			s := fixture()
			s.Objects[0].Body = "[链接](<" + target + ">)"
			if _, _, err := Render(s); err == nil {
				t.Fatal("accepted unsafe/missing Markdown target")
			}
		})
	}
	s := fixture()
	s.Objects[0].Body = "[ref]: ../../private"
	if _, _, err := Render(s); err == nil {
		t.Fatal("accepted unsafe reference link")
	}
}

func TestLiteralEscapedMarkdownIsNotLink(t *testing.T) {
	s := fixture()
	s.Objects[0].Body = `普通中文，\[示例](不是文件)。`
	if _, _, err := Render(s); err != nil {
		t.Fatal(err)
	}
}

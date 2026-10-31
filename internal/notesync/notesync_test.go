package notesync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/provider"
)

func sampleCard() (*provider.KnowledgeCard, []provider.Segment) {
	segs := []provider.Segment{{ID: "s1", Start: 12.4, End: 20, Text: "hello"}, {ID: "s2", Start: 30, End: 40, Text: "world"}}
	return &provider.KnowledgeCard{
		Quotes:    []provider.Quote{{Text: " verbatim line ", Citations: []string{"s2"}}, {Text: "  ", Citations: nil}},
		KeyPoints: []provider.KeyPoint{{Content: "Point", Description: "detail", Citations: []string{"s1"}}, {Content: "No cite"}},
	}, segs
}

func TestHighlights(t *testing.T) {
	card, segs := sampleCard()
	hs := Highlights(card, segs, "Ep", "Show", "https://oc.example/", "episode", "e1")
	if len(hs) != 3 {
		t.Fatalf("want 3 (blank quote skipped), got %d: %+v", len(hs), hs)
	}
	q := hs[0]
	if q.Text != "verbatim line" || q.Note != "" || q.Location != 30 || q.LocationType != "time_offset" ||
		q.SourceURL != "https://oc.example/sources/episode/e1?t=30.0#seg-s2" || q.Category != "podcasts" || q.Title != "Ep" || q.Author != "Show" {
		t.Errorf("quote: %+v", q)
	}
	k := hs[1]
	if k.Text != "Point：detail" || !strings.Contains(k.Note, "AI") || k.Location != 12 {
		t.Errorf("keypoint: %+v", k)
	}
	n := hs[2]
	if n.LocationType != "" || n.Location != 0 || n.SourceURL != "https://oc.example/sources/episode/e1" {
		t.Errorf("unresolvable citation should keep item without position: %+v", n)
	}
	long := Highlights(&provider.KnowledgeCard{Quotes: []provider.Quote{{Text: strings.Repeat("字", 9000)}}}, nil, "t", "", "http://x", "episode", "e")
	if got := len([]rune(long[0].Text)); got != maxTextRunes {
		t.Errorf("truncate to %d, got %d", maxTextRunes, got)
	}
}

func TestReadwisePush(t *testing.T) {
	var auths []string
	var sizes []int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		var body struct{ Highlights []Highlight }
		_ = json.NewDecoder(r.Body).Decode(&body)
		sizes = append(sizes, len(body.Highlights))
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	hs := make([]Highlight, 250)
	for i := range hs {
		hs[i] = Highlight{Text: "t", Title: "T", SourceType: "orangecast", Category: "podcasts"}
	}
	n, err := (&Readwise{Token: "tok", Endpoint: ts.URL}).Push(context.Background(), hs)
	if err != nil || n != 250 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(sizes) != 3 || sizes[0] != 100 || sizes[2] != 50 || auths[0] != "Token tok" {
		t.Errorf("batches %v auths %v", sizes, auths)
	}
	if n, err := (&Readwise{Token: "tok", Endpoint: ts.URL}).Push(context.Background(), nil); err != nil || n != 0 {
		t.Errorf("empty push: %d %v", n, err)
	}
}

func TestReadwisePushErrors(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Invalid token", http.StatusUnauthorized)
	}))
	defer bad.Close()
	hs := []Highlight{{Text: "t"}}
	if _, err := (&Readwise{Token: "", Endpoint: bad.URL}).Push(context.Background(), hs); err == nil || !strings.Contains(err.Error(), "READWISE_TOKEN") {
		t.Errorf("missing token: %v", err)
	}
	if n, err := (&Readwise{Token: "x", Endpoint: bad.URL}).Push(context.Background(), hs); err == nil || !strings.Contains(err.Error(), "401") || n != 0 {
		t.Errorf("401: n=%d err=%v", n, err)
	}
	bad.Close()
	if _, err := (&Readwise{Token: "x", Endpoint: bad.URL}).Push(context.Background(), hs); err == nil {
		t.Error("unreachable should fail")
	}
}

func TestWriteObsidian(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault", "OrangeCast")
	p, err := WriteObsidian(dir, "../../etc/passwd: a/b?", "# md")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != dir || strings.Contains(filepath.Base(p), "/") {
		t.Errorf("must stay inside dir: %s", p)
	}
	if b, _ := os.ReadFile(p); string(b) != "# md" {
		t.Errorf("content %q", b)
	}
	if p2, _ := WriteObsidian(dir, "../../etc/passwd: a/b?", "# v2"); p2 != p {
		t.Errorf("same title should overwrite: %s vs %s", p2, p)
	}
	if b, _ := os.ReadFile(p); string(b) != "# v2" {
		t.Errorf("overwrite %q", b)
	}
	if _, err := WriteObsidian("  ", "t", "x"); err == nil {
		t.Error("empty dir must fail")
	}
	file := filepath.Join(t.TempDir(), "f")
	_ = os.WriteFile(file, nil, 0o644)
	if _, err := WriteObsidian(filepath.Join(file, "sub"), "t", "x"); err == nil {
		t.Error("mkdir under file must fail")
	}
	ro := t.TempDir()
	_ = os.Chmod(ro, 0o555)
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	if os.Geteuid() != 0 {
		if _, err := WriteObsidian(ro, "t", "x"); err == nil {
			t.Error("read-only dir must fail")
		}
	}
}

func TestSafeName(t *testing.T) {
	cases := map[string]string{"": "untitled", "...": "untitled", "a\x00b\n": "a-b", "A|B#C": "A-B-C", strings.Repeat("长", 200): strings.Repeat("长", 120)}
	for in, want := range cases {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q)=%q want %q", in, got, want)
		}
	}
}

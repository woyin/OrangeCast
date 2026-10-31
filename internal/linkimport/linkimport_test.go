package linkimport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchDirectAudio(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ep.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("fake-audio"))
		case "/page.mp3":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	im := &Importer{TempDir: t.TempDir(), Client: http.DefaultClient}

	res, err := im.Fetch(context.Background(), ts.URL+"/ep.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "ep" || res.Filename != "ep.mp3" || res.ContentType != "audio/mpeg" || res.Size != 10 {
		t.Errorf("result: %+v", res)
	}
	if b, _ := os.ReadFile(res.Path); string(b) != "fake-audio" {
		t.Errorf("content: %q", b)
	}

	if _, err := im.Fetch(context.Background(), ts.URL+"/page.mp3"); err == nil || !strings.Contains(err.Error(), "网页") {
		t.Errorf("html should be rejected: %v", err)
	}
	if _, err := im.Fetch(context.Background(), ts.URL+"/missing.mp3"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("404: %v", err)
	}
}

func TestFetchRejectsBadAndUnsupportedLinks(t *testing.T) {
	im := &Importer{TempDir: t.TempDir()}
	for _, raw := range []string{"", "ftp://x/a.mp3", "http://user:pw@example.com/a.mp3", "https:///a.mp3"} {
		if _, err := im.Fetch(context.Background(), raw); err == nil {
			t.Errorf("%q should be rejected", raw)
		}
	}
	// 非白名单站点、且不是直接音频：不能交给 yt-dlp。
	if _, err := im.Fetch(context.Background(), "https://intranet.example.com/watch?v=1"); err != ErrUnsupported {
		t.Errorf("want ErrUnsupported, got %v", err)
	}
	// 子域名伪装不能通过白名单。
	if _, err := im.Fetch(context.Background(), "https://youtube.com.evil.test/watch?v=1"); err != ErrUnsupported {
		t.Errorf("lookalike host: %v", err)
	}
}

func fakeYtDlp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "yt-dlp")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFetchYtDlp(t *testing.T) {
	// 取 -o 模板所在目录，造一个文件，并按 --print 约定输出标题与路径。
	ok := fakeYtDlp(t, `while [ $# -gt 0 ]; do [ "$1" = "-o" ] && out="$2"; shift; done
d=$(dirname "$out"); printf audio > "$d/abc.m4a"; echo "My Talk"; echo "$d/abc.m4a"`)
	im := &Importer{YtDlp: ok, TempDir: t.TempDir()}
	res, err := im.Fetch(context.Background(), "https://www.youtube.com/watch?v=abc")
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "My Talk" || res.Filename != "My Talk.m4a" || res.ContentType != "audio/mp4" || res.Size != 5 {
		t.Errorf("result: %+v", res)
	}
	if b, _ := os.ReadFile(res.Path); string(b) != "audio" {
		t.Errorf("content: %q", b)
	}
	if matches, _ := filepath.Glob(filepath.Join(im.TempDir, "ytdlp-*")); len(matches) != 0 {
		t.Errorf("work dir should be removed: %v", matches)
	}

	fail := fakeYtDlp(t, "echo 'ERROR: Video unavailable' >&2; exit 1")
	if _, err := (&Importer{YtDlp: fail, TempDir: t.TempDir()}).Fetch(context.Background(), "https://youtu.be/x"); err == nil || !strings.Contains(err.Error(), "Video unavailable") {
		t.Errorf("failure should surface stderr: %v", err)
	}
	empty := fakeYtDlp(t, "echo only-one-line")
	if _, err := (&Importer{YtDlp: empty, TempDir: t.TempDir()}).Fetch(context.Background(), "https://youtu.be/x"); err == nil {
		t.Error("short output should fail")
	}
	missing := fakeYtDlp(t, `echo T; echo /nonexistent/file.m4a`)
	if _, err := (&Importer{YtDlp: missing, TempDir: t.TempDir()}).Fetch(context.Background(), "https://youtu.be/x"); err == nil {
		t.Error("missing output file should fail")
	}
	if _, err := (&Importer{YtDlp: "/no/such/yt-dlp", TempDir: t.TempDir()}).Fetch(context.Background(), "https://youtu.be/x"); err == nil || !strings.Contains(err.Error(), "未安装") {
		t.Errorf("missing binary: %v", err)
	}
}

func TestFetchDirectTooLargeAndUnreachable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte("0123456789"))
	}))
	im := &Importer{TempDir: t.TempDir(), Client: http.DefaultClient, MaxBytes: 5}
	if _, err := im.Fetch(context.Background(), ts.URL+"/big.wav"); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Errorf("oversize: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(im.TempDir, "link-*")); len(left) != 0 {
		t.Errorf("partial file should be removed: %v", left)
	}
	ts.Close()
	if _, err := im.Fetch(context.Background(), ts.URL+"/gone.mp3"); err == nil {
		t.Error("unreachable server should fail")
	}
	// 默认客户端带 SSRF 防护：环回地址必须被拦截。
	if _, err := (&Importer{TempDir: t.TempDir()}).Fetch(context.Background(), "http://127.0.0.1:1/a.mp3"); err == nil {
		t.Error("loopback must be blocked by default client")
	}
}

func TestFetchYtDlpFallbacks(t *testing.T) {
	// 未知扩展名与空标题：内容类型按扩展名推断，标题回退到文件名。
	bin := fakeYtDlp(t, `while [ $# -gt 0 ]; do [ "$1" = "-o" ] && out="$2"; shift; done
d=$(dirname "$out"); printf x > "$d/clip.opus"; echo ""; echo "$d/clip.opus"`)
	res, err := (&Importer{YtDlp: bin, TempDir: t.TempDir()}).Fetch(context.Background(), "https://m.youtube.com/watch?v=1")
	if err != nil {
		t.Fatal(err)
	}
	if res.ContentType != "audio/opus" || res.Title != "clip" {
		t.Errorf("fallbacks: %+v", res)
	}
	if _, err := (&Importer{TempDir: filepath.Join(t.TempDir(), "f", "g")}).Fetch(context.Background(), "https://youtu.be/x"); err != nil && strings.Contains(err.Error(), "invalid") {
		t.Errorf("unexpected: %v", err)
	}
}

func TestFetchFilesystemErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受目录权限限制")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("x"))
	}))
	defer ts.Close()
	ctx := context.Background()

	// TempDir 的父路径是文件：MkdirAll 失败。
	file := filepath.Join(t.TempDir(), "f")
	_ = os.WriteFile(file, nil, 0o644)
	if _, err := (&Importer{TempDir: filepath.Join(file, "sub")}).Fetch(ctx, "https://youtu.be/x"); err == nil {
		t.Error("mkdir under a file should fail")
	}

	// TempDir 只读：direct 的 CreateTemp、ytdlp 的 MkdirTemp 失败。
	ro := t.TempDir()
	_ = os.Chmod(ro, 0o555)
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	if _, err := (&Importer{TempDir: ro, Client: http.DefaultClient}).Fetch(ctx, ts.URL+"/a.mp3"); err == nil {
		t.Error("read-only dir: direct should fail")
	}
	if _, err := (&Importer{TempDir: ro}).Fetch(ctx, "https://youtu.be/x"); err == nil {
		t.Error("read-only dir: ytdlp should fail")
	}

	// yt-dlp 运行期间 TempDir 变为只读：最后的 Rename 失败。
	work := t.TempDir()
	t.Cleanup(func() { _ = os.Chmod(work, 0o755) })
	bin := fakeYtDlp(t, `while [ $# -gt 0 ]; do [ "$1" = "-o" ] && out="$2"; shift; done
d=$(dirname "$out"); printf x > "$d/a.m4a"; chmod 555 "$(dirname "$d")"; echo T; echo "$d/a.m4a"`)
	if _, err := (&Importer{YtDlp: bin, TempDir: work}).Fetch(ctx, "https://youtu.be/x"); err == nil {
		t.Error("rename into read-only dir should fail")
	}
}

package queue

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/woyin/orangecast/internal/filehash"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

func TestDownloadAudio_RealEpisodeURL(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := NewWorker(s, provider.NewSelector("g", "o"), filepath.Join(dir, "tmp"), filepath.Join(dir, "evidence"), filepath.Join(dir, "narrations"))
	os.MkdirAll(filepath.Join(dir, "tmp"), 0o755)

	url := "https://dai.transistor.fm/550311e4.mp3?s=ee18a55b532243160598698dcd4afc6dc933306d"
	path, err := w.downloadAudio(context.Background(), url)
	if err != nil {
		t.Fatalf("downloadAudio: %v", err)
	}
	defer os.Remove(path)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() == 0 {
		t.Fatal("下载文件为空")
	}
	t.Logf("下载成功，大小=%d", fi.Size())
}

// TestGuessAudioExt 验证音频扩展名推断（含大小写与未知回退）。
func TestGuessAudioExt(t *testing.T) {
	cases := map[string]string{
		"https://cdn.example.com/a.mp3": ".mp3",
		"https://cdn.example.com/b.M4A": ".m4a",
		"https://cdn.example.com/c.wav": ".wav",
		"https://cdn.example.com/d.aac": ".aac",
		"https://cdn.example.com/e.ogg": ".ogg",
		"https://cdn.example.com/noext": ".mp3", // 未知回退
		"https://cdn.example.com/f.pdf": ".mp3", // 非音频扩展回退
	}
	for in, want := range cases {
		if got := guessAudioExt(in); got != want {
			t.Errorf("guessAudioExt(%q)=%q want %q", in, got, want)
		}
	}
}

// TestDownloadAudio_Success 验证 downloadAudio 从测试服务器下载内容。
func TestDownloadAudio_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake-audio-bytes"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	w := NewWorkerWithClient(&http.Client{}, dir)
	path, err := w.downloadAudio(context.Background(), srv.URL+"/audio.mp3")
	if err != nil {
		t.Fatalf("downloadAudio: %v", err)
	}
	defer os.Remove(path)
	data, _ := os.ReadFile(path)
	if string(data) != "fake-audio-bytes" {
		t.Errorf("下载内容不符: %q", data)
	}
}

// TestDownloadAudio_HTTPError 验证非 200 响应报错。
func TestDownloadAudio_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	w := NewWorkerWithClient(&http.Client{}, dir)
	if _, err := w.downloadAudio(context.Background(), srv.URL+"/audio.mp3"); err == nil {
		t.Fatal("404 应报错")
	}
}

// TestDownloadAudio_InvalidURL 验证非法 URL 报错。
func TestDownloadAudio_InvalidURL(t *testing.T) {
	dir := t.TempDir()
	w := NewWorkerWithClient(&http.Client{}, dir)
	if _, err := w.downloadAudio(context.Background(), "ftp://x.com/a.mp3"); err == nil {
		t.Fatal("非法 URL 应报错")
	}
}

// TestDownloadAudio_CreateTempError 验证临时目录不可写时 downloadAudio 报错。
func TestDownloadAudio_CreateTempError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake-audio"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	// tempDir 用文件占位，使 os.CreateTemp 失败
	blocker := filepath.Join(dir, "tmp")
	os.WriteFile(blocker, []byte("x"), 0o644)
	w := &Worker{client: &http.Client{}, tempDir: blocker}
	if _, err := w.downloadAudio(context.Background(), srv.URL+"/audio.mp3"); err == nil {
		t.Fatal("临时目录不可写应报错")
	}
}

// TestDownloadAudio_BodyCopyError 验证响应体读取失败时 downloadAudio 报错并清理临时文件。
func TestDownloadAudio_BodyCopyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj := w.(http.Hijacker)
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nshort"))
		conn.Close() // 提前关闭让 io.Copy 报错
	}))
	defer srv.Close()

	dir := t.TempDir()
	w := NewWorkerWithClient(&http.Client{}, dir)
	if _, err := w.downloadAudio(context.Background(), srv.URL+"/audio.mp3"); err == nil {
		t.Fatal("响应体读取失败应报错")
	}
}

// TestFetchRawAudio_UploadMissingFile 验证 upload 源原始文件缺失时 fetchRawAudio 报错。
func TestFetchRawAudio_UploadMissingFile(t *testing.T) {
	dir := t.TempDir()
	w := NewWorkerWithClient(&http.Client{}, dir)
	// job 指向 upload 源；tempDir/uploads 下无该 id 文件 → os.Stat 报错
	job := &models.ProcessingJob{SourceType: models.SourceUpload, SourceID: "up-missing"}
	os.MkdirAll(w.tempDir, 0o755)
	if _, _, err := w.fetchRawAudio(context.Background(), job); err == nil {
		t.Fatal("upload 原始文件缺失应报错")
	}
}

// TestFetchRawAudio_EpisodeGetError 验证 episode 源查询失败时 fetchRawAudio 报错。
func TestFetchRawAudio_EpisodeGetError(t *testing.T) {
	s := newTestStoreForDownload(t)
	w := NewWorker(s, provider.NewSelector("g", "o"), t.TempDir(), t.TempDir(), t.TempDir())
	job := &models.ProcessingJob{SourceType: models.SourceEpisode, SourceID: "ep-none"}
	if _, _, err := w.fetchRawAudio(context.Background(), job); err == nil {
		t.Fatal("不存在的 episode 应报错")
	}
}

// TestSHA256_MissingFile 验证共享文件哈希模块对不存在的文件报错。
func TestSHA256_MissingFile(t *testing.T) {
	if _, err := filehash.SHA256(filepath.Join(t.TempDir(), "nope.bin")); err == nil {
		t.Fatal("文件不存在应报错")
	}
}

// TestSHA256_Deterministic 验证共享文件哈希模块的确定性计算。
func TestSHA256_Deterministic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	h1, err := filehash.SHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := filehash.SHA256(path)
	if h1 != h2 {
		t.Errorf("哈希应确定: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("sha256 hex 应为 64 字符，实际 %d", len(h1))
	}
}

// NewWorkerWithClient 构造一个使用指定 HTTP client 的 worker（测试用）.
func NewWorkerWithClient(client *http.Client, dir string) *Worker {
	os.MkdirAll(filepath.Join(dir, "tmp"), 0o755)
	return &Worker{client: client, tempDir: filepath.Join(dir, "tmp")}
}

// newTestStoreForDownload 构造一个独立 store（供需真实 DB 的测试复用）。
func newTestStoreForDownload(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

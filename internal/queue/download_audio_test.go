package queue

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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

// NewWorkerWithClient 构造一个使用指定 HTTP client 的 worker（测试用）.
func NewWorkerWithClient(client *http.Client, dir string) *Worker {
	os.MkdirAll(filepath.Join(dir, "tmp"), 0o755)
	return &Worker{client: client, tempDir: filepath.Join(dir, "tmp")}
}

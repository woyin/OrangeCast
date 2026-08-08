package queue

import (
	"context"
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

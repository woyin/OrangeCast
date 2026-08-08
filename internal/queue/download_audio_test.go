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

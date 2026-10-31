// Package linkimport 把一个链接变成本地音频文件，供上传流水线处理。
// 直接音频链接走 safehttp 下载（含 SSRF 防护）；已知站点（YouTube、小宇宙等）交给 yt-dlp。
// yt-dlp 自带抓取逻辑，无法逐跳校验重定向，所以只对白名单域名开放。
package linkimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/woyin/orangecast/internal/safehttp"
)

// MaxBytes 是单个导入音频的大小上限，与手动上传一致。
const MaxBytes = 200 << 20

// ytdlpHosts 是允许交给 yt-dlp 的站点（含其子域名）。
var ytdlpHosts = []string{"youtube.com", "youtu.be", "xiaoyuzhoufm.com", "bilibili.com", "b23.tv", "soundcloud.com"}

var audioExts = map[string]string{".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".wav": "audio/wav"}

// Result 是一次导入的产物。调用方负责删除 Path。
type Result struct {
	Path        string
	Title       string
	Filename    string // 含扩展名，用作上传记录的文件名
	ContentType string
	Size        int64
}

// Importer 下载链接指向的音频。
type Importer struct {
	YtDlp   string // yt-dlp 可执行文件；空则用 "yt-dlp"
	TempDir string // 中间文件目录
	// MaxBytes 覆盖单文件大小上限；0 表示用 MaxBytes 常量。
	MaxBytes int64
	// client 下载直接音频，测试可替换。
	Client *http.Client
}

// ErrUnsupported 表示链接既不是直接音频，也不在 yt-dlp 白名单内。
var ErrUnsupported = errors.New("不支持的链接：请使用直接音频地址（.mp3/.m4a/.wav），或 YouTube、小宇宙、B 站、SoundCloud 链接")

func (im *Importer) limit() int64 {
	if im.MaxBytes > 0 {
		return im.MaxBytes
	}
	return MaxBytes
}

func hostMatches(host string) bool {
	host = strings.ToLower(host)
	for _, h := range ytdlpHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// Fetch 下载 rawURL 的音频到 TempDir 下的新文件。
func (im *Importer) Fetch(ctx context.Context, rawURL string) (*Result, error) {
	rawURL = strings.TrimSpace(rawURL)
	if err := safehttp.ValidateURL(rawURL); err != nil {
		return nil, fmt.Errorf("链接无效：%w", err)
	}
	u, _ := url.Parse(rawURL)
	if u.Hostname() == "" || u.User != nil {
		return nil, errors.New("链接无效")
	}
	if err := os.MkdirAll(im.TempDir, 0o755); err != nil {
		return nil, err
	}
	if ext := strings.ToLower(path.Ext(u.Path)); audioExts[ext] != "" {
		return im.direct(ctx, u, ext)
	}
	if hostMatches(u.Hostname()) {
		return im.ytdlp(ctx, rawURL)
	}
	return nil, ErrUnsupported
}

func (im *Importer) direct(ctx context.Context, u *url.URL, ext string) (*Result, error) {
	limit := im.limit()
	client := im.Client
	if client == nil {
		client = safehttp.NewClient(5, limit, 10*time.Minute)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil) // URL 已校验，不会失败
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载失败：HTTP %d", resp.StatusCode)
	}
	if ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); strings.HasPrefix(ct, "text/") {
		return nil, errors.New("链接返回的是网页而不是音频")
	}
	f, err := os.CreateTemp(im.TempDir, "link-*"+ext)
	if err != nil {
		return nil, err
	}
	n, err := io.Copy(f, safehttp.LimitBody(resp.Body, limit+1))
	f.Close()
	if err == nil && n > limit {
		err = errors.New("音频超过 200MB 上限")
	}
	if err != nil {
		os.Remove(f.Name())
		return nil, err
	}
	name := path.Base(u.Path)
	return &Result{Path: f.Name(), Title: strings.TrimSuffix(name, ext), Filename: name, ContentType: audioExts[ext], Size: n}, nil
}

func (im *Importer) ytdlp(ctx context.Context, rawURL string) (*Result, error) {
	bin := im.YtDlp
	if bin == "" {
		bin = "yt-dlp"
	}
	dir, err := os.MkdirTemp(im.TempDir, "ytdlp-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	// `--` 之后的参数一律当作 URL，避免以 - 开头的输入被解析成选项。
	cmd := exec.CommandContext(ctx, bin, "--no-playlist", "--no-progress", "--max-filesize", "200M",
		"-f", "bestaudio[ext=m4a]/bestaudio", "-o", filepath.Join(dir, "%(id)s.%(ext)s"),
		"--print", "title", "--print", "after_move:filepath", "--", rawURL)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		switch {
		case errors.Is(err, exec.ErrNotFound), errors.Is(err, os.ErrNotExist):
			return nil, errors.New("服务器未安装 yt-dlp，无法导入该站点的链接")
		case errors.As(err, &ee):
			return nil, fmt.Errorf("yt-dlp 失败：%s", lastLine(string(ee.Stderr)))
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\r\n"), "\n")
	if len(lines) < 2 {
		return nil, errors.New("yt-dlp 没有返回文件")
	}
	title, file := strings.TrimSpace(lines[0]), strings.TrimSpace(lines[len(lines)-1])
	info, err := os.Stat(file)
	if err != nil {
		return nil, errors.New("yt-dlp 没有产出音频文件")
	}
	ext := strings.ToLower(filepath.Ext(file))
	dst := filepath.Join(im.TempDir, "link-"+uuid.NewString()+ext)
	if err := os.Rename(file, dst); err != nil {
		return nil, err
	}
	ct := audioExts[ext]
	if ct == "" {
		ct = "audio/" + strings.TrimPrefix(ext, ".")
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(file), ext)
	}
	return &Result{Path: dst, Title: title, Filename: title + ext, ContentType: ct, Size: info.Size()}, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

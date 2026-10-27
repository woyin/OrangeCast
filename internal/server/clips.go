// clips.go 高光片段音频导出（学习补齐：片段可单独播放、下载与分享）。
// 区间起止来自高光 Citation 的 Segment 时间（程序计算，AI 不自估），由 ffmpeg 从
// EvidenceAudio 切出；结果按源音频 sha256 + 区间缓存，第二次请求不再转码。
// 普通 GET 只读缓存或本地转码，不调用任何远端模型。
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// handleHighlightClip 输出一个高光片段的音频文件。
// 路径：/api/clip/{sourceType}/{sourceID}/{highlightID}[?format=mp3|wav]
func (srv *Server) handleHighlightClip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/clip/"), "/")
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}
	sourceType := models.SourceType(parts[0])
	sourceID, highlightID := parts[1], parts[2]
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "mp3"
	}
	if format != "mp3" && format != "wav" {
		http.Error(w, "仅支持 mp3 或 wav", http.StatusBadRequest)
		return
	}

	version, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindHighlight)
	if err != nil {
		http.Error(w, "该来源尚无高光片段", http.StatusNotFound)
		return
	}
	var set provider.HighlightSet
	if err := json.Unmarshal([]byte(version.Payload), &set); err != nil {
		http.Error(w, "高光数据损坏", http.StatusInternalServerError)
		return
	}
	target := findHighlight(set, highlightID)
	if target == nil {
		http.NotFound(w, r)
		return
	}
	transcript, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindTranscript)
	if err != nil {
		http.Error(w, "该来源尚无转录稿，无法定位片段", http.StatusNotFound)
		return
	}
	var payload provider.TranscriptPayload
	if err := json.Unmarshal([]byte(transcript.Payload), &payload); err != nil {
		http.Error(w, "转录数据损坏", http.StatusInternalServerError)
		return
	}
	start, end, ok := provider.ResolveCitationSpan(target.Citations, payload.Segments)
	if !ok || end <= start {
		http.Error(w, "该高光没有可定位的真实区间", http.StatusUnprocessableEntity)
		return
	}

	audio, err := srv.store.GetEvidenceAudio(r.Context(), sourceType, sourceID)
	if err != nil || audio.Status != "ready" {
		http.Error(w, "该来源没有可用的证据音频", http.StatusNotFound)
		return
	}
	srcPath := filepath.Join(srv.cfg.EvidenceDir, audio.RelPath)
	clipDir := srv.clipDir()
	clip, err := srv.store.GetHighlightClip(r.Context(), audio.SHA256, start, end, format)
	if err == nil {
		serveClipFile(w, r, filepath.Join(clipDir, clip.RelPath), format)
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		http.Error(w, "读取片段缓存失败", http.StatusInternalServerError)
		return
	}

	// 缓存未命中时才需要源文件：已生成的片段按音频身份独立有效。
	if _, statErr := os.Stat(srcPath); statErr != nil {
		http.Error(w, "证据音频文件缺失", http.StatusNotFound)
		return
	}
	prefix := audio.SHA256
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	rel := fmt.Sprintf("%s_h%d_%d.%s", prefix, int(start*1000), int(end*1000), format)
	dest := filepath.Join(clipDir, rel)
	if err := os.MkdirAll(clipDir, 0o755); err != nil {
		http.Error(w, "创建片段目录失败", http.StatusInternalServerError)
		return
	}
	if err := cutAudioClip(srcPath, dest, start, end, format); err != nil {
		http.Error(w, "切出片段失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	info, statErr := os.Stat(dest)
	var size int64
	if statErr == nil {
		size = info.Size()
	}
	if _, err := srv.store.CreateHighlightClip(r.Context(), audio.SHA256, start, end, format, rel, size); err != nil {
		// 缓存记录失败不阻断本次下载：音频已生成，下次会重新生成一次。
		_ = err
	}
	serveClipFile(w, r, dest, format)
}

// clipDir 返回片段缓存目录；未显式配置时按数据目录推导（测试与旧配置兼容）。
func (srv *Server) clipDir() string {
	if srv.cfg.ClipDir != "" {
		return srv.cfg.ClipDir
	}
	return filepath.Join(srv.cfg.DataDir, "clips")
}

// findHighlight 在冻结的高光集合中按稳定 ID 查找。
func findHighlight(set provider.HighlightSet, id string) *provider.Highlight {
	for i := range set.Highlights {
		if set.Highlights[i].ID == id {
			return &set.Highlights[i]
		}
	}
	return nil
}

// serveClipFile 输出片段文件，文件名带来源高光身份，便于 Owner 保存后辨认。
func serveClipFile(w http.ResponseWriter, r *http.Request, path, format string) {
	if _, err := os.Stat(path); err != nil {
		http.Error(w, "片段文件缺失", http.StatusNotFound)
		return
	}
	if format == "wav" {
		w.Header().Set("Content-Type", "audio/wav")
	} else {
		w.Header().Set("Content-Type", "audio/mpeg")
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, path)
}

// cutAudioClip 用 ffmpeg 从证据音频切出 [start, end) 片段。
// 只做本地转码，不访问网络；失败时删除半成品，避免留下损坏缓存。
func cutAudioClip(src, dest string, start, end float64, format string) error {
	duration := end - start
	if duration <= 0 {
		return fmt.Errorf("片段时长必须大于 0")
	}
	args := []string{"-nostdin", "-v", "error", "-y", "-ss", strconv.FormatFloat(start, 'f', 3, 64),
		"-t", strconv.FormatFloat(duration, 'f', 3, 64), "-i", src, "-vn"}
	if format == "wav" {
		args = append(args, "-ac", "1", "-ar", "22050", "-c:a", "pcm_s16le")
	} else {
		args = append(args, "-ac", "1", "-ar", "22050", "-c:a", "libmp3lame", "-b:a", "64k")
	}
	args = append(args, dest)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(dest); err != nil {
		return fmt.Errorf("片段未生成: %w", err)
	}
	return nil
}

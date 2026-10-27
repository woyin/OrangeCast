// structured_views.go 只读结构化视图：对齐译文 API 与结构导图数据。
// 两者都只读取当前不可变版本的 Content，不调用模型、不写库：
//   - 译文 API 返回 Segment 对齐行，缺失译文明确为 null，程序解析时间范围；
//   - 导图数据由章节与要点构成，节点层级来自 Citation 的 Segment 归属，不由 AI 生成。
package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// translationRow 一条对齐译文行（供 API 与导图复用）。
type translationRow struct {
	SegmentID   string   `json:"segment_id"`
	Start       float64  `json:"start"`
	End         float64  `json:"end"`
	Original    string   `json:"original"`
	Translation *string  `json:"translation"`
	Citations   []string `json:"citations"`
}

// handleTranslationAPI 返回某来源某语言的对齐译文。
// 路径：/api/translation/{sourceType}/{sourceID}?lang=zh
func (srv *Server) handleTranslationAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/translation/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	sourceType, sourceID := models.SourceType(parts[0]), parts[1]
	lang := strings.TrimSpace(r.URL.Query().Get("lang"))

	transcript, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindTranscript)
	if err != nil {
		http.Error(w, "该来源尚无转录稿", http.StatusNotFound)
		return
	}
	var payload provider.TranscriptPayload
	if err := json.Unmarshal([]byte(transcript.Payload), &payload); err != nil {
		http.Error(w, "转录数据损坏", http.StatusInternalServerError)
		return
	}
	translations := map[string]models.SegmentTranslation{}
	if lang != "" {
		translations, err = srv.store.ListSegmentTranslations(r.Context(), sourceType, sourceID, transcript.Version, lang)
		if err != nil {
			http.Error(w, "读取译文失败", http.StatusInternalServerError)
			return
		}
	}
	rows := make([]translationRow, 0, len(payload.Segments))
	for _, seg := range payload.Segments {
		row := translationRow{SegmentID: seg.ID, Start: seg.Start, End: seg.End, Original: seg.Text, Citations: []string{seg.ID}}
		if t, ok := translations[seg.ID]; ok && strings.TrimSpace(t.Text) != "" {
			text := t.Text
			row.Translation = &text
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source_type": sourceType, "source_id": sourceID, "language": lang,
		"transcript_version": transcript.Version, "segments": rows,
		"note": "译文是 CitedDerivative，逐段对齐；不得当作原文逐字引语",
	})
}

// mindMapNode 结构导图节点。
type mindMapNode struct {
	ID       string        `json:"id"`
	Kind     string        `json:"kind"` // title | chapter | keypoint | quote
	Label    string        `json:"label"`
	Detail   string        `json:"detail,omitempty"`
	Start    float64       `json:"start"`
	End      float64       `json:"end"`
	Children []mindMapNode `json:"children,omitempty"`
}

// handleMindMapAPI 返回以章节为骨架、要点为分支的结构导图数据。
// 路径：/api/mindmap/{sourceType}/{sourceID}
// 节点位置全部来自 Citation 解析；没有可解析位置的内容不会进入导图。
func (srv *Server) handleMindMapAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/mindmap/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	sourceType, sourceID := models.SourceType(parts[0]), parts[1]

	cardVersion, err := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindKnowledgeCard)
	if err != nil {
		http.Error(w, "该来源尚无知识卡片", http.StatusNotFound)
		return
	}
	var card provider.KnowledgeCard
	if err := json.Unmarshal([]byte(cardVersion.Payload), &card); err != nil {
		http.Error(w, "卡片数据损坏", http.StatusInternalServerError)
		return
	}
	var segments []provider.Segment
	if transcript, terr := srv.store.GetCurrentVersion(r.Context(), sourceType, sourceID, store.KindTranscript); terr == nil {
		var payload provider.TranscriptPayload
		if json.Unmarshal([]byte(transcript.Payload), &payload) == nil {
			segments = payload.Segments
		}
	}

	root := mindMapNode{ID: "root", Kind: "title", Label: card.Title}
	for i, ch := range card.Chapters {
		start, end, ok := provider.ResolveCitationSpan(ch.Citations, segments)
		if !ok {
			continue
		}
		node := mindMapNode{ID: "chapter-" + strconv.Itoa(i), Kind: "chapter", Label: ch.Title, Detail: ch.Gist, Start: start, End: end}
		for j, kp := range card.KeyPoints {
			kpStart, kpEnd, ok := provider.ResolveCitationSpan(kp.Citations, segments)
			if !ok || kpStart < start || kpStart > end {
				continue // 只挂到覆盖其引用的章节，避免错挂
			}
			node.Children = append(node.Children, mindMapNode{
				ID: "keypoint-" + strconv.Itoa(i) + "-" + strconv.Itoa(j), Kind: "keypoint",
				Label: kp.Content, Detail: kp.Description, Start: kpStart, End: kpEnd,
			})
		}
		root.Children = append(root.Children, node)
	}
	// 未被任何章节覆盖的要点直接挂在根下，保证不丢内容。
	for j, kp := range card.KeyPoints {
		start, end, ok := provider.ResolveCitationSpan(kp.Citations, segments)
		if !ok {
			continue
		}
		id := "loose-keypoint-" + strconv.Itoa(j)
		attached := false
		for _, ch := range root.Children {
			for _, child := range ch.Children {
				if child.Label == kp.Content {
					attached = true
				}
			}
		}
		if attached {
			continue
		}
		root.Children = append(root.Children, mindMapNode{ID: id, Kind: "keypoint", Label: kp.Content, Detail: kp.Description, Start: start, End: end})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source_type": sourceType, "source_id": sourceID, "card_version": cardVersion.Version,
		"root": root, "note": "节点时间来自 Citation 解析；导图不新增原文以外的内容",
	})
}

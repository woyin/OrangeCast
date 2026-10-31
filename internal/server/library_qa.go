package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

const (
	libraryQAHits    = 12  // 送给模型的候选片段数上限
	libraryQAMaxRune = 400 // 每个候选片段的文字上限
)

// libraryPassage 是一个送给问答模型的候选：命中片段及其前后相邻句，带稳定的临时编号。
type libraryPassage struct {
	Ref        string // 临时编号（c1…），模型只引用它；程序再映射回真实来源与时间
	SourceType models.SourceType
	SourceID   string
	Title      string
	Start, End float64
	Text       string
}

type libraryCitation struct {
	Title, Link, Range, Text string
}

// handleAskLibrary 是跨来源提问（“问我的库”）：先在整个资料库里召回相关片段，
// 再让问答模型只依据这些片段作答。遵守证据契约：模型没有引用任何候选片段时明确拒答。
// 召回使用 RetrieveExternal，只返回当前问答供应商被允许接收的来源（ModelDataPolicy）。
func (srv *Server) handleAskLibrary(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"CSRF": auth.CSRFValue(r)}
	if r.Method == http.MethodGet {
		srv.tmpl.Render(w, "ask.html", data)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	question := strings.TrimSpace(r.FormValue("question"))
	data["Question"] = question
	if n := len([]rune(question)); n < 2 || n > 500 {
		data["Error"] = "请输入 2–500 字的问题。"
		srv.tmpl.Render(w, "ask.html", data)
		return
	}
	st, _ := srv.store.GetSettings(r.Context())
	tc := taskConfigFrom(st.QAProvider, st.QAModel)
	bundle, err := srv.bundleFor(tc)
	if err != nil {
		data["Error"] = "问答模型不可用：" + err.Error()
		srv.tmpl.Render(w, "ask.html", data)
		return
	}
	passages, err := srv.libraryPassages(r.Context(), question, tc.Provider)
	if err != nil {
		data["Error"] = "检索失败：" + err.Error()
		srv.tmpl.Render(w, "ask.html", data)
		return
	}
	if len(passages) == 0 {
		data["Error"] = "资料库里没有找到相关内容，或相关来源不允许发送给当前问答模型。"
		srv.tmpl.Render(w, "ask.html", data)
		return
	}
	segments := make([]provider.Segment, len(passages))
	byRef := make(map[string]libraryPassage, len(passages))
	for i, p := range passages {
		segments[i] = provider.Segment{ID: p.Ref, Start: p.Start, End: p.End, Text: p.Text}
		byRef[p.Ref] = p
	}
	res, err := bundle.QA.Answer(question, segments)
	if err != nil {
		data["Error"] = "回答失败，请稍后重试。"
		srv.tmpl.Render(w, "ask.html", data)
		return
	}
	// 证据契约：只有模型实际引用的候选才能成为依据；没有就拒答，不把“检索到的”冒充依据。
	var cites []libraryCitation
	seen := map[string]bool{}
	if provider.HasReliableSources(res) {
		for _, s := range res.Sources {
			p, ok := byRef[s.SegmentID]
			if !ok || seen[p.Ref] {
				continue
			}
			seen[p.Ref] = true
			cites = append(cites, libraryCitation{
				Title: p.Title, Text: p.Text,
				Link:  fmt.Sprintf("/sources/%s/%s?t=%.1f", p.SourceType, p.SourceID, p.Start),
				Range: clock(p.Start) + " – " + clock(p.End),
			})
		}
	}
	if len(cites) == 0 {
		data["Error"] = "证据不足，无法可靠回答：模型没有引用任何可核验片段。请换一种问法。"
		srv.tmpl.Render(w, "ask.html", data)
		return
	}
	data["Answer"], data["Citations"] = res.Answer, cites
	srv.tmpl.Render(w, "ask.html", data)
}

// libraryPassages 召回候选并读取真实转录文字（命中句及前后各一句），按来源与位置去重。
func (srv *Server) libraryPassages(ctx context.Context, question, sendProvider string) ([]libraryPassage, error) {
	res, err := srv.store.Retrieve(ctx, store.KnowledgeRetrieveQuery{
		Purpose: store.RetrieveExternal,
		Search:  store.KnowledgeSearchQuery{Text: question, Kind: "original", SendProvider: sendProvider, PerPage: libraryQAHits},
	})
	if err != nil {
		return nil, err
	}
	transcripts := map[string][]provider.Segment{}
	var out []libraryPassage
	used := map[string]bool{}
	for _, h := range res.Hits {
		if h.SegmentID == "" || (h.SourceType != string(models.SourceEpisode) && h.SourceType != string(models.SourceUpload)) {
			continue
		}
		key := h.SourceType + ":" + h.SourceID
		segs, ok := transcripts[key]
		if !ok {
			segs = srv.currentSegments(ctx, models.SourceType(h.SourceType), h.SourceID)
			transcripts[key] = segs
		}
		i := -1
		for k, s := range segs {
			if s.ID == h.SegmentID {
				i = k
				break
			}
		}
		if i < 0 {
			continue
		}
		lo, hi := max(i-1, 0), min(i+1, len(segs)-1)
		if used[key+"#"+segs[lo].ID] {
			continue
		}
		var parts []string
		for _, s := range segs[lo : hi+1] {
			parts = append(parts, strings.TrimSpace(s.Text))
		}
		text := []rune(strings.Join(parts, " "))
		if len(text) > libraryQAMaxRune {
			text = text[:libraryQAMaxRune]
		}
		used[key+"#"+segs[lo].ID] = true
		out = append(out, libraryPassage{
			Ref: fmt.Sprintf("c%d", len(out)+1), SourceType: models.SourceType(h.SourceType), SourceID: h.SourceID,
			Title: h.Title, Start: segs[lo].Start, End: segs[hi].End, Text: string(text),
		})
	}
	return out, nil
}

// currentSegments 读取来源当前转录版本的全部句子；缺失或损坏时返回空。
func (srv *Server) currentSegments(ctx context.Context, sourceType models.SourceType, sourceID string) []provider.Segment {
	v, err := srv.store.GetCurrentVersion(ctx, sourceType, sourceID, store.KindTranscript)
	if err != nil {
		return nil
	}
	var tp provider.TranscriptPayload
	if json.Unmarshal([]byte(v.Payload), &tp) != nil {
		return nil
	}
	return tp.Segments
}

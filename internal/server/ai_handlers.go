// Package server 实现 CloudWisePod 的 HTTP 层：路由、handler、模板渲染与 REST API。
// 按职责拆分到多个文件（本文件：证据问答 / 复述讲解 / 学习对话（CitedDerivative 与 GeneratedDerivative））。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// ===--- 证据问答 EvidenceQA（CitedDerivative）---===
//
// handleEvidenceQA 处理单 Source 证据问答（EvidenceQA，ADR-0018）。
// EvidenceQA 属 CitedDerivative：回答必须挂 Citation、证据不足拒答（ADR-0008），
// 与 Phase E 新增的 StudyChat（GeneratedDerivative，挂 Reference、不拒答）并存且不可混淆。
// handleQA 为向后兼容别名。
func (srv *Server) handleEvidenceQA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceID := r.FormValue("source_id")
	question := r.FormValue("question")

	tp, ok := srv.loadTranscriptJSON(w, r.Context(), sourceType, sourceID)
	if !ok {
		return
	}
	// 读 settings 选 Q&A Provider + Model
	st, _ := srv.store.GetSettings(r.Context())
	bundle, err := srv.bundleFor(taskConfigFrom(st.QAProvider, st.QAModel))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	res, err := bundle.QA.Answer(question, tp.Segments)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "回答失败"})
		return
	}
	// 证据契约（ADR-0008 / Phase 7）：只有模型实际引用的 Segment 才能成为 Citation。
	// 无可靠引用时明确拒答，绝不附加"被检索到"的片段伪装成依据。
	if len(res.Sources) == 0 || strings.TrimSpace(res.Answer) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":   "证据不足，无法可靠回答：模型没有引用任何可核验片段。请换一种问法或查看转录稿。",
			"answer":  "",
			"sources": []provider.Source{},
		})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// evidenceQAResultToResponse 依据证据契约决定 EvidenceQA 响应（ADR-0008 / ADR-0018）。
// EvidenceQA 属 CitedDerivative：无可靠引用或空答案 → 422 明确拒答；否则 200 返回答案 + Citation 引用。
func evidenceQAResultToResponse(res *provider.QAResult) (int, map[string]any) {
	if !provider.HasReliableSources(res) {
		return http.StatusUnprocessableEntity, map[string]any{
			"error":   "证据不足，无法可靠回答：模型没有引用任何可核验片段。请换一种问法或查看转录稿。",
			"answer":  "",
			"sources": []provider.Source{},
		}
	}
	return http.StatusOK, map[string]any{
		"answer":  res.Answer,
		"sources": res.Sources,
	}
}

// ===--- 复述讲解 Paraphrase（GeneratedDerivative）---===
//
// handleParaphrase 处理复述讲解（Paraphrase，GeneratedDerivative，ADR-0018 R2）。
//
// Owner 在阅读转录稿时对某段（或某区间）触发"重讲"。系统：
//  1. 读取当前 Transcript，按 form 提供的 segment_ids 取参考 Segment；
//  2. 调用 ParaphraseProvider 生成 AI 讲解（非逐字原文，挂 Reference）；
//  3. 持久化为 Paraphrase（同锚点保留最近 3 次）；
//  4. 返回讲解 + Reference 跳转信息。
//
// 与 EvidenceQA 对照：EvidenceQA 是 CitedDerivative（挂 Citation、拒答）；
// Paraphrase 是 GeneratedDerivative（挂 Reference、明确标注 AI 生成）。
func (srv *Server) handleParaphrase(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceID := r.FormValue("source_id")
	question := strings.TrimSpace(r.FormValue("question"))
	referenceIDs := parseSegmentIDs(r.FormValue("segment_ids"))
	if len(referenceIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "复述讲解至少需要选择一个参考片段"})
		return
	}

	tp, ok := srv.loadTranscriptJSON(w, r.Context(), sourceType, sourceID)
	if !ok {
		return
	}
	segMap := make(map[string]provider.Segment, len(tp.Segments))
	for _, seg := range tp.Segments {
		segMap[seg.ID] = seg
	}
	var refs []provider.Segment
	for _, id := range referenceIDs {
		if seg, ok := segMap[id]; ok {
			refs = append(refs, seg)
		}
	}
	if len(refs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "参考片段在当前转录稿中不存在"})
		return
	}

	st, _ := srv.store.GetSettings(r.Context())
	cfg := taskConfigFrom(st.HighlightProvider, st.HighlightModel)
	bundle, err := srv.bundleFor(cfg)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	res, err := bundle.Paraphrase.Paraphrase(question, refs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "复述讲解失败"})
		return
	}

	row, err := srv.store.CreateParaphrase(r.Context(), sourceType, sourceID, question, res.Text,
		bundle.Paraphrase.Name(), cfg.Model, res.ReferenceSegmentIDs, tp.Segments)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "持久化复述讲解失败"})
		return
	}
	start, end := provider.ResolveReferenceRange(res.ReferenceSegmentIDs, tp.Segments)
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         row.ID,
		"text":       res.Text,
		"references": res.ReferenceSegmentIDs,
		"time_start": start,
		"time_end":   end,
		"generated":  true, // 明确标注为 AI 生成、非原文（GeneratedDerivative）
		"ai_note":    "AI 讲解·非原文（参考，不可逐字核验）",
	})
}

// ===--- 学习对话 StudyChat（GeneratedDerivative）---===
//
// handleStudyChat 处理学习对话（StudyChat，GeneratedDerivative，ADR-0018 R3）。
//
// 两条硬约束防止 GeneratedDerivative 退化为通用幻觉聊天助手：
//   - 硬约束一（scope 缰绳）：每轮回答必须挂至少一条指向当前 Source Segment 的 Reference，
//     无 Reference 不生成，改提示 Owner 该问题已超出本集范围。
//   - 硬约束二（防虚挂）：ReferenceCheck 独立判定回答主题是否扎根于 Reference 段；
//     校验失败则不呈现，给 Owner 可见反馈（问题越界，非系统故障）。
//
// 会话由 study_session_id 维持；首次提问自动建会话。
func (srv *Server) handleStudyChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	sourceType := models.SourceType(r.FormValue("source_type"))
	sourceID := r.FormValue("source_id")
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	question := strings.TrimSpace(r.FormValue("question"))
	if question == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请输入问题"})
		return
	}

	// 读取当前 Transcript 作为可检索候选 Segment。
	tp, ok := srv.loadTranscriptJSON(w, r.Context(), sourceType, sourceID)
	if !ok {
		return
	}

	sessionID, history, err := srv.studyChatSession(r.Context(), sourceType, sourceID, sessionID, question)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	// 先持久化用户问题（无论后续是否生成）。
	if _, err := srv.store.AppendStudyMessage(r.Context(), sessionID, "user", question, nil, false); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "记录问题失败"})
		return
	}

	// 选 Provider（复用 QA Provider/Model 设置，学习对话与问答同属"对话型"任务）。
	st, _ := srv.store.GetSettings(r.Context())
	bundle, err := srv.bundleFor(taskConfigFrom(st.QAProvider, st.QAModel))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	// 生成（硬约束一在 provider 内：无 Reference 不生成，返回 ScopeFeedback）。
	result, err := bundle.StudyChat.StudyChatAnswer(question, history, tp.Segments)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "学习对话生成失败"})
		return
	}
	if result.ScopeFeedback != "" || result.Answer == nil {
		// 硬约束一触发：问题超出本集范围。已记录用户问题，不生成回答。
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id":     sessionID,
			"scope_feedback": result.ScopeFeedback,
			"generated":      false,
			"out_of_scope":   true,
		})
		return
	}

	refSegs := studyReferenceSegments(result.Answer.ReferenceSegmentIDs, tp.Segments)
	check, err := bundle.RefChecker.CheckReference(question, result.Answer.Content, refSegs)
	if err != nil {
		// 校验本身失败：保守不呈现，记录被抑制的消息（含 suppress 标记）供评测。
		_, _ = srv.store.AppendStudyMessage(r.Context(), sessionID, "assistant", result.Answer.Content, result.Answer.ReferenceSegmentIDs, true)
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id":     sessionID,
			"scope_feedback": "我无法确认这个回答是否紧扣本集内容，暂时不展示。请尝试更贴近本集内容的问题。",
			"generated":      false,
			"check_error":    true,
		})
		return
	}
	if !check.Related {
		// 硬约束二失败：虚挂或主题漂移。记录被抑制的消息，给可见反馈。
		_, _ = srv.store.AppendStudyMessage(r.Context(), sessionID, "assistant", result.Answer.Content, result.Answer.ReferenceSegmentIDs, true)
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id":         sessionID,
			"scope_feedback":     "这个回答似乎脱离了本集内容（" + check.Reason + "）。请尝试更贴近本集内容的问题。",
			"generated":          false,
			"reference_rejected": true,
		})
		return
	}

	// 通过两条硬约束：持久化并呈现。
	if _, err := srv.store.AppendStudyMessage(r.Context(), sessionID, "assistant", result.Answer.Content, result.Answer.ReferenceSegmentIDs, false); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "持久化回答失败"})
		return
	}
	start, end := provider.ResolveReferenceRange(result.Answer.ReferenceSegmentIDs, tp.Segments)
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"answer":     result.Answer.Content,
		"references": result.Answer.ReferenceSegmentIDs,
		"time_start": start,
		"time_end":   end,
		"generated":  true,
		"ai_note":    "AI 讲解·非原文（参考，不可逐字核验）",
	})
}

func (srv *Server) studyChatSession(ctx context.Context, sourceType models.SourceType, sourceID, sessionID, question string) (string, []provider.StudyChatMessage, error) {
	if sessionID == "" {
		title := question
		if len([]rune(title)) > 40 {
			title = string([]rune(title)[:40]) + "…"
		}
		session, err := srv.store.CreateStudySession(ctx, sourceType, sourceID, title)
		if err != nil {
			return "", nil, fmt.Errorf("创建学习会话失败")
		}
		sessionID = session.ID
	}
	rows, err := srv.store.ListStudyMessages(ctx, sessionID, false)
	if err != nil {
		return "", nil, fmt.Errorf("读取会话历史失败")
	}
	history := make([]provider.StudyChatMessage, 0, len(rows))
	for _, row := range rows {
		history = append(history, provider.StudyChatMessage{Role: row.Role, Content: row.Content, ReferenceSegmentIDs: row.ReferenceSegmentIDs})
	}
	return sessionID, history, nil
}

func studyReferenceSegments(referenceIDs []string, segments []provider.Segment) []provider.Segment {
	byID := make(map[string]provider.Segment, len(segments))
	for _, segment := range segments {
		byID[segment.ID] = segment
	}
	references := make([]provider.Segment, 0, len(referenceIDs))
	for _, id := range referenceIDs {
		if segment, ok := byID[id]; ok {
			references = append(references, segment)
		}
	}
	return references
}

// handleStudyChatHistory 返回某会话的历史消息（用于回看）。
func (srv *Server) handleStudyChatHistory(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 session_id"})
		return
	}
	msgs, err := srv.store.ListStudyMessages(r.Context(), sessionID, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "读取历史失败"})
		return
	}
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, map[string]any{
			"role":                  m.Role,
			"content":               m.Content,
			"reference_segment_ids": m.ReferenceSegmentIDs,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out})
}

// loadTranscriptJSON 读取并解析当前 Transcript 版本，返回 TranscriptPayload。
// 失败时写入 JSON 错误响应并返回 false（供 handler 直接 return）。
// 收敛 EvidenceQA/Paraphrase/StudyChat 三处重复的"读转录稿 + 解析 payload"。
func (srv *Server) loadTranscriptJSON(w http.ResponseWriter, ctx context.Context, sourceType models.SourceType, sourceID string) (provider.TranscriptPayload, bool) {
	av, err := srv.store.GetCurrentVersion(ctx, sourceType, sourceID, store.KindTranscript)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "无转录稿"})
		return provider.TranscriptPayload{}, false
	}
	var tp provider.TranscriptPayload
	if err := json.Unmarshal([]byte(av.Payload), &tp); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "转录载荷损坏"})
		return provider.TranscriptPayload{}, false
	}
	return tp, true
}

// taskConfigFrom 从 settings 的 Provider/Model 指针构建任务级 TaskConfig。
// Provider 为空时回退默认 "groq"（ADR-0009），Model 可为空（由 selector 决定默认模型）。
func taskConfigFrom(providerPtr, modelPtr *string) provider.TaskConfig {
	tc := provider.TaskConfig{Provider: ptrStr(providerPtr), Model: ptrStr(modelPtr)}
	if tc.Provider == "" {
		tc.Provider = "groq"
	}
	return tc
}

// ptrStr 安全解引用 *string，nil 返回空串。
func ptrStr(p *string) string {
	if p != nil {
		return *p
	}
	return ""
}

// strPtr 把非空字符串转为 *string，空串返回 nil（settings 里空值表示"使用默认"）。
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

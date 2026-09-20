package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"sort"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

// handlePublicationPackage renders or downloads a package only after the exact revision passes evidence review.
func (srv *Server) handlePublicationPackage(w http.ResponseWriter, r *http.Request) {
	id, ok := strings.CutPrefix(r.URL.Path, "/workbench/revisions/")
	if !ok || !strings.HasSuffix(id, "/package") {
		http.NotFound(w, r)
		return
	}
	id = strings.TrimSuffix(id, "/package")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	revision, err := srv.store.GetArticleRevision(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	draft, err := srv.store.GetArticleDraft(r.Context(), revision.DraftID)
	if err != nil {
		http.Error(w, "读取文章草稿失败", http.StatusInternalServerError)
		return
	}
	// A passed historic snapshot remains auditable, but cannot be published
	// after a newer revision becomes current. This prevents an old approval
	// from bypassing the re-review requirement created by any edit.
	if draft.CurrentRevisionID == nil || *draft.CurrentRevisionID != revision.ID {
		http.Error(w, "只能为当前修订生成内容包；请先审校当前版本", http.StatusConflict)
		return
	}
	// R20：统一就绪门禁（新契约 = durable ClaimReview+StyleReview 均 passed；
	// 旧文章 = evidence+style 兼容规则）。缺失/failed/仅旧审校/其他 revision 均 409。
	readiness, err := srv.store.EvaluateArticlePublicationReadiness(r.Context(), id)
	if err != nil {
		http.Error(w, "检查交付门禁失败", http.StatusInternalServerError)
		return
	}
	if !readiness.Ready {
		http.Error(w, "当前修订尚未通过交付门禁："+strings.Join(readiness.Issues, "；"), http.StatusConflict)
		return
	}
	var sources []string
	if readiness.NewContract {
		sources, err = srv.claimMapSources(r, revision, draft.EditorialProfileID)
	} else {
		sources, err = srv.publicationSources(r, id, draft.EditorialProfileID)
	}
	if err != nil {
		var dbErr publicationDBError
		if errors.As(err, &dbErr) {
			http.Error(w, "读取来源或笔记失败："+err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, "当前证据或素材授权已失效，不能生成内容包："+err.Error(), http.StatusConflict)
		return
	}
	if r.URL.Query().Get("format") == "markdown" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+sanitizeFilename(revision.Title)+".md\"")
		_, _ = w.Write([]byte(revision.Markdown + "\n\n---\n\n## 来源\n" + markdownSourceList(sources)))
		return
	}
	packageData := buildPublicationPackage(revision.Title, revision.Markdown)
	switch r.URL.Query().Get("format") {
	case "plain":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+sanitizeFilename(revision.Title)+".txt\"")
		_, _ = w.Write([]byte(packageData.PlainText + "\n\n来源\n" + plainSourceList(sources)))
		return
	case "cover-svg":
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+sanitizeFilename(revision.Title)+"-cover.svg\"")
		_, _ = w.Write([]byte(coverSVG(packageData.CoverTitle, packageData.CoverSubtitle)))
		return
	}
	// R22：登记状态回显与显式动作表单（CSRF）。历史读取错误只允许 ErrNotFound
	// 视为未登记，其余显式 500（不得静默当作未登记）。
	registered := ""
	history, err := srv.store.GetArticleHistoryForRevision(r.Context(), revision.ID)
	switch {
	case err == nil:
		registered = history.Status
	case !errors.Is(err, store.ErrNotFound):
		http.Error(w, "读取登记状态失败", http.StatusInternalServerError)
		return
	}
	// ?history= 不能伪造：只有参数合法且等于数据库真实状态时才显示"刚刚完成登记"。
	flashStatus := ""
	if q := r.URL.Query().Get("history"); (q == "published" || q == "unpublished") && registered != "" && q == registered {
		flashStatus = q
	}
	srv.tmpl.Render(w, "publication_package.html", map[string]any{
		"Revision": revision, "Sources": sources, "RichHTML": template.HTML(wechatRichText(revision.Markdown)), "Package": packageData,
		"CSRF": auth.CSRFValue(r), "RegisteredStatus": registered, "FlashStatus": flashStatus, "NewContract": readiness.NewContract,
	})
}

type publicationPackage struct {
	PlainText, Summary, Recommendation, CoverTitle, CoverSubtitle string
	CandidateTitles                                               []string
}

func buildPublicationPackage(title, markdown string) publicationPackage {
	plain := markdownPlainText(markdown)
	summary := ""
	var headings []string
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if heading != "" && heading != title {
				headings = append(headings, heading)
			}
			continue
		}
		if summary == "" && trimmed != "" && !strings.HasPrefix(trimmed, "-") {
			summary = truncateRunes(trimmed, 120)
		}
	}
	if summary == "" {
		summary = truncateRunes(plain, 120)
	}
	titles := []string{title}
	for _, heading := range headings {
		candidate := title + "｜" + heading
		if len(titles) == 3 {
			break
		}
		titles = append(titles, candidate)
	}
	return publicationPackage{PlainText: plain, Summary: summary, Recommendation: "推荐阅读：" + summary, CoverTitle: title, CoverSubtitle: summary, CandidateTitles: titles}
}

func markdownPlainText(markdown string) string {
	var lines []string
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func truncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

func plainSourceList(sources []string) string {
	if len(sources) == 0 {
		return "本文未使用外部来源。\n"
	}
	return strings.Join(sources, "\n") + "\n"
}

func coverSVG(title, subtitle string) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="900" height="383" viewBox="0 0 900 383"><rect width="900" height="383" fill="#111827"/><rect x="48" y="48" width="8" height="287" rx="4" fill="#f59e0b"/><text x="88" y="150" fill="white" font-family="sans-serif" font-size="42" font-weight="700">%s</text><text x="88" y="215" fill="#d1d5db" font-family="sans-serif" font-size="22">%s</text><text x="88" y="315" fill="#9ca3af" font-family="sans-serif" font-size="18">CloudWisePod · Evidence-grounded</text></svg>`, html.EscapeString(truncateRunes(title, 22)), html.EscapeString(truncateRunes(subtitle, 36)))
}

func (srv *Server) publicationSources(r *http.Request, revisionID, profileID string) ([]string, error) {
	maps, err := srv.store.ListEvidenceMaps(r.Context(), revisionID)
	if err != nil {
		return nil, err
	}
	if len(maps) == 0 {
		return nil, fmt.Errorf("Revision 没有 EvidenceMap")
	}
	seen := map[string]bool{}
	var sources []string
	for _, mapping := range maps {
		var ids []string
		if err := json.Unmarshal([]byte(mapping.KeyPointIDs), &ids); err != nil {
			return nil, err
		}
		for _, id := range ids {
			keyPoint, err := srv.store.GetKeyPoint(r.Context(), id)
			if err != nil {
				return nil, err
			}
			usable, err := srv.store.CanUseSourceForPublication(r.Context(), profileID, keyPoint.SourceType, keyPoint.SourceID)
			if err != nil || !usable {
				return nil, fmt.Errorf("Source %s/%s 已归档或不可用", keyPoint.SourceType, keyPoint.SourceID)
			}
			var citations []string
			if err := json.Unmarshal([]byte(keyPoint.CitationsJSON), &citations); err != nil {
				return nil, err
			}
			valid, err := srv.store.ValidateSourceCitations(r.Context(), keyPoint.SourceType, keyPoint.SourceID, citations)
			if err != nil || !valid {
				return nil, fmt.Errorf("KeyPoint %s 的 Citation 已失效", keyPoint.ID)
			}
			title := strings.TrimSpace(keyPoint.SourceTitle)
			if title == "" {
				title = fmt.Sprintf("%s · %s", keyPoint.SourceType, keyPoint.SourceID)
			}
			sourceKey := string(keyPoint.SourceType) + "\x00" + keyPoint.SourceID
			if !seen[sourceKey] {
				seen[sourceKey] = true
				sources = append(sources, title)
			}
		}
	}
	return sources, nil
}

// claimMapSources R20：新契约文章的导出来源从该精确 revision 的 ClaimMap 材料 ID
// 派生；逐 KeyPoint 动态重验 CanUseSourceForPublication 与 Citation 有效性
// （归档/撤销/失效立即阻断导出）。只有 Owner/Synthesis 无来源表达时允许空来源。
func (srv *Server) claimMapSources(r *http.Request, revision *models.ArticleRevision, profileID string) ([]string, error) {
	entries, err := srv.store.ListClaimMap(r.Context(), revision.DraftID, revision.ID)
	if err != nil {
		return nil, err
	}
	var reviewedNotes map[string]provider.ClaimOwnerNote
	used := map[string]bool{}
	for _, entry := range entries {
		for _, id := range entry.MaterialIDs {
			used[id] = true
		}
	}
	seen := map[string]bool{}
	var sources []string
	// 排序保证来源输出确定性（map 迭代无序）。
	ids := make([]string, 0, len(used))
	for id := range used {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		keyPoint, kpErr := srv.store.GetKeyPoint(r.Context(), id)
		if kpErr == nil {
			usable, err := srv.store.CanUseSourceForPublication(r.Context(), profileID, keyPoint.SourceType, keyPoint.SourceID)
			if err != nil || !usable {
				return nil, fmt.Errorf("Source %s/%s 已归档或不可用", keyPoint.SourceType, keyPoint.SourceID)
			}
			var citations []string
			if err := json.Unmarshal([]byte(keyPoint.CitationsJSON), &citations); err != nil {
				return nil, err
			}
			valid, err := srv.store.ValidateSourceCitations(r.Context(), keyPoint.SourceType, keyPoint.SourceID, citations)
			if err != nil || !valid {
				return nil, fmt.Errorf("KeyPoint %s 的 Citation 已失效", keyPoint.ID)
			}
			title := strings.TrimSpace(keyPoint.SourceTitle)
			if title == "" {
				title = fmt.Sprintf("%s · %s", keyPoint.SourceType, keyPoint.SourceID)
			}
			sourceKey := string(keyPoint.SourceType) + "\x00" + keyPoint.SourceID
			if !seen[sourceKey] {
				seen[sourceKey] = true
				sources = append(sources, title)
			}
			continue
		}
		if !errors.Is(kpErr, store.ErrNotFound) {
			return nil, kpErr
		}
		// R23：OwnerNote 材料——精确加载笔记并重验其来源归档/可发布策略与
		// citations/references；来源按底层 source 去重，不把 note ID 当 KeyPoint。
		// ErrNotFound（材料已不存在）→ 409 文案；真实 DB 错误 → 500（可被调用方区分）。
		note, noteErr := srv.store.GetOwnerNote(r.Context(), id)
		if noteErr != nil {
			if errors.Is(noteErr, store.ErrNotFound) {
				return nil, fmt.Errorf("材料 %s 已不存在", id)
			}
			return nil, publicationDBError{fmt.Errorf("读取笔记 %s 失败: %w", id, noteErr)}
		}
		if reviewedNotes == nil {
			reviewInput, err := srv.store.GetPassedClaimReviewInput(r.Context(), revision.ID)
			if err != nil {
				return nil, publicationDBError{fmt.Errorf("读取已通过的主张审校快照失败: %w", err)}
			}
			reviewedNotes = make(map[string]provider.ClaimOwnerNote, len(reviewInput.OwnerNotes))
			for _, reviewed := range reviewInput.OwnerNotes {
				reviewedNotes[reviewed.ID] = reviewed
			}
		}
		frozen, ok := reviewedNotes[note.ID]
		if !ok {
			return nil, fmt.Errorf("个人笔记 %s 不在该修订的主张审校快照中", note.ID)
		}
		if note.Revision != frozen.Revision || note.Kind != frozen.Kind || note.Content != frozen.Content ||
			note.SourceType != frozen.SourceType || note.SourceID != frozen.SourceID {
			return nil, fmt.Errorf("个人笔记 %s 在审校后已修改，请重新生成并审校文章", note.ID)
		}
		usable, err := srv.store.CanUseSourceForPublication(r.Context(), profileID, models.SourceType(note.SourceType), note.SourceID)
		if err != nil || !usable {
			return nil, fmt.Errorf("笔记来源 %s/%s 已归档或不可用", note.SourceType, note.SourceID)
		}
		if note.Kind == "source_note" {
			var citations []string
			if err := json.Unmarshal([]byte(note.CitationsJSON), &citations); err != nil {
				return nil, fmt.Errorf("解析笔记引用失败: %w", err)
			}
			if len(citations) == 0 {
				return nil, fmt.Errorf("来源笔记 %s 缺少 Citation", note.ID)
			}
			valid, err := srv.store.ValidateSourceCitations(r.Context(), models.SourceType(note.SourceType), note.SourceID, citations)
			if err != nil || !valid {
				return nil, fmt.Errorf("来源笔记 %s 的 Citation 已失效", note.ID)
			}
		} else if note.Kind == "owner_reflection" {
			var references []string
			if err := json.Unmarshal([]byte(note.ReferencesJSON), &references); err != nil {
				return nil, publicationDBError{fmt.Errorf("解析个人笔记 %s 的 Reference 失败: %w", note.ID, err)}
			}
			if len(references) > 0 {
				valid, err := srv.store.ValidateSourceCitations(r.Context(), models.SourceType(note.SourceType), note.SourceID, references)
				if err != nil || !valid {
					return nil, fmt.Errorf("个人笔记 %s 的 Reference 已失效", note.ID)
				}
			}
		}
		sourceKey := string(note.SourceType) + "\x00" + note.SourceID
		if !seen[sourceKey] {
			seen[sourceKey] = true
			sources = append(sources, srv.store.SourceTitle(r.Context(), models.SourceType(note.SourceType), note.SourceID))
		}
	}
	return sources, nil
}

func markdownSourceList(sources []string) string {
	if len(sources) == 0 {
		return "- 本文未使用外部来源。\n"
	}
	var b strings.Builder
	for _, source := range sources {
		b.WriteString("- ")
		b.WriteString(source)
		b.WriteByte('\n')
	}
	return b.String()
}

// wechatRichText deliberately supports the conservative Markdown subset emitted by Writer and Owner edits.
// User text is escaped before assembly; no raw HTML from a revision is trusted.
func wechatRichText(markdown string) string {
	var b strings.Builder
	inList := false
	closeList := func() {
		if inList {
			b.WriteString("</ul>")
			inList = false
		}
	}
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			closeList()
		case strings.HasPrefix(trimmed, "### "):
			closeList()
			b.WriteString("<h3>")
			b.WriteString(html.EscapeString(strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))))
			b.WriteString("</h3>")
		case strings.HasPrefix(trimmed, "## "):
			closeList()
			b.WriteString("<h2>")
			b.WriteString(html.EscapeString(strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))))
			b.WriteString("</h2>")
		case strings.HasPrefix(trimmed, "# "):
			closeList()
			b.WriteString("<h1>")
			b.WriteString(html.EscapeString(strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))))
			b.WriteString("</h1>")
		case strings.HasPrefix(trimmed, "- "):
			if !inList {
				b.WriteString("<ul>")
				inList = true
			}
			b.WriteString("<li>")
			b.WriteString(html.EscapeString(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))))
			b.WriteString("</li>")
		default:
			closeList()
			b.WriteString("<p>")
			b.WriteString(html.EscapeString(trimmed))
			b.WriteString("</p>")
		}
	}
	closeList()
	return b.String()
}

// publicationDBError 标记来源/笔记校验中的真实数据库故障（500），与门禁冲突（409）区分。
type publicationDBError struct{ err error }

func (e publicationDBError) Error() string { return e.err.Error() }
func (e publicationDBError) Unwrap() error { return e.err }

// handleRecordArticleHistory Owner 显式登记创作历史（R22 / C13）：
// published（已在外部渠道发布）或 unpublished（已写作未发布）。
// POST 必须重新检查 exact current readiness 与来源有效性——不能绕过门禁；
// 导出/预览/内容包 GET 绝不调用本方法。完成后回到内容包页显示已登记状态。
func (srv *Server) handleRecordArticleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}
	revisionID := strings.TrimSpace(r.FormValue("revision_id"))
	status := strings.TrimSpace(r.FormValue("status"))
	if revisionID == "" || (status != "published" && status != "unpublished") {
		http.Error(w, "参数非法", http.StatusBadRequest)
		return
	}
	revision, err := srv.store.GetArticleRevision(r.Context(), revisionID)
	if err != nil {
		http.Error(w, "文章修订不存在", http.StatusNotFound)
		return
	}
	draft, err := srv.store.GetArticleDraft(r.Context(), revision.DraftID)
	if err != nil {
		http.Error(w, "读取文章草稿失败", http.StatusInternalServerError)
		return
	}
	// 门禁重检：exact current + readiness + 来源有效性（动态重验，不信任 GET 时的快照）。
	if draft.CurrentRevisionID == nil || *draft.CurrentRevisionID != revision.ID {
		http.Error(w, "只能登记当前修订；请先审校当前版本", http.StatusConflict)
		return
	}
	readiness, err := srv.store.EvaluateArticlePublicationReadiness(r.Context(), revision.ID)
	if err != nil {
		http.Error(w, "检查交付门禁失败", http.StatusInternalServerError)
		return
	}
	if !readiness.Ready {
		http.Error(w, "当前修订尚未通过交付门禁："+strings.Join(readiness.Issues, "；"), http.StatusConflict)
		return
	}
	var dbErr publicationDBError
	if readiness.NewContract {
		if _, err = srv.claimMapSources(r, revision, draft.EditorialProfileID); err != nil {
			if errors.As(err, &dbErr) {
				http.Error(w, "读取来源或笔记失败："+err.Error(), http.StatusInternalServerError)
				return
			}
			http.Error(w, "当前证据或素材授权已失效，不能登记："+err.Error(), http.StatusConflict)
			return
		}
	} else {
		if _, err = srv.publicationSources(r, revision.ID, draft.EditorialProfileID); err != nil {
			http.Error(w, "当前证据或素材授权已失效，不能登记："+err.Error(), http.StatusConflict)
			return
		}
	}
	if _, err := srv.store.RecordArticleHistory(r.Context(), revision.ID, status); err != nil {
		writeEditorialError(w, err)
		return
	}
	// 回到内容包页并显示已登记状态。
	http.Redirect(w, r, "/workbench/revisions/"+revision.ID+"/package?history="+status, http.StatusSeeOther)
}

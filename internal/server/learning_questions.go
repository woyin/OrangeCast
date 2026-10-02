package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
)

type questionRelationView struct {
	store.LearningQuestionRelation
	Label, Href      string
	Missing, Changed bool
}

func (srv *Server) questionRelationView(r *http.Request, l store.LearningQuestionRelation) questionRelationView {
	v := questionRelationView{LearningQuestionRelation: l, Label: l.ObjectID}
	ctx := r.Context()
	var err error
	switch l.Kind {
	case "source":
		v.Label = srv.store.SourceTitle(ctx, models.SourceType(l.SourceType), l.SourceID)
		v.Href = sourceHref(models.SourceType(l.SourceType), l.SourceID, 0)
	case "note":
		var n *models.OwnerNote
		n, err = srv.store.GetOwnerNote(ctx, l.ObjectID)
		if err == nil {
			v.Label = n.Content
			v.Changed = n.Revision != l.Version
			v.Href = sourceHref(models.SourceType(n.SourceType), n.SourceID, 0)
		}
	case "keypoint":
		var k *store.KeyPointRow
		k, err = srv.store.GetKeyPoint(ctx, l.ObjectID)
		if err == nil {
			v.Label = k.Content
			v.Changed = k.CardVersion != l.Version
			v.Href = sourceHref(k.SourceType, k.SourceID, 0)
		}
	case "evidence":
		var s *models.SourceSnapshot
		s, err = srv.store.GetSourceSnapshot(ctx, l.ObjectID)
		if err == nil {
			v.Label = s.Title
			v.Href = "/evidence/" + s.ID
			v.Missing = s.Status == models.SnapshotPurged
		}
	case "article":
		var a *store.KnowledgeArticleRecord
		a, err = srv.store.GetKnowledgeArticle(ctx, l.ObjectID)
		if err == nil {
			v.Label = a.Title
			v.Href = "/knowledge-articles/" + a.ID
			v.Changed = l.Origin != "generation" && a.WorkingRevision != l.Version
		}
	}
	v.Missing = v.Missing || err != nil
	if len([]rune(v.Label)) > 240 {
		v.Label = string([]rune(v.Label)[:240]) + "…"
	}
	return v
}
func (srv *Server) handleLearningQuestions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	list, err := srv.store.ListLearningQuestions(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("status"))
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	profile, err := srv.store.EnsureDefaultEditorialProfile(r.Context())
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	themes, err := srv.store.ListThemes(r.Context(), profile.ID)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	if err = srv.tmpl.Render(w, "learning_questions.html", map[string]any{"Questions": list, "Themes": themes, "Query": r.URL.Query(), "CSRF": auth.CSRFValue(r)}); err != nil {
		http.Error(w, "渲染学习问题失败", 500)
	}
}
func (srv *Server) handleLearningQuestionDetail(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/understandings") {
		srv.handleQuestionUnderstandings(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/gaps") {
		srv.handleEvidenceGaps(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/study") {
		srv.handleQuestionStudy(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/questions/")
	q, err := srv.store.GetLearningQuestion(r.Context(), id)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	links, err := srv.store.ListLearningQuestionRelations(r.Context(), id)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	relations := []questionRelationView{}
	confirmed, suggested := 0, 0
	for _, l := range links {
		relations = append(relations, srv.questionRelationView(r, l))
		if l.State == "confirmed" {
			confirmed++
		} else {
			suggested++
		}
	}
	articles, err := srv.store.ListLearningQuestionArticles(r.Context(), id)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	passed, pending := 0, 0
	for _, a := range articles {
		if a.PassedRevision > 0 {
			passed++
		}
		if a.WorkingRevision > a.PassedRevision || a.PassedRevision == 0 {
			pending++
		}
	}
	ops, err := srv.store.ListLearningQuestionOperations(r.Context(), id)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	profile, err := srv.store.EnsureDefaultEditorialProfile(r.Context())
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	themes, err := srv.store.ListThemes(r.Context(), profile.ID)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	search := strings.TrimSpace(r.URL.Query().Get("material_q"))
	page, _ := strconv.Atoi(r.URL.Query().Get("material_page"))
	if page < 1 {
		page = 1
	}
	materials, err := srv.store.SearchKnowledge(r.Context(), store.KnowledgeSearchQuery{Kind: "materials", Text: search, Page: page, PerPage: 20, MetadataOnly: true})
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	sources, err := srv.store.SearchKnowledgeSources(r.Context(), store.KnowledgeListQuery{Text: search, Page: page, PerPage: 20}, "")
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	evidenceOptions := []*models.SourceSnapshot{}
	for _, source := range sources.Items {
		snapshots, e := srv.store.ListSourceSnapshots(r.Context(), models.SourceType(source.SourceType), source.SourceID)
		if e != nil {
			questionHTTPError(w, e)
			return
		}
		for _, snapshot := range snapshots {
			if snapshot.Status != models.SnapshotPurged {
				evidenceOptions = append(evidenceOptions, snapshot)
				if len(evidenceOptions) >= 100 {
					break
				}
			}
		}
		if len(evidenceOptions) >= 100 {
			break
		}
	}
	articleOptions, e := srv.store.SearchKnowledge(r.Context(), store.KnowledgeSearchQuery{Kind: "article", Text: search, Page: page, PerPage: 20, MetadataOnly: true, IncludeDrafts: true})
	if e != nil {
		questionHTTPError(w, e)
		return
	}
	f, err := srv.store.FreezeLearningQuestion(r.Context(), id, false)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	eligible, err := srv.store.SearchKnowledge(r.Context(), store.KnowledgeSearchQuery{Kind: "materials", Question: f, Recall: true, SendProvider: "pod", RecallProfileID: profile.ID, MetadataOnly: true, PerPage: 20})
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	data := map[string]any{"EvidenceOptions": evidenceOptions, "ArticleOptions": articleOptions.Hits, "Question": q, "Themes": themes, "Relations": relations, "ConfirmedCount": confirmed, "SuggestedCount": suggested, "Articles": articles, "PassedCount": passed, "PendingCount": pending, "Operations": ops, "Materials": materials.Hits, "Sources": sources.Items, "Search": search, "Page": page, "PrevPage": page - 1, "NextPage": page + 1, "HasNext": page*20 < materials.Total || page*20 < sources.Total, "EligibleCount": eligible.Total, "Insufficient": eligible.Total < 2, "CSRF": auth.CSRFValue(r)}
	if err = srv.tmpl.Render(w, "learning_question.html", data); err != nil {
		http.Error(w, "渲染学习问题失败", 500)
	}
}
func questionHTTPError(w http.ResponseWriter, err error) {
	code := 400
	message := err.Error()
	if errors.Is(err, store.ErrConflict) {
		code = 409
		message = "问题已被其他操作修改或当前状态不允许自动任务。请刷新核对；当前输入尚未保存。"
	} else if errors.Is(err, store.ErrNotFound) {
		code = 404
		message = "问题或关联对象不存在"
	}
	http.Error(w, message, code)
}
func questionResult(w http.ResponseWriter, r *http.Request, href string) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"href": href})
		return
	}
	http.Redirect(w, r, href, http.StatusSeeOther)
}
func (srv *Server) handleLearningQuestionAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单无效", 400)
		return
	}
	action, id := r.FormValue("action"), r.FormValue("question_id")
	if action == "create" {
		q, err := srv.store.CreateLearningQuestion(r.Context(), store.LearningQuestion{Body: r.FormValue("body"), Goal: r.FormValue("goal"), ThemeID: r.FormValue("theme_id"), TargetDate: r.FormValue("target_date")})
		if err != nil {
			questionHTTPError(w, err)
			return
		}
		questionResult(w, r, "/questions/"+q.ID)
		return
	}
	expected, err := strconv.Atoi(r.FormValue("expected_revision"))
	if err != nil || expected < 1 {
		http.Error(w, "缺少有效的问题修订号", 400)
		return
	}
	if action == "generate" {
		q, e := srv.store.GetLearningQuestion(r.Context(), id)
		if e != nil {
			questionHTTPError(w, e)
			return
		}
		if q.Revision != expected {
			questionHTTPError(w, store.ErrConflict)
			return
		}
		if !srv.cfg.PodAvailable() {
			http.Error(w, "请配置.env中的POD_*并重启", 400)
			return
		}
		profile, e := srv.store.EnsureDefaultEditorialProfile(r.Context())
		if e != nil {
			questionHTTPError(w, e)
			return
		}
		a, _, e := srv.enqueueKnowledgeArticleScope(r.Context(), profile.ID, false, store.KnowledgeScope{QuestionID: id, ExpectedQuestionRevision: expected})
		if e != nil {
			questionHTTPError(w, e)
			return
		}
		questionResult(w, r, "/knowledge-articles/"+a.ID)
		return
	}
	if action == "note" {
		source := strings.SplitN(r.FormValue("source"), ":", 2)
		if len(source) != 2 {
			http.Error(w, "请选择笔记来源", 400)
			return
		}
		_, err = srv.store.CreateLearningQuestionNote(r.Context(), id, expected, models.OwnerNote{SourceType: source[0], SourceID: source[1], Content: r.FormValue("content"), ReferencesJSON: r.FormValue("references_json"), AnchorJSON: r.FormValue("anchor_json")})
		if err != nil {
			questionHTTPError(w, err)
			return
		}
		questionResult(w, r, "/questions/"+id)
		return
	}
	c := store.LearningQuestionChange{Action: action, Body: r.FormValue("body"), Goal: r.FormValue("goal"), ThemeID: r.FormValue("theme_id"), TargetDate: r.FormValue("target_date"), Status: r.FormValue("status"), Link: provider.LearningQuestionLink{Kind: r.FormValue("kind"), ObjectID: r.FormValue("object_id")}}
	if c.Link.Kind == "" {
		relation := strings.SplitN(r.FormValue("relation"), ":", 2)
		if len(relation) == 2 {
			c.Link.Kind, c.Link.ObjectID = relation[0], relation[1]
		}
	}
	if c.Link.Kind == "source" {
		source := strings.SplitN(c.Link.ObjectID, ":", 2)
		if len(source) == 2 {
			c.Link.SourceType, c.Link.SourceID = source[0], source[1]
		}
	}
	_, err = srv.store.ChangeLearningQuestion(r.Context(), id, expected, c)
	if err != nil {
		questionHTTPError(w, err)
		return
	}
	href := "/questions/" + id
	if action == "delete" {
		href = "/questions"
	}
	questionResult(w, r, href)
}

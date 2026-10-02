package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type qualityParagraphView struct {
	Index      int
	Text, Hash string
}

func (srv *Server) handleArticleQualityCases(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "方法不允许", 405)
		return
	}
	srv.renderArticleQualityCases(w, r, 200, "", nil)
}

type qualityFeedbackView struct {
	store.ArticleQualityFeedback
	CategoryLabel, Preview string
}

type qualityRecoveryField struct {
	Name, Value string
	Editable    bool
}

func (srv *Server) renderArticleQualityCases(w http.ResponseWriter, r *http.Request, status int, message string, form url.Values) {
	cases, err := srv.store.ListArticleQualityCases(r.Context())
	if err != nil {
		http.Error(w, "无法读取案例", 500)
		return
	}
	feedback, err := srv.store.ListArticleQualityFeedback(r.Context())
	if err != nil {
		http.Error(w, "无法读取反馈", 500)
		return
	}
	facts := map[string]store.ArticleQualityFacts{}
	for _, c := range cases {
		f, e := srv.store.ArticleQualityCaseFacts(r.Context(), c.ID)
		if e != nil {
			http.Error(w, "无法读取冻结事实", 500)
			return
		}
		facts[c.ID] = f
	}
	views := make([]qualityFeedbackView, 0, len(feedback))
	for _, f := range feedback {
		label := map[string]string{"useful": "有用", "shallow": "太浅", "duplicate": "重复", "misattribution": "归因不准"}[f.Category]
		preview := []rune(f.Comment)
		if len(preview) > 120 {
			preview = append(preview[:120], '…')
		}
		views = append(views, qualityFeedbackView{f, label, string(preview)})
	}
	classifyKeys, retireKeys := map[string]string{}, map[string]string{}
	for _, c := range cases {
		classifyKeys[c.ID] = uuid.NewString()
		retireKeys[c.ID] = uuid.NewString()
	}
	data := map[string]any{"Cases": cases, "Feedback": views, "Facts": facts, "CSRF": auth.CSRFValue(r), "AcceptRequestKey": uuid.NewString(), "ClassifyKeys": classifyKeys, "RetireKeys": retireKeys, "Error": message}
	if form != nil {
		fields := []qualityRecoveryField{}
		for _, name := range []string{"action", "article_id", "revision", "content_hash", "paragraph_index", "paragraph_hash", "category", "comment", "feedback_id", "version", "expected", "request_key", "case_id", "classification", "evidence", "provider", "case_ids"} {
			if values, ok := form[name]; ok && len(values) > 0 {
				fields = append(fields, qualityRecoveryField{name, values[0], name == "comment" || name == "expected" || name == "evidence"})
			}
		}
		data["RecoveryFields"] = fields
	}
	article := r.URL.Query().Get("article_id")
	revision, _ := strconv.Atoi(r.URL.Query().Get("revision"))
	if article == "" && form != nil {
		article = form.Get("article_id")
		revision, _ = strconv.Atoi(form.Get("revision"))
	}
	if article != "" {
		v, err := srv.store.GetKnowledgeRevision(r.Context(), article, revision)
		if err != nil && form == nil {
			http.Error(w, "原文章版本不可用", 404)
			return
		}
		if err == nil {
			var blocks []provider.KnowledgeBlock
			json.Unmarshal([]byte(v.BlocksJSON), &blocks)
			paras := []qualityParagraphView{}
			for i, b := range blocks {
				paras = append(paras, qualityParagraphView{i, b.Text, articleParagraphHash(b.Text)})
			}
			data["Revision"] = v
			data["Paragraphs"] = paras
		}
	}
	var body bytes.Buffer
	if err = srv.tmpl.Render(&body, "quality_cases.html", data); err != nil {
		http.Error(w, "页面错误", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(body.Bytes())
}
func (srv *Server) handleArticleQualityAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "方法不允许", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16000)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "请求过大或无效", 400)
		return
	}
	var result any
	var err error
	revision, _ := strconv.Atoi(r.FormValue("revision"))
	version, _ := strconv.Atoi(r.FormValue("version"))
	switch r.FormValue("action") {
	case "feedback":
		p, _ := strconv.Atoi(r.FormValue("paragraph_index"))
		result, err = srv.store.RecordArticleQualityFeedback(r.Context(), r.FormValue("article_id"), revision, p, r.FormValue("content_hash"), r.FormValue("paragraph_hash"), r.FormValue("category"), r.FormValue("comment"))
	case "accept":
		result, err = srv.store.AcceptArticleQualityCase(r.Context(), r.FormValue("feedback_id"), r.FormValue("request_key"), version, r.FormValue("expected"))
	case "classify":
		err = srv.store.ClassifyArticleQualityCaseCommand(r.Context(), r.FormValue("case_id"), version, r.FormValue("classification"), r.FormValue("evidence"), r.FormValue("request_key"))
	case "retire":
		err = srv.store.RetireArticleQualityCaseCommand(r.Context(), r.FormValue("case_id"), version, r.FormValue("request_key"))
	case "manifest":
		var ids []string
		if json.Unmarshal([]byte(r.FormValue("case_ids")), &ids) != nil {
			http.Error(w, "案例列表无效", 400)
			return
		}
		result, err = srv.store.BuildArticleQualityManifest(r.Context(), ids, r.FormValue("provider"))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="private-quality-manifest.json"`)
	default:
		http.Error(w, "操作无效", 400)
		return
	}
	if err != nil {
		status := 400
		if errors.Is(err, store.ErrConflict) {
			status = 409
		}
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			http.Error(w, "操作未完成；保留输入并核对原版本", status)
		} else {
			w.Header().Del("Content-Disposition")
			srv.renderArticleQualityCases(w, r, status, "操作未完成；原输入和请求身份保留，请核对原版本。", r.PostForm)
		}
		return
	}
	if !strings.Contains(r.Header.Get("Accept"), "application/json") && r.FormValue("action") != "manifest" {
		destination := "/quality-cases"
		if r.FormValue("article_id") != "" {
			destination += "?" + url.Values{"article_id": {r.FormValue("article_id")}, "revision": {r.FormValue("revision")}}.Encode()
		}
		http.Redirect(w, r, destination, http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{"result": result})
}

func articleParagraphHash(text string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(text))) }

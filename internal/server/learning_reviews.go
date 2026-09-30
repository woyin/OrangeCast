package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/provider"
	"github.com/woyin/orangecast/internal/store"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// StartLearningReviews runs the independently opted-in weekly scheduler.
func (srv *Server) StartLearningReviews(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			if err := srv.RunLearningReviews(ctx, time.Now()); err != nil {
				log.Printf("每周学习回顾: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// RunLearningReviews admits only due weekly batches and uses the existing POD connection.
func (srv *Server) RunLearningReviews(ctx context.Context, now time.Time) error {
	prefs, err := srv.store.GetLearningReviewSettings(ctx)
	if err != nil {
		return err
	}
	if !store.LearningReviewDue(now, prefs) {
		return nil
	}
	if !srv.cfg.PodAvailable() {
		return fmt.Errorf("POD_* 配置不完整，回顾暂停")
	}
	profile, err := srv.store.EnsureDefaultEditorialProfile(ctx)
	if err != nil {
		return err
	}
	_, _, err = srv.store.ReserveLearningReview(ctx, profile.ID, "pod", srv.cfg.PodModel, now, true)
	return err
}

type learningReviewView struct {
	Item    store.LearningReviewItem
	Links   []knowledgeMaterialLink
	History []store.LearningReviewAnswer
}

func (srv *Server) handleLearningReview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", 405)
		return
	}
	prefs, err := srv.store.GetLearningReviewSettings(r.Context())
	if err != nil {
		http.Error(w, "读取回顾设置失败", 500)
		return
	}
	batches, err := srv.store.ListLearningReviewBatches(r.Context())
	if err != nil {
		http.Error(w, "读取回顾历史失败", 500)
		return
	}
	data := map[string]any{"Settings": prefs, "Batches": batches, "CSRF": auth.CSRFValue(r), "Available": srv.cfg.PodAvailable()}
	id := r.URL.Query().Get("batch")
	if id == "" && len(batches) > 0 {
		id = batches[0].ID
	}
	if id != "" {
		batch, e := srv.store.GetLearningReviewBatch(r.Context(), id)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		data["Batch"] = batch
		items, e := srv.store.ListLearningReviewItems(r.Context(), id)
		if e != nil {
			http.Error(w, "读取解释问题失败", 500)
			return
		}
		var req provider.KnowledgeArticleRequest
		if json.Unmarshal([]byte(batch.InputJSON), &req) != nil {
			http.Error(w, "回顾依据损坏", 500)
			return
		}
		var views []learningReviewView
		for _, item := range items {
			v := learningReviewView{Item: item}
			if item.Revealed {
				var ids []string
				if json.Unmarshal([]byte(item.MaterialIDsJSON), &ids) != nil {
					http.Error(w, "问题依据损坏", 500)
					return
				}
				blocks := knowledgeArticleViews(req, []provider.KnowledgeBlock{{Kind: "synthesis", MaterialIDs: ids}})
				if len(blocks) > 0 {
					v.Links = blocks[0].Links
				}
				v.History, e = srv.store.LearningReviewAnswerHistory(r.Context(), item.ID)
				if e != nil {
					http.Error(w, "读取回答历史失败", 500)
					return
				}
			}
			views = append(views, v)
		}
		data["Items"] = views
		if e = srv.store.CheckKnowledgeMaterials(r.Context(), batch.ProfileID, batch.Provider, req.Materials); e != nil {
			if !errors.Is(e, store.ErrConflict) && !errors.Is(e, store.ErrNotFound) && !errors.Is(e, store.ErrSnapshotInvalidated) {
				http.Error(w, "读取回顾依据失败", 500)
				return
			}
			data["EvidenceWarning"] = "部分学习材料已变化或不可用；以下仍是生成时的冻结依据，请重新核对。"
		}
		var input, output int
		var cost *float64
		e = srv.store.DB.QueryRowContext(r.Context(), `SELECT COALESCE(SUM(u.input_units),0),COALESCE(SUM(u.output_units),0),CASE WHEN COUNT(u.id)>0 AND SUM(CASE WHEN u.estimated_cost IS NULL THEN 1 ELSE 0 END)=0 THEN SUM(u.estimated_cost) ELSE NULL END FROM processing_jobs j LEFT JOIN usage_records u ON u.attempt_id=j.id WHERE j.source_type='learning_review' AND j.source_id=?`, id).Scan(&input, &output, &cost)
		if e != nil {
			http.Error(w, "读取回顾费用失败", 500)
			return
		}
		data["InputUnits"], data["OutputUnits"] = input, output
		data["Cost"] = "未知"
		if cost != nil {
			data["Cost"] = fmt.Sprintf("%.4f", *cost)
		}
	}
	if err = srv.tmpl.Render(w, "review.html", data); err != nil {
		http.Error(w, "渲染回顾失败", 500)
	}
}
func (srv *Server) reviewActionError(w http.ResponseWriter, r *http.Request, err error) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		http.Error(w, err.Error(), 400)
		return
	}
	w.WriteHeader(400)
	_ = srv.tmpl.Render(w, "review_error.html", map[string]any{"Error": err.Error(), "CSRF": auth.CSRFValue(r), "Item": r.FormValue("item"), "Answer": r.FormValue("answer"), "Expected": r.FormValue("expected_revision"), "Assessment": r.FormValue("assessment"), "Action": r.FormValue("action"), "Timezone": r.FormValue("timezone"), "Weekday": r.FormValue("weekday"), "ClockTime": r.FormValue("clock_time"), "Enabled": r.FormValue("enabled")})
}
func (srv *Server) handleLearningReviewAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", 405)
		return
	}
	ctx := r.Context()
	action := r.FormValue("action")
	target := "/review"
	switch action {
	case "settings":
		day, e := strconv.Atoi(r.FormValue("weekday"))
		if e != nil {
			srv.reviewActionError(w, r, e)
			return
		}
		prefs := store.LearningReviewSettings{Enabled: r.FormValue("enabled") == "on", Timezone: r.FormValue("timezone"), Weekday: day, ClockTime: r.FormValue("clock_time")}
		if prefs.Enabled && !srv.cfg.PodAvailable() {
			srv.reviewActionError(w, r, fmt.Errorf("POD_* 配置不完整"))
			return
		}
		if e = srv.store.SetLearningReviewSettings(ctx, prefs); e != nil {
			srv.reviewActionError(w, r, e)
			return
		}
	case "generate":
		if !srv.cfg.PodAvailable() {
			srv.reviewActionError(w, r, fmt.Errorf("请配置 POD_* 并重启"))
			return
		}
		profile, e := srv.store.EnsureDefaultEditorialProfile(ctx)
		if e != nil {
			srv.reviewActionError(w, r, e)
			return
		}
		batch, _, e := srv.store.ReserveLearningReview(ctx, profile.ID, "pod", srv.cfg.PodModel, time.Now(), false)
		if e != nil {
			srv.reviewActionError(w, r, e)
			return
		}
		if batch == nil {
			srv.reviewActionError(w, r, fmt.Errorf("本周没有可外发的学习材料。先记录笔记或重点"))
			return
		}
		target += "?batch=" + batch.ID
	case "retry":
		id := r.FormValue("batch")
		if e := srv.store.RetryLearningReview(ctx, id); e != nil {
			srv.reviewActionError(w, r, e)
			return
		}
		target += "?batch=" + id
	case "answer", "later", "reveal", "save_note":
		expected, e := strconv.Atoi(r.FormValue("expected_revision"))
		if e != nil {
			srv.reviewActionError(w, r, e)
			return
		}
		id := r.FormValue("item")
		var batchID string
		if e = srv.store.DB.QueryRowContext(ctx, `SELECT batch_id FROM learning_review_items WHERE id=?`, id).Scan(&batchID); e != nil {
			srv.reviewActionError(w, r, e)
			return
		}
		if action == "save_note" {
			_, e = srv.store.SaveLearningReviewNote(ctx, id, expected)
		} else {
			e = srv.store.AnswerLearningReview(ctx, id, r.FormValue("answer"), r.FormValue("assessment"), action, expected)
		}
		if e != nil {
			srv.reviewActionError(w, r, e)
			return
		}
		target += "?batch=" + batchID
	default:
		srv.reviewActionError(w, r, fmt.Errorf("未知回顾操作"))
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"href": target})
		return
	}
	http.Redirect(w, r, target, 303)
}

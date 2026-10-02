package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/provider"
	"io"
	"net/http"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/store"
)

type knowledgeSemanticCommand struct {
	Action           string `json:"action"`
	Query            string `json:"query"`
	ConfigID         string `json:"config_id"`
	RequestKey       string `json:"request_key"`
	JobID            string `json:"job_id"`
	ExpectedRevision int    `json:"expected_revision"`
	AllowUnknown     bool   `json:"allow_unknown"`
}
type knowledgeSemanticJobState struct {
	JobID           string `json:"job_id"`
	Status          string `json:"status"`
	ResultState     string `json:"result_state"`
	Error           string `json:"error"`
	QueryReady      bool   `json:"query_ready"`
	KnownResponse   bool   `json:"known_response"`
	RemoteStarted   bool   `json:"remote_started"`
	ControlRevision int    `json:"control_revision"`
}

func (srv *Server) currentKnowledgeEmbeddingConfig(ctx context.Context, id string) (*store.KnowledgeEmbeddingConfig, error) {
	client, err := srv.selector.Embedding()
	if err != nil {
		return nil, err
	}
	route := client.Config()
	if id == "" {
		configs, err := srv.store.EnabledKnowledgeEmbeddingConfigs(ctx, route)
		if err != nil {
			return nil, err
		}
		if len(configs) != 1 {
			return nil, store.ErrNotFound
		}
		return configs[0], nil
	}
	cfg, err := srv.store.GetKnowledgeEmbeddingConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	if cfg.Profile != route.Profile || cfg.ConnectionID != route.ConnectionID || cfg.Model != route.Model || (route.Dimensions != 0 && route.Dimensions != cfg.Dimensions) {
		return nil, store.ErrConflict
	}
	return cfg, nil
}

func (srv *Server) handleKnowledgeSemantic(w http.ResponseWriter, r *http.Request) {
	var job *models.ProcessingJob
	var err error
	if r.Method == http.MethodGet {
		if key := r.URL.Query().Get("request_key"); key != "" {
			if _, e := uuid.Parse(key); e != nil {
				semanticHTTPError(w, store.ErrInvalidEditorialState)
				return
			}
			var jobID sql.NullString
			if r.URL.Query().Get("request_action") == "query" {
				err = srv.store.DB.QueryRowContext(r.Context(), `SELECT job_id FROM knowledge_query_requests WHERE request_key=?`, key).Scan(&jobID)
			} else if r.URL.Query().Get("request_action") == "preflight" {
				err = srv.store.DB.QueryRowContext(r.Context(), `SELECT job_id FROM knowledge_embedding_requests WHERE request_key=?`, "preflight:"+key).Scan(&jobID)
			} else if r.URL.Query().Get("request_action") == "retry" {
				err = srv.store.DB.QueryRowContext(r.Context(), `SELECT job_id FROM knowledge_embedding_requests WHERE request_key=?`, "retry:"+key).Scan(&jobID)
			} else {
				err = store.ErrInvalidEditorialState
			}
			if errors.Is(err, sql.ErrNoRows) || (err == nil && !jobID.Valid) {
				err = store.ErrNotFound
			}
			if err == nil {
				job, err = srv.store.GetJob(r.Context(), jobID.String)
			}
		} else if id := r.URL.Query().Get("job_id"); id != "" {
			job, err = srv.store.GetJob(r.Context(), id)
		} else {
			job, err = srv.store.FindKnowledgeQueryEmbeddingJob(r.Context(), r.URL.Query().Get("config_id"), r.URL.Query().Get("query"))
		}
	} else if r.Method == http.MethodPost {
		var command knowledgeSemanticCommand
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&command) != nil {
			http.Error(w, "语义搜索请求无效或超过容量", 400)
			return
		}
		var tail any
		if decoder.Decode(&tail) != io.EOF {
			http.Error(w, "只允许一个语义搜索命令", 400)
			return
		}
		switch command.Action {
		case "preflight":
			client, e := srv.selector.Embedding()
			if e != nil {
				semanticHTTPError(w, e)
				return
			}
			job, _, err = srv.store.ReserveKnowledgeEmbeddingPreflight(r.Context(), command.RequestKey, client.Config())
		case "query", "evaluation_query":
			cfg, e := srv.currentKnowledgeEmbeddingConfig(r.Context(), command.ConfigID)
			if e != nil {
				semanticHTTPError(w, e)
				return
			}
			if command.Action == "evaluation_query" && command.ExpectedRevision < 1 {
				semanticHTTPError(w, store.ErrInvalidEditorialState)
				return
			}
			if command.Action == "query" {
				_, reason, e := srv.store.KnowledgeEmbeddingQualityGate(r.Context(), cfg.ID)
				if e != nil {
					semanticHTTPError(w, e)
					return
				}
				if !cfg.SemanticEnabled || reason != "" {
					http.Error(w, "语义检索尚未通过有效质量准入并开启；请继续使用FTS。", 409)
					return
				}
			}
			if command.ExpectedRevision > 0 && command.ExpectedRevision != cfg.Revision {
				semanticHTTPError(w, store.ErrConflict)
				return
			}
			job, _, err = srv.store.ReserveKnowledgeQueryEmbeddingRequest(r.Context(), cfg.ID, command.Query, command.RequestKey)
		case "retry":
			job, _, err = srv.store.RetryKnowledgeEmbeddingJob(r.Context(), command.JobID, command.RequestKey, command.ExpectedRevision, command.AllowUnknown)
		default:
			http.Error(w, "未知语义搜索操作", 400)
			return
		}
	} else {
		w.WriteHeader(405)
		return
	}
	if err != nil {
		semanticHTTPError(w, err)
		return
	}
	if job == nil || job.JobType != models.JobKnowledgeEmbedding {
		http.NotFound(w, r)
		return
	}
	execution, err := srv.store.GetJobExecution(r.Context(), job.ID)
	if err != nil {
		semanticHTTPError(w, err)
		return
	}
	state := knowledgeSemanticJobState{JobID: job.ID, Status: string(job.Status), ResultState: execution.ResultState, Error: runPublicError(automationError(job.LastError)), KnownResponse: execution.CheckpointJSON != "", RemoteStarted: execution.RemoteCallStarted}
	if err = srv.store.DB.QueryRowContext(r.Context(), `SELECT control_revision FROM processing_jobs WHERE id=?`, job.ID).Scan(&state.ControlRevision); err != nil {
		http.Error(w, "读取任务状态失败", 500)
		return
	}
	var in store.KnowledgeEmbeddingJobInput
	if json.Unmarshal([]byte(execution.InputSnapshotJSON), &in) == nil && in.Kind == "query" {
		if _, e := srv.currentKnowledgeEmbeddingConfig(r.Context(), in.Config.ID); e == nil {
			_, e = srv.store.KnowledgeQueryEmbedding(r.Context(), in.Config.ID, in.Query)
			state.QueryReady = e == nil
			if e != nil && !errors.Is(e, store.ErrNotFound) && state.Error == "" {
				state.Error = "语义缓存已失效；本次继续使用FTS。"
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

func semanticHTTPError(w http.ResponseWriter, err error) {
	code := 400
	message := "语义操作未完成，请核对搜索设置与任务记录。"
	switch {
	case errors.Is(err, store.ErrConflict):
		code = 409
		message = "配置、来源范围或请求身份已变化；请核对后重新操作，原查询保留。"
	case errors.Is(err, store.ErrNotFound):
		code = 404
		message = "语义任务、配置或缓存不存在。"
	case errors.Is(err, provider.ErrEmbeddingUnavailable):
		message = "独立语义连接尚未配置，本次可继续使用FTS。"
	case errors.Is(err, store.ErrInvalidEditorialState):
		message = strings.TrimPrefix(runPublicError(err.Error()), store.ErrInvalidEditorialState.Error()+": ")
	}
	http.Error(w, message, code)
}

package server

import (
	"encoding/json"
	"github.com/woyin/orangecast/internal/models"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/woyin/orangecast/internal/auth"
	"github.com/woyin/orangecast/internal/store"
)

// Settings is a read-only projection. A measured connection remains disabled;
// preflight itself requires an explicit persistent command.
func (srv *Server) handleKnowledgeSearchSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		srv.handleEmbeddingSettingsCommand(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		srv.handleEmbeddingSettingsRead(w, r)
		return
	}
	data := map[string]any{"CSRF": auth.CSRFValue(r), "Capacity": store.KnowledgeEmbeddingCapacity, "MaxDimensions": 2048}
	client, err := srv.selector.Embedding()
	if err == nil {
		route := client.Config()
		data["Connection"] = route
		rows, e := srv.store.DB.QueryContext(r.Context(), `SELECT id FROM knowledge_embedding_configs WHERE connection_id=? AND model=? AND profile=? AND (?=0 OR dimensions=?) ORDER BY updated_at DESC LIMIT 8`, route.ConnectionID, route.Model, route.Profile, route.Dimensions, route.Dimensions)
		if e != nil {
			http.Error(w, "读取搜索配置失败", 500)
			return
		}
		var ids []string
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				break
			}
			ids = append(ids, id)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			http.Error(w, "读取搜索配置失败", 500)
			return
		}
		var statuses []*store.EmbeddingIndexStatus
		for _, id := range ids {
			status, e := srv.store.KnowledgeEmbeddingStatus(r.Context(), id)
			if e != nil {
				http.Error(w, "读取索引状态失败", 500)
				return
			}
			statuses = append(statuses, status)
		}
		data["Statuses"] = statuses
		details := make(map[string]embeddingSettingsView)
		for _, status := range statuses {
			id := status.Config.ID
			identity, e := srv.store.KnowledgeEmbeddingQualityIdentity(r.Context(), id)
			if e != nil {
				http.Error(w, "读取评测身份失败", 500)
				return
			}
			report, reason, e := srv.store.KnowledgeEmbeddingQualityGate(r.Context(), id)
			if e != nil {
				http.Error(w, "读取评测报告失败", 500)
				return
			}
			scope, _ := json.Marshal(status.Sources)
			if len(status.Sources) == 0 {
				scope = []byte("[]")
			}
			price, _ := srv.store.GetModelPrice(r.Context(), status.Config.Provider, status.Config.Model)
			templateReport := store.EmbeddingQualityReport{Identity: identity, Fusion: store.KnowledgeFusionVersion, Queries: 40, Samples: 20, PerformanceDimensions: 2048, Windows10k: 10000, Windows50k: 50000}
			templateJSON, _ := json.MarshalIndent(templateReport, "", "  ")
			permissions, e := srv.store.PreviewKnowledgeEmbeddingScope(r.Context(), id, status.Sources)
			if e != nil {
				http.Error(w, "读取来源授权预览失败", 500)
				return
			}
			understandings, e := srv.store.ListEmbeddingUnderstandingChoices(r.Context(), id)
			if e != nil {
				http.Error(w, "读取理解选择失败", 500)
				return
			}
			selected := []string{}
			for _, v := range understandings {
				if v.Selected {
					selected = append(selected, v.SnapshotID)
				}
			}
			selectedJSON, _ := json.Marshal(selected)
			details[id] = embeddingSettingsView{UnderstandingChoices: understandings, UnderstandingsJSON: string(selectedJSON), Permissions: permissions, ReportTemplate: string(templateJSON), Identity: identity, Reason: reason, Report: report, Price: price, SourcesJSON: string(scope)}
		}
		data["Details"] = details
		sources, e := srv.store.SearchKnowledgeSources(r.Context(), store.KnowledgeListQuery{Text: r.URL.Query().Get("source_query"), PerPage: 100}, "")
		if e != nil {
			http.Error(w, "读取来源失败", 500)
			return
		}
		data["AvailableSources"] = sources.Items
	}
	if err = srv.tmpl.Render(w, "knowledge_search_settings.html", data); err != nil {
		http.Error(w, "渲染搜索设置失败", 500)
	}
}

// embeddingSettingsView contains no route endpoint, credential, or source body.
type embeddingSettingsView struct {
	UnderstandingChoices                          []store.EmbeddingUnderstandingChoice
	UnderstandingsJSON                            string
	Permissions                                   []store.EmbeddingScopePreview
	Identity, Reason, SourcesJSON, ReportTemplate string
	Report                                        *store.EmbeddingQualityReport
	Price                                         *models.ModelPrice
}

func (srv *Server) handleEmbeddingSettingsCommand(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	var command struct {
		Action string `json:"action"`
		store.EmbeddingSettingsCommand
		Enabled bool                         `json:"enabled"`
		Report  store.EmbeddingQualityReport `json:"report"`
	}
	isJSON := strings.Contains(r.Header.Get("Content-Type"), "application/json")
	if isJSON {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if dec.Decode(&command) != nil {
			http.Error(w, "设置命令无效", 400)
			return
		}
		var tail any
		if dec.Decode(&tail) != io.EOF {
			http.Error(w, "只允许一个设置命令", 400)
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "设置表单无效", 400)
			return
		}
		command.Action = r.FormValue("action")
		command.ConfigID = r.FormValue("config_id")
		command.ExpectedRevision, _ = strconv.Atoi(r.FormValue("expected_revision"))
		command.WindowCapacity, _ = strconv.Atoi(r.FormValue("window_capacity"))
		command.IndexAuthorized = r.FormValue("index_authorized") == "1"
		command.Enabled = r.FormValue("enabled") == "1"
		if price := strings.TrimSpace(r.FormValue("input_cents_per_million")); price != "" {
			n, e := strconv.ParseInt(price, 10, 64)
			if e != nil {
				http.Error(w, "输入价格无效", 400)
				return
			}
			command.InputCentsPerMillion = &n
		}
		if command.Action == "settings" || command.Action == "preview" {
			if json.Unmarshal([]byte(r.FormValue("sources")), &command.Sources) != nil {
				http.Error(w, "来源范围格式无效", 400)
				return
			}
		}
		if command.Action == "settings" {
			raw := r.FormValue("understanding_snapshot_ids")
			if raw == "" {
				raw = "[]"
			}
			if json.Unmarshal([]byte(raw), &command.UnderstandingSnapshotIDs) != nil {
				http.Error(w, "理解范围格式无效", 400)
				return
			}
		}
		if command.Action == "report" {
			dec := json.NewDecoder(strings.NewReader(r.FormValue("report")))
			dec.DisallowUnknownFields()
			if dec.Decode(&command.Report) != nil {
				http.Error(w, "质量报告格式无效", 400)
				return
			}
			var tail any
			if dec.Decode(&tail) != io.EOF {
				http.Error(w, "只允许一份质量报告", 400)
				return
			}
		}
	}
	// The current server route must match the measured safe config before mutation.
	if _, err := srv.currentKnowledgeEmbeddingConfig(r.Context(), command.ConfigID); err != nil {
		semanticHTTPError(w, err)
		return
	}
	var result any
	var err error
	switch command.Action {
	case "preview":
		result, err = srv.store.PreviewKnowledgeEmbeddingScope(r.Context(), command.ConfigID, command.Sources)
	case "settings":
		result, err = srv.store.UpdateKnowledgeEmbeddingSettings(r.Context(), command.EmbeddingSettingsCommand)
	case "report":
		result, err = srv.store.SaveKnowledgeEmbeddingQualityReport(r.Context(), command.ConfigID, command.Report)
	case "activate":
		err = srv.store.SetKnowledgeSemanticEnabled(r.Context(), command.ConfigID, command.ExpectedRevision, command.Enabled)
		result = map[string]bool{"enabled": command.Enabled}
	default:
		http.Error(w, "未知设置操作", 400)
		return
	}
	if err != nil {
		semanticHTTPError(w, err)
		return
	}
	if !isJSON && command.Action == "preview" {
		sourcesJSON, _ := json.Marshal(command.Sources)
		understandingsJSON, _ := json.Marshal(command.UnderstandingSnapshotIDs)
		if len(command.Sources) == 0 {
			sourcesJSON = []byte("[]")
		}
		if len(command.UnderstandingSnapshotIDs) == 0 {
			understandingsJSON = []byte("[]")
		}
		data := map[string]any{"CSRF": auth.CSRFValue(r), "Preview": result, "Command": command.EmbeddingSettingsCommand, "SourcesJSON": string(sourcesJSON), "UnderstandingsJSON": string(understandingsJSON)}
		if err = srv.tmpl.Render(w, "knowledge_embedding_scope_preview.html", data); err != nil {
			http.Error(w, "渲染范围预览失败", 500)
		}
		return
	}
	if !isJSON {
		http.Redirect(w, r, "/search/settings", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// Explicit read-only evaluation projects already-paid vectors; GET never prepares them.
func (srv *Server) handleEmbeddingSettingsRead(w http.ResponseWriter, r *http.Request) {
	cfg, err := srv.currentKnowledgeEmbeddingConfig(r.Context(), r.URL.Query().Get("config_id"))
	if err != nil {
		semanticHTTPError(w, err)
		return
	}
	identity, err := srv.store.KnowledgeEmbeddingQualityIdentity(r.Context(), cfg.ID)
	if err != nil {
		semanticHTTPError(w, err)
		return
	}
	var result any
	if r.URL.Query().Get("action") == "evaluate" {
		query := store.KnowledgeSearchQuery{Text: r.URL.Query().Get("q"), PerPage: 10, Recall: true}
		lexical, e := srv.store.Retrieve(r.Context(), store.KnowledgeRetrieveQuery{Search: query})
		if e != nil {
			semanticHTTPError(w, e)
			return
		}
		hybrid, e := srv.store.EvaluateKnowledgeRetrieval(r.Context(), store.KnowledgeRetrieveQuery{Search: query, Semantic: true, EmbeddingConfigID: cfg.ID})
		if e != nil {
			semanticHTTPError(w, e)
			return
		}
		after, e := srv.store.KnowledgeEmbeddingQualityIdentity(r.Context(), cfg.ID)
		if e != nil {
			semanticHTTPError(w, e)
			return
		}
		if after != identity {
			semanticHTTPError(w, store.ErrConflict)
			return
		}
		result = map[string]any{"identity": identity, "config": cfg, "lexical": lexical, "hybrid": hybrid, "read_only": true}
	} else {
		report, reason, e := srv.store.KnowledgeEmbeddingQualityGate(r.Context(), cfg.ID)
		if e != nil {
			semanticHTTPError(w, e)
			return
		}
		result = map[string]any{"identity": identity, "config": cfg, "report": report, "reason": reason, "effective_semantic_enabled": cfg.Enabled && cfg.SemanticEnabled && reason == ""}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

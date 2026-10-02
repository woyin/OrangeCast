package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/woyin/orangecast/internal/learningbundle"
)

// LearningExportScope selects Owner-readable results, without granting model access.
type LearningExportScope struct {
	PublicURL                                      string
	Kind, ID                                       string
	IncludeHistory, IncludeDrafts, IncludeExcerpts bool
}

// LearningExportPreview binds one 24-hour scope/content/state hash and the exact rendered size; it does not grant model access.
type LearningExportPreview struct {
	ID, Hash     string
	Count, Bytes int
	Scope        LearningExportScope
	ExpiresAt    string
}

// LearningExport tracks a zero-model assembly job. Path is private and omitted from JSON; status reflects expired or missing files.
type LearningExport struct {
	ID, JobID, Status, ExpiresAt, Error string
	Path                                string `json:"-"`
	Preview                             LearningExportPreview
}

// PreviewLearningExport copies a bounded consistent snapshot; rendering runs after commit.
func (s *Store) PreviewLearningExport(ctx context.Context, scope LearningExportScope) (*LearningExportPreview, error) {
	if (scope.Kind != "question" && scope.Kind != "theme") || scope.ID == "" {
		return nil, ErrInvalidEditorialState
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	snap, e := exportSnapshot(ctx, tx, scope)
	if e != nil {
		return nil, e
	}
	raw, e := json.Marshal(snap)
	if e != nil {
		return nil, e
	}
	if int64(len(raw)) > learningbundle.MaxBytes {
		return nil, ErrInvalidEditorialState
	}
	var seq int64
	if e = tx.QueryRowContext(ctx, "SELECT seq FROM learning_export_state WHERE singleton=1").Scan(&seq); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	_, manifest, e := learningbundle.Render(snap)
	if e != nil {
		return nil, ErrInvalidEditorialState
	}
	tx, e = s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var current int64
	if e = tx.QueryRowContext(ctx, "SELECT seq FROM learning_export_state WHERE singleton=1").Scan(&current); e != nil {
		return nil, e
	}
	if current != seq {
		return nil, ErrConflict
	}
	scopeRaw, _ := json.Marshal(scope)
	hash := fmt.Sprintf("%x", sha256.Sum256(append(append(scopeRaw, raw...), []byte(fmt.Sprint(seq))...)))
	p := &LearningExportPreview{ID: uuid.NewString(), Hash: hash, Count: len(snap.Objects), Bytes: int(manifest.TotalBytes), Scope: scope, ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)}
	var count int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM learning_export_previews WHERE expires_at>?", time.Now().UTC().Format(time.RFC3339)).Scan(&count); e != nil {
		return nil, e
	}
	if count >= 100 {
		return nil, ErrInvalidEditorialState
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO learning_export_previews(id,hash,scope_json,snapshot_json,state_seq,count,bytes,expires_at)VALUES(?,?,?,?,?,?,?,?)", p.ID, p.Hash, string(scopeRaw), string(raw), seq, p.Count, p.Bytes, p.ExpiresAt)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return p, nil
}

func exportSnapshot(ctx context.Context, tx *sql.Tx, scope LearningExportScope) (learningbundle.Snapshot, error) {
	snap := learningbundle.Snapshot{ScopeKind: scope.Kind, ScopeID: scope.ID, Objects: []learningbundle.Object{}}
	var copiedBytes int64
	// Every query reads at most the object bound plus one and refuses oversized values before copying.
	add := func(query string, args ...any) error {
		rows, e := tx.QueryContext(ctx, query, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var o learningbundle.Object
			var size int
			if e = rows.Scan(&o.Kind, &o.ID, &o.Revision, &o.Title, &o.Body, &o.Status, &size); e != nil {
				return e
			}
			copiedBytes += int64(size) + int64(len(o.Title))
			if copiedBytes > learningbundle.MaxBytes || len(snap.Objects) >= learningbundle.MaxObjects {
				return ErrInvalidEditorialState
			}
			snap.Objects = append(snap.Objects, o)
		}
		return rows.Err()
	}
	var found int
	table := "learning_questions"
	if scope.Kind == "theme" {
		table = "themes"
	}
	if e := tx.QueryRowContext(ctx, "SELECT 1 FROM "+table+" WHERE id=?", scope.ID).Scan(&found); errors.Is(e, sql.ErrNoRows) {
		return snap, ErrNotFound
	} else if e != nil {
		return snap, e
	}
	qscope := `SELECT id FROM learning_questions WHERE id=?`
	if scope.Kind == "theme" {
		qscope = `SELECT id FROM learning_questions WHERE theme_id=?`
	}
	if e := add(`SELECT 'questions',id,revision,body,CASE WHEN length(body||goal)<=104857600 THEN body||char(10)||char(10)||goal ELSE '' END,status,length(body||goal) FROM learning_questions WHERE id IN (`+qscope+`) ORDER BY id LIMIT 501`, scope.ID); e != nil {
		return snap, e
	}
	linkscope := `SELECT kind,object_id,source_type,source_id FROM learning_question_links WHERE state='confirmed' AND question_id IN (` + qscope + `)`
	if e := add(`SELECT 'understandings',u.id,u.version,'我的理解',CASE WHEN length(answer||uncertainty||next_step)<=104857600 THEN answer||char(10)||'尚不确定：'||uncertainty||char(10)||'下一步：'||next_step ELSE '' END,CASE WHEN h.current_snapshot_id=u.id THEN 'current' ELSE 'history' END,length(answer||uncertainty||next_step) FROM understanding_snapshots u LEFT JOIN understanding_heads h ON h.question_id=u.question_id WHERE u.question_id IN (`+qscope+`) AND (? OR h.current_snapshot_id=u.id) ORDER BY u.id LIMIT 501`, scope.ID, scope.IncludeHistory); e != nil {
		return snap, e
	}
	if e := add(`SELECT 'notes',n.note_id,n.revision,n.kind,CASE WHEN length(n.content)<=104857600 THEN n.content ELSE '' END,CASE WHEN n.revision=o.revision THEN 'current' ELSE 'history' END,length(n.content) FROM owner_note_revisions n JOIN owner_notes o ON o.id=n.note_id WHERE n.note_id IN(SELECT object_id FROM (`+linkscope+`) WHERE kind='note') AND (? OR n.revision=o.revision) ORDER BY n.note_id,n.revision LIMIT 501`, scope.ID, scope.IncludeHistory); e != nil {
		return snap, e
	}
	keyscope := `SELECT object_id FROM (` + linkscope + `) WHERE kind='keypoint'`
	keyargs := []any{scope.ID}
	if scope.Kind == "theme" {
		keyscope += ` UNION SELECT keypoint_id FROM theme_keypoints WHERE theme_id=?`
		keyargs = append(keyargs, scope.ID)
	}
	if e := add(`SELECT 'keypoints',id,card_version,source_title,CASE WHEN length(content||description)<=104857600 THEN content||char(10)||description ELSE '' END,'current',length(content||description) FROM keypoint_index WHERE id IN (`+keyscope+`) AND evidence_status!='stale' AND production_status!='dismissed' ORDER BY id LIMIT 501`, keyargs...); e != nil {
		return snap, e
	}
	articlescope := `SELECT object_id FROM (` + linkscope + `) WHERE kind='article'`
	articleArgs := []any{scope.ID}
	if scope.Kind == "theme" {
		articlescope += ` UNION SELECT article_id FROM knowledge_article_material_refs WHERE material_id IN(SELECT keypoint_id FROM theme_keypoints WHERE theme_id=?)`
		articleArgs = append(articleArgs, scope.ID)
	}
	if e := add(`SELECT 'articles',r.article_id,r.revision,r.title,CASE WHEN length(r.blocks_json)<=104857600 THEN r.blocks_json ELSE '' END,CASE WHEN a.passed_revision=r.revision THEN 'passed' WHEN a.working_revision=r.revision THEN 'draft' ELSE 'history_passed' END,length(r.blocks_json) FROM knowledge_article_revisions r JOIN knowledge_articles a ON a.id=r.article_id WHERE a.id IN (`+articlescope+`) AND r.evidence_status='valid' AND (a.passed_revision=r.revision OR (? AND EXISTS(SELECT 1 FROM knowledge_article_reviews v WHERE v.article_id=r.article_id AND v.revision=r.revision AND v.passed=1)) OR (? AND a.working_revision=r.revision)) ORDER BY r.article_id,r.revision LIMIT 501`, append(append([]any{}, articleArgs...), scope.IncludeHistory, scope.IncludeDrafts)...); e != nil {
		return snap, e
	}
	for i := range snap.Objects {
		if snap.Objects[i].Kind == "articles" {
			var blocks []struct {
				Text string `json:"text"`
				Kind string `json:"kind"`
			}
			if e := json.Unmarshal([]byte(snap.Objects[i].Body), &blocks); e != nil {
				return snap, e
			}
			var body []string
			for _, b := range blocks {
				body = append(body, "材料类别："+b.Kind+"\n\n"+b.Text)
			}
			snap.Objects[i].Body = strings.Join(body, "\n\n")
		}
	}
	if e := add(`SELECT 'cases',c.id,c.version,'质量案例 · 文章修订 '||f.revision,CASE WHEN length(c.expected||c.classification||c.blocks_json)<=104857600 THEN json_object('expected',c.expected,'classification',c.classification,'blocks',json(c.blocks_json)) ELSE '' END,c.state,length(c.expected||c.classification||c.blocks_json) FROM article_quality_cases c JOIN article_quality_feedback f ON f.id=c.feedback_id JOIN knowledge_articles a ON a.id=f.article_id WHERE c.state='accepted' AND a.id IN (`+articlescope+`) AND (? OR a.passed_revision=f.revision) ORDER BY c.id LIMIT 501`, append(append([]any{}, articleArgs...), scope.IncludeHistory)...); e != nil {
		return snap, e
	}
	for i := range snap.Objects {
		if snap.Objects[i].Kind == "cases" {
			var c struct {
				Expected, Classification string
				Blocks                   []struct {
					Text string `json:"text"`
					Kind string `json:"kind"`
				}
			}
			if e := json.Unmarshal([]byte(snap.Objects[i].Body), &c); e != nil {
				return snap, e
			}
			body := "预期：" + c.Expected + "\n\n分类：" + c.Classification
			for _, b := range c.Blocks {
				body += "\n\n材料类别：" + b.Kind + "\n\n" + b.Text
			}
			snap.Objects[i].Body = body
		}
	}
	// Only confirmed source links and exact article reference sources are copied. No recordings/configuration.
	sourcescope := `SELECT source_type,source_id FROM (` + linkscope + `) WHERE source_id!='' UNION SELECT r.source_type,r.source_id FROM knowledge_article_material_refs r JOIN knowledge_articles a ON a.id=r.article_id JOIN knowledge_article_revisions rv ON rv.article_id=r.article_id AND rv.revision=r.revision WHERE r.article_id IN (` + articlescope + `) AND rv.evidence_status='valid' AND (a.passed_revision=r.revision OR (? AND EXISTS(SELECT 1 FROM knowledge_article_reviews v WHERE v.article_id=r.article_id AND v.revision=r.revision AND v.passed=1)) OR (? AND a.working_revision=r.revision)) UNION SELECT ur.source_type,ur.source_id FROM understanding_references ur JOIN understanding_snapshots us ON us.id=ur.snapshot_id LEFT JOIN understanding_heads h ON h.question_id=us.question_id WHERE ur.purged=0 AND us.question_id IN (` + qscope + `) AND (? OR h.current_snapshot_id=us.id) UNION SELECT k.source_type,k.source_id FROM keypoint_index k WHERE k.id IN (` + keyscope + `)`

	for _, src := range []struct{ kind, table, title, body string }{{"episode", "episodes", "title", "description"}, {"upload", "uploads", "original_filename", "original_filename"}, {"document", "documents", "title", "content"}} {
		versionExpr := "version"
		if src.kind != "document" {
			versionExpr = "COALESCE(current_transcript_version,0)"
			src.body = "(SELECT json_extract(a.payload,'$.segments') FROM artifact_versions a WHERE a.source_type='" + src.kind + "' AND a.source_id=" + src.table + ".id AND a.kind='transcript' AND a.version=" + src.table + ".current_transcript_version)"
		}
		query := `SELECT 'sources',?||'-'||id,` + versionExpr + `,` + src.title + `,CASE WHEN ? AND length(COALESCE(` + src.body + `,''))<=104857600 THEN COALESCE(` + src.body + `,'') ELSE '原文未包含' END,'current',CASE WHEN ? THEN length(COALESCE(` + src.body + `,'')) ELSE 0 END FROM ` + src.table + ` WHERE id IN(SELECT source_id FROM (` + sourcescope + `) WHERE source_type=?) AND NOT EXISTS(SELECT 1 FROM source_snapshots ss WHERE ss.source_type=? AND ss.source_id=` + src.table + `.id AND ss.status='purged') ORDER BY id LIMIT 501`

		sourceArgs := []any{src.kind, scope.IncludeExcerpts, scope.IncludeExcerpts, scope.ID}
		sourceArgs = append(sourceArgs, articleArgs...)
		sourceArgs = append(sourceArgs, scope.IncludeHistory, scope.IncludeDrafts, scope.ID, scope.IncludeHistory)
		sourceArgs = append(sourceArgs, keyargs...)
		sourceArgs = append(sourceArgs, src.kind, src.kind)
		if e := add(query, sourceArgs...); e != nil {
			return snap, e
		}
	}

	for i := range snap.Objects {
		o := &snap.Objects[i]
		if o.Kind == "sources" && scope.IncludeExcerpts && (strings.HasPrefix(o.ID, "episode-") || strings.HasPrefix(o.ID, "upload-")) {
			if o.Body == "" {
				o.Body = "转录原文缺失"
				continue
			}
			var segments []struct {
				ID         string `json:"id"`
				Text       string `json:"text"`
				Start, End float64
			}
			if e := json.Unmarshal([]byte(o.Body), &segments); e != nil {
				return snap, e
			}
			var body []string
			for _, seg := range segments {
				body = append(body, fmt.Sprintf("[%s %g–%g 秒] %s", seg.ID, seg.Start, seg.End, seg.Text))
			}
			o.Body = strings.Join(body, "\n\n")
		}
	}
	paths := map[string]string{}
	for _, o := range snap.Objects {
		p, e := learningbundle.ObjectPath(o)
		if e != nil {
			return snap, ErrInvalidEditorialState
		}
		paths[o.Kind+":"+o.ID] = p
	}
	for i := range snap.Objects {
		o := &snap.Objects[i]
		var rows *sql.Rows
		var e error
		switch o.Kind {
		case "notes":
			rows, e = tx.QueryContext(ctx, `SELECT source_type,source_id,COALESCE(json_extract(anchor_json,'$.position'),0) FROM owner_note_revisions WHERE note_id=? AND revision=?`, o.ID, o.Revision)
		case "keypoints":
			rows, e = tx.QueryContext(ctx, `SELECT source_type,source_id,time_start FROM keypoint_index WHERE id=?`, o.ID)
		case "articles":
			rows, e = tx.QueryContext(ctx, `SELECT source_type,source_id,0 FROM knowledge_article_material_refs WHERE article_id=? AND revision=? ORDER BY material_id LIMIT 501`, o.ID, o.Revision)
		case "understandings":
			rows, e = tx.QueryContext(ctx, `SELECT source_type,source_id,0 FROM understanding_references WHERE snapshot_id=? AND purged=0 ORDER BY ordinal LIMIT 501`, o.ID)
		case "questions":
			for _, target := range snap.Objects {
				if target.Kind != "questions" {
					p, _ := learningbundle.ObjectPath(target)
					o.Links = append(o.Links, learningbundle.Link{Label: target.Title, Path: p})
				}
			}
			continue
		default:
			continue
		}
		if e != nil {
			return snap, e
		}
		count := 0
		for rows.Next() {
			var kind, id string
			var position float64
			if e = rows.Scan(&kind, &id, &position); e != nil {
				rows.Close()
				return snap, e
			}
			count++
			if count > 500 {
				rows.Close()
				return snap, ErrInvalidEditorialState
			}
			if p := paths["sources:"+kind+"-"+id]; p != "" {
				o.Links = append(o.Links, learningbundle.Link{Label: fmt.Sprintf("来源 %s:%s 定位 %g", kind, id, position), Path: p})
				if scope.PublicURL != "" {
					base := strings.TrimRight(scope.PublicURL, "/")
					path := "/episodes/"
					if kind == "upload" {
						path = "/uploads/"
					}
					if kind == "document" {
						path = "/documents/"
					}
					target := base + path + url.PathEscape(id)
					if kind == "document" {
						target += "#" + url.PathEscape(id) + fmt.Sprintf("-p%04d", int(position))
					} else {
						target += fmt.Sprintf("?t=%g", position)
					}
					o.Links = append(o.Links, learningbundle.Link{Label: "在原站查看确切定位", URL: target})
				}
			} else if id != "" {
				o.Body += "\n\n缺失来源：" + kind + ":" + id
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return snap, e
		}
	}

	for i := range snap.Objects {
		o := &snap.Objects[i]
		if o.Kind != "articles" {
			continue
		}
		rows, e := tx.QueryContext(ctx, `SELECT kind,material_id,material_version,source_type,source_id,snapshot_id FROM knowledge_article_material_refs WHERE article_id=? AND revision=? ORDER BY material_id LIMIT 501`, o.ID, o.Revision)
		if e != nil {
			return snap, e
		}
		n := 0
		for rows.Next() {
			var kind, id, sourceType, sourceID, snapshot string
			var version int
			if e = rows.Scan(&kind, &id, &version, &sourceType, &sourceID, &snapshot); e != nil {
				rows.Close()
				return snap, e
			}
			n++
			if n > 500 {
				rows.Close()
				return snap, ErrInvalidEditorialState
			}
			o.Body += fmt.Sprintf("\n\n引用材料：%s %s · 材料版本 %d · 来源 %s:%s · 快照 %s", kind, id, version, sourceType, sourceID, snapshot)
			category := "notes"
			if kind == "keypoint" {
				category = "keypoints"
			}
			if kind == "understanding" {
				category = "understandings"
			}
			target, e := learningbundle.ObjectPath(learningbundle.Object{Kind: category, ID: id, Revision: version})
			if e == nil {
				for _, candidate := range snap.Objects {
					p, _ := learningbundle.ObjectPath(candidate)
					if p == target {
						o.Links = append(o.Links, learningbundle.Link{Label: fmt.Sprintf("确切引用 %s v%d", id, version), Path: p})
						break
					}
				}
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return snap, e
		}
	}
	return snap, nil
}

func exportPreview(ctx context.Context, q reviewReader, id string) (*LearningExportPreview, string, int64, error) {
	p := &LearningExportPreview{ID: id}
	var scope, raw string
	var seq int64
	e := q.QueryRowContext(ctx, `SELECT hash,scope_json,snapshot_json,state_seq,count,bytes,expires_at FROM learning_export_previews WHERE id=?`, id).Scan(&p.Hash, &scope, &raw, &seq, &p.Count, &p.Bytes, &p.ExpiresAt)
	if errors.Is(e, sql.ErrNoRows) {
		e = ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal([]byte(scope), &p.Scope)
	}
	return p, raw, seq, e
}
func checkExportPreview(ctx context.Context, q reviewReader, p *LearningExportPreview, seq int64) error {
	var current int64
	if e := q.QueryRowContext(ctx, `SELECT seq FROM learning_export_state WHERE singleton=1`).Scan(&current); e != nil {
		return e
	}
	if current != seq || p.ExpiresAt <= time.Now().UTC().Format(time.RFC3339) {
		return ErrConflict
	}
	return nil
}

// CreateLearningExport atomically rechecks snapshot identity and enqueues a zero-model job.
func (s *Store) CreateLearningExport(ctx context.Context, previewID, hash, request, userID string) (*LearningExport, error) {
	if _, e := uuid.Parse(request); e != nil || userID == "" {
		return nil, ErrInvalidEditorialState
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var existing, oldPreview string
	e = tx.QueryRowContext(ctx, `SELECT id,preview_id FROM learning_exports WHERE request_key=? AND user_id=?`, request, userID).Scan(&existing, &oldPreview)
	if e == nil {
		p, _, _, e := exportPreview(ctx, tx, oldPreview)
		if e != nil {
			return nil, e
		}
		if oldPreview != previewID || p.Hash != hash {
			return nil, ErrConflict
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
		return s.GetLearningExport(ctx, existing)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	p, _, seq, e := exportPreview(ctx, tx, previewID)
	if e != nil {
		return nil, e
	}
	if p.Hash != hash {
		return nil, ErrConflict
	}
	if e = checkExportPreview(ctx, tx, p, seq); e != nil {
		return nil, e
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM learning_exports WHERE user_id=? AND expires_at>?`, userID, time.Now().UTC().Format(time.RFC3339)).Scan(&count); e != nil {
		return nil, e
	}
	if count >= 100 {
		return nil, ErrInvalidEditorialState
	}
	id, job := uuid.NewString(), uuid.NewString()
	_, e = tx.ExecContext(ctx, `INSERT INTO processing_jobs(id,source_type,source_id,job_type,status,input_snapshot_json)VALUES(?,'learning_export',?,'learning_export','queued','{}')`, job, id)
	if e != nil {
		return nil, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO learning_exports(id,preview_id,job_id,request_key,user_id,expires_at)VALUES(?,?,?,?,?,?)`, id, previewID, job, request, userID, p.ExpiresAt)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.GetLearningExport(ctx, id)
}

// GetLearningExport returns metadata and frozen preview identity, never material bodies; expired or missing private files are not reported ready.
func (s *Store) GetLearningExport(ctx context.Context, id string) (*LearningExport, error) {
	v := &LearningExport{ID: id}
	var preview string
	e := s.DB.QueryRowContext(ctx, `SELECT preview_id,job_id,status,path,expires_at,error FROM learning_exports WHERE id=?`, id).Scan(&preview, &v.JobID, &v.Status, &v.Path, &v.ExpiresAt, &v.Error)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	p, _, _, e := exportPreview(ctx, s.DB, preview)
	if e != nil {
		return nil, e
	}
	v.Preview = *p
	if v.ExpiresAt <= time.Now().UTC().Format(time.RFC3339) {
		v.Status = "expired"
	} else if v.Status == "ready" {
		info, e := os.Lstat(v.Path)
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			v.Status = "failed"
			v.Error = "临时文件缺失或权限不正确，请重新生成"
		}
	}
	return v, nil
}

// ListLearningExports returns the newest 100 metadata records without loading source content or issuing model calls.
func (s *Store) ListLearningExports(ctx context.Context) ([]LearningExport, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT id FROM learning_exports ORDER BY created_at DESC,id LIMIT 100`)
	if e != nil {
		return nil, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []LearningExport{}
	for _, id := range ids {
		v, e := s.GetLearningExport(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, nil
}

// LearningExportSnapshot revalidates frozen content without Provider/configuration access.
func (s *Store) LearningExportSnapshot(ctx context.Context, id string) (learningbundle.Snapshot, error) {
	var snap learningbundle.Snapshot
	v, e := s.GetLearningExport(ctx, id)
	if e != nil {
		return snap, e
	}
	p, raw, seq, e := exportPreview(ctx, s.DB, v.Preview.ID)
	if e != nil {
		return snap, e
	}
	if e = checkExportPreview(ctx, s.DB, p, seq); e != nil {
		return snap, e
	}
	e = json.Unmarshal([]byte(raw), &snap)
	return snap, e
}

// PublishLearningExport competes transactionally with withdrawal and Owner stop.
func (s *Store) PublishLearningExport(ctx context.Context, id, path string) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var preview, job string
	if e = tx.QueryRowContext(ctx, `SELECT preview_id,job_id FROM learning_exports WHERE id=?`, id).Scan(&preview, &job); e != nil {
		return e
	}
	if e = checkRunControl(ctx, tx, job); e != nil {
		return e
	}
	p, _, seq, e := exportPreview(ctx, tx, preview)
	if e != nil {
		return e
	}
	if e = checkExportPreview(ctx, tx, p, seq); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE learning_exports SET status='ready',path=?,error='' WHERE id=?`, path, id)
	if e != nil {
		return e
	}
	return tx.Commit()
}

// FailLearningExport preserves a failed local assembly as an actionable record, without retrying paid work or publishing a partial file.
func (s *Store) FailLearningExport(ctx context.Context, id string, cause error) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE learning_exports SET status='failed',error=? WHERE id=?`, cause.Error(), id)
	return e
}

// ValidLearningExportDownload requires ready status, an unexpired unchanged source sequence and a regular 0600 file; withdrawal returns ErrConflict and missing files ErrNotFound.
func (s *Store) ValidLearningExportDownload(ctx context.Context, id string) (*LearningExport, error) {
	v, e := s.GetLearningExport(ctx, id)
	if e != nil {
		return nil, e
	}
	if v.Status == "failed" {
		return nil, ErrNotFound
	}
	if v.Status != "ready" {
		return nil, ErrConflict
	}
	if _, e = s.LearningExportSnapshot(ctx, id); e != nil {
		return nil, e
	}
	info, e := os.Lstat(v.Path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return nil, ErrNotFound
	}
	return v, nil
}

// CleanupLearningExports removes expired or invalid bundles and their sensitive snapshots.
func (s *Store) CleanupLearningExports(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	for {
		rows, e := s.DB.QueryContext(ctx, `SELECT id,path FROM learning_exports WHERE expires_at<=? AND status!='expired' ORDER BY id LIMIT 100`, now)
		if e != nil {
			return e
		}
		type expired struct{ id, path string }
		var batch []expired
		for rows.Next() {
			var v expired
			if e = rows.Scan(&v.id, &v.path); e != nil {
				rows.Close()
				return e
			}
			batch = append(batch, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, v := range batch {
			if v.path != "" {
				if e = os.Remove(v.path); e != nil && !os.IsNotExist(e) {
					return e
				}
			}
			if _, e = s.DB.ExecContext(ctx, `UPDATE learning_exports SET status='expired',path='' WHERE id=?`, v.id); e != nil {
				return e
			}
		}
		if len(batch) < 100 {
			break
		}
	}
	_, e := s.DB.ExecContext(ctx, `UPDATE learning_export_previews SET snapshot_json='{}' WHERE expires_at<=?`, now)
	return e
}

// GetLearningExportPreview reads the exact existing metadata for an authenticated
// confirmation page; it creates no preview/job and rejects expiry or source changes.
func (s *Store) GetLearningExportPreview(ctx context.Context, id string) (*LearningExportPreview, error) {
	p, _, seq, e := exportPreview(ctx, s.DB, id)
	if e != nil {
		return nil, e
	}
	if e = checkExportPreview(ctx, s.DB, p, seq); e != nil {
		return nil, e
	}
	return p, nil
}

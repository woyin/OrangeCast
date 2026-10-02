package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// EvidenceGap preserves the provenance of a bounded material limitation. It is
// never a diagnosis of the entire library or evidence of learning mastery.
type EvidenceGap struct {
	ID, ParentKind, ParentID, ParentHash, Kind, Origin, Explanation, Coverage, State string
	ParentRevision, Revision                                                         int
	CreatedAt, UpdatedAt                                                             string
}

// EvidenceGapOperation records an explicit judgment without rewriting earlier actions.
type EvidenceGapOperation struct {
	Revision                  int
	State, Comment, CreatedAt string
}

// EvidenceGapCandidate exposes a local match and its actual evidence availability.
type EvidenceGapCandidate struct {
	KnowledgeSearchHit
	AlreadyLinked, RequiresProcessing, Playable bool
	Start, End                                  float64
	Href, Limitation                            string
}

// EvidenceGapCandidates retains bounded coverage and retrieval degradation.
type EvidenceGapCandidates struct {
	Candidates                    []EvidenceGapCandidate
	Method, Coverage, Degradation string
	Total                         int
}

const gapColumns = `id,parent_kind,parent_id,parent_revision,parent_hash,kind,origin,explanation,coverage,state,revision,created_at,updated_at`

func scanEvidenceGap(row interface{ Scan(...any) error }) (EvidenceGap, error) {
	var g EvidenceGap
	err := row.Scan(&g.ID, &g.ParentKind, &g.ParentID, &g.ParentRevision, &g.ParentHash, &g.Kind, &g.Origin, &g.Explanation, &g.Coverage, &g.State, &g.Revision, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return g, err
}
func gapDigest(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }

// gapParent reads the authoritative revision and limited persisted Missing
// fields. Unknown causes are deliberately not inferred from empty results.
type evidenceGapReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *Store) gapParent(ctx context.Context, kind, id string) (int, string, []string, error) {
	return gapParentRead(ctx, s.DB, kind, id)
}
func gapParentRead(ctx context.Context, reader evidenceGapReader, kind, id string) (int, string, []string, error) {
	var revision int
	var raw, scope, dependencyInput string
	var err error
	switch kind {
	case "question":
		var body, goal string
		err = reader.QueryRowContext(ctx, `SELECT revision,body,goal FROM learning_questions WHERE id=?`, id).Scan(&revision, &body, &goal)
		if err == nil {
			rows, e := reader.QueryContext(ctx, `SELECT kind,object_id,version,state FROM learning_question_links WHERE question_id=? ORDER BY kind,object_id`, id)
			if e != nil {
				return 0, "", nil, e
			}
			defer rows.Close()
			scope = body + "\x00" + goal
			for rows.Next() {
				var k, o, st string
				var v int
				if e = rows.Scan(&k, &o, &v, &st); e != nil {
					return 0, "", nil, e
				}
				scope += fmt.Sprintf("|%s:%s:%d:%s", k, o, v, st)
			}
			if e = rows.Err(); e != nil {
				return 0, "", nil, e
			}
		}
		if err == nil {
			rows, e := reader.QueryContext(ctx, `SELECT t.id,t.state FROM question_study_turns t JOIN question_study_sessions se ON se.id=t.session_id WHERE se.question_id=? AND t.state='insufficient' AND t.purged=0 ORDER BY t.id LIMIT 20`, id)
			if e != nil {
				return 0, "", nil, e
			}
			for rows.Next() {
				var turn, state string
				if e = rows.Scan(&turn, &state); e != nil {
					rows.Close()
					return 0, "", nil, e
				}
				scope += "|turn:" + turn + ":" + state
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return 0, "", nil, e
			}
		}
		if err == nil {
			rows, e := reader.QueryContext(ctx, `SELECT c.id,c.topic_json,c.selection_json FROM knowledge_topic_candidates c JOIN knowledge_discovery_batches b ON b.id=c.batch_id WHERE json_valid(b.input_json) AND json_extract(b.input_json,'$.learning_question.id')=? AND json_extract(b.input_json,'$.learning_question.revision')=? UNION ALL SELECT p.id,p.analysis_json,p.material_fingerprint FROM knowledge_update_proposals p WHERE p.question_id=? AND p.question_revision=? ORDER BY 1 LIMIT 20`, id, revision, id, revision)
			if e != nil {
				return 0, "", nil, e
			}
			defer rows.Close()
			var questionMissing struct {
				Missing []string `json:"missing"`
			}
			for rows.Next() {
				var object, body, selection string
				if e = rows.Scan(&object, &body, &selection); e != nil {
					return 0, "", nil, e
				}
				scope += "|missing:" + object + ":" + body + ":" + selection
				var model struct {
					Missing []string `json:"missing"`
				}
				if json.Unmarshal([]byte(body), &model) == nil {
					for _, v := range model.Missing {
						questionMissing.Missing = append(questionMissing.Missing, "关联文章范围的模型建议："+v)
					}
				}
			}
			if e = rows.Err(); e != nil {
				return 0, "", nil, e
			}
			encoded, _ := json.Marshal(questionMissing)
			raw = string(encoded)
		}
	case "candidate":
		err = reader.QueryRowContext(ctx, `SELECT c.topic_json,c.selection_json||b.input_hash,b.input_json FROM knowledge_topic_candidates c JOIN knowledge_discovery_batches b ON b.id=c.batch_id WHERE c.id=?`, id).Scan(&raw, &scope, &dependencyInput)
		revision = 1
	case "update":
		err = reader.QueryRowContext(ctx, `SELECT p.parent_revision,CASE WHEN a.working_revision=p.parent_revision THEN p.analysis_json ELSE '{}' END,p.input_hash||':'||p.state||':'||a.working_revision,p.input_json FROM knowledge_update_proposals p JOIN knowledge_articles a ON a.id=p.article_id WHERE p.id=?`, id).Scan(&revision, &raw, &scope, &dependencyInput)
	case "article":
		err = reader.QueryRowContext(ctx, `SELECT working_revision,topic_json,status||':'||input_hash,input_json FROM knowledge_articles WHERE id=?`, id).Scan(&revision, &raw, &scope, &dependencyInput)
	default:
		return 0, "", nil, ErrInvalidEditorialState
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return 0, "", nil, err
	}
	if json.Valid([]byte(dependencyInput)) {
		var missingSource int
		e := reader.QueryRowContext(ctx, `SELECT count(*) FROM json_tree(?) ids JOIN json_tree(?) types ON types.parent=ids.parent AND types.key='source_type' WHERE ids.key='source_id' AND ids.value!='' AND ((types.value='episode' AND NOT EXISTS(SELECT 1 FROM episodes WHERE id=ids.value)) OR (types.value='upload' AND NOT EXISTS(SELECT 1 FROM uploads WHERE id=ids.value)) OR (types.value='document' AND NOT EXISTS(SELECT 1 FROM documents WHERE id=ids.value)))`, dependencyInput, dependencyInput).Scan(&missingSource)
		if e != nil {
			return 0, "", nil, e
		}
		var missingSnapshot int
		e = reader.QueryRowContext(ctx, `SELECT count(*) FROM json_tree(?) WHERE key='snapshot_id' AND value!='' AND NOT EXISTS(SELECT 1 FROM source_snapshots WHERE id=json_tree.value AND status!='purged')`, dependencyInput).Scan(&missingSnapshot)
		if e != nil {
			return 0, "", nil, e
		}
		if missingSource > 0 || missingSnapshot > 0 {
			scope += "|dependencies-purged"
			raw = "{}"
		}
	}
	var missing struct {
		Missing []string `json:"missing"`
	}
	if raw != "" && json.Unmarshal([]byte(raw), &missing) != nil {
		return 0, "", nil, ErrInvalidEditorialState
	}
	if kind == "question" && strings.Contains(scope, "|turn:") {
		missing.Missing = append(missing.Missing, "已保存的回答检查记录为材料不足；具体缺口原因未知，需由我确认")
	}
	return revision, gapDigest(fmt.Sprintf("%d:%s:%s", revision, raw, scope)), missing.Missing, nil
}
func gapKind(text string) string {
	for _, v := range []struct{ word, kind string }{{"反例", "counterexample"}, {"例子", "example"}, {"条件", "condition"}, {"比较", "comparison"}, {"实践", "practice"}} {
		if strings.Contains(text, v.word) {
			return v.kind
		}
	}
	return "other"
}
func validGapKind(k string) bool {
	return k == "example" || k == "counterexample" || k == "condition" || k == "comparison" || k == "practice" || k == "other"
}

// ListEvidenceGaps is a read-only projection: GET never admits model work,
// changes judgments or writes material links. Stale judgments remain visible.
func (s *Store) ListEvidenceGaps(ctx context.Context, kind, id string) ([]EvidenceGap, error) {
	rev, hash, missing, err := s.gapParent(ctx, kind, id)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+gapColumns+` FROM evidence_gaps WHERE parent_kind=? AND parent_id=? ORDER BY created_at,id LIMIT 200`, kind, id)
	if err != nil {
		return nil, err
	}
	out := []EvidenceGap{}
	seen := map[string]bool{}
	for rows.Next() {
		g, e := scanEvidenceGap(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		if g.ParentHash != hash || g.ParentRevision != rev {
			g.State = "expired"
		}
		seen[g.ID] = true
		out = append(out, g)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, text := range missing {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		g := EvidenceGap{ParentKind: kind, ParentID: id, ParentRevision: rev, ParentHash: hash, Kind: gapKind(text), Origin: "model", Explanation: text, Coverage: "仅基于父对象保存的选材范围；模型建议，未检验全库", State: "pending", Revision: 0}
		if kind == "question" && strings.HasPrefix(text, "已保存的回答检查") {
			g.Origin = "program"
			g.Coverage = "仅基于当前问题已保存的不足状态；不推断原因，未检验全库"
		}
		g.ID = "gap:" + gapDigest(kind+":"+id+":"+hash+":"+text)
		if !seen[g.ID] {
			out = append(out, g)
			seen[g.ID] = true
		}
		if len(out) >= 200 {
			break
		}
	}
	return out, nil
}

// CreateEvidenceGap records explicit Owner/program limitations. Model fields
// use the persisted Missing projection rather than arbitrary request content.
func (s *Store) CreateEvidenceGap(ctx context.Context, kind, id string, expected int, typ, origin, text, coverage string) (EvidenceGap, error) {
	if !validGapKind(typ) || (origin != "owner" && origin != "program") || strings.TrimSpace(text) == "" || len([]rune(text)) > 2000 || len([]rune(coverage)) > 2000 {
		return EvidenceGap{}, ErrInvalidEditorialState
	}
	rev, hash, _, err := s.gapParent(ctx, kind, id)
	if err != nil {
		return EvidenceGap{}, err
	}
	if rev != expected {
		return EvidenceGap{}, ErrConflict
	}
	if coverage == "" {
		coverage = "仅基于当前父对象范围；原因未知，未检验全库"
	}
	g := EvidenceGap{ID: "gap:" + gapDigest(kind+":"+id+":"+hash+":"+typ+":"+origin+":"+text), ParentKind: kind, ParentID: id, ParentRevision: rev, ParentHash: hash, Kind: typ, Origin: origin, Explanation: strings.TrimSpace(text), Coverage: coverage, State: "pending"}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return g, err
	}
	defer tx.Rollback()
	current, currentHash, _, err := gapParentRead(ctx, tx, kind, id)
	if err != nil {
		return g, err
	}
	if current != expected || currentHash != hash {
		return g, ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO evidence_gaps(id,parent_kind,parent_id,parent_revision,parent_hash,kind,origin,explanation,coverage)VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, g.ID, g.ParentKind, g.ParentID, g.ParentRevision, g.ParentHash, g.Kind, g.Origin, g.Explanation, g.Coverage)
	if err != nil {
		return g, err
	}
	if err = tx.Commit(); err != nil {
		return g, err
	}
	return scanEvidenceGap(s.DB.QueryRowContext(ctx, `SELECT `+gapColumns+` FROM evidence_gaps WHERE id=?`, g.ID))
}

// ChangeEvidenceGap uses both parent scope CAS and gap CAS. Playback cannot
// invoke this command and never marks a gap fulfilled automatically.
func (s *Store) ChangeEvidenceGap(ctx context.Context, kind, id, gapID string, parentExpected, gapExpected int, state, comment string) (EvidenceGap, error) {
	if state != "helpful" && state != "insufficient" && state != "ignored" && state != "pending" || len([]rune(comment)) > 2000 {
		return EvidenceGap{}, ErrInvalidEditorialState
	}
	gaps, err := s.ListEvidenceGaps(ctx, kind, id)
	if err != nil {
		return EvidenceGap{}, err
	}
	var g EvidenceGap
	found := false
	for _, v := range gaps {
		if v.ID == gapID {
			g = v
			found = true
			break
		}
	}
	if !found {
		return g, ErrNotFound
	}
	rev, hash, _, err := s.gapParent(ctx, kind, id)
	if err != nil {
		return g, err
	}
	if rev != parentExpected || g.ParentHash != hash || g.State == "expired" || g.Revision != gapExpected {
		return g, ErrConflict
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return g, err
	}
	defer tx.Rollback()
	if gapExpected == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO evidence_gaps(id,parent_kind,parent_id,parent_revision,parent_hash,kind,origin,explanation,coverage,revision,state)VALUES(?,?,?,?,?,?,?,?,?,1,?)`, g.ID, g.ParentKind, g.ParentID, g.ParentRevision, g.ParentHash, g.Kind, g.Origin, g.Explanation, g.Coverage, state)
	} else {
		var res sql.Result
		res, err = tx.ExecContext(ctx, `UPDATE evidence_gaps SET state=?,revision=revision+1,updated_at=datetime('now') WHERE id=? AND revision=? AND parent_hash=?`, state, g.ID, gapExpected, hash)
		if err == nil {
			n, _ := res.RowsAffected()
			if n != 1 {
				return g, ErrConflict
			}
		}
	}
	if err != nil {
		return g, err
	}
	next := gapExpected + 1
	_, err = tx.ExecContext(ctx, `INSERT INTO evidence_gap_operations(gap_id,revision,state,comment)VALUES(?,?,?,?)`, g.ID, next, state, comment)
	if err != nil {
		return g, err
	}
	// Reject concurrent body, scope or proposal changes in the same transaction.
	current, currentHash, _, err := gapParentRead(ctx, tx, kind, id)
	if err != nil {
		return g, err
	}
	if current != parentExpected || currentHash != hash {
		return g, ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return g, err
	}
	return scanEvidenceGap(s.DB.QueryRowContext(ctx, `SELECT `+gapColumns+` FROM evidence_gaps WHERE id=?`, g.ID))
}

// ListEvidenceGapOperations is immutable Owner action history.
func (s *Store) ListEvidenceGapOperations(ctx context.Context, id string) ([]EvidenceGapOperation, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT revision,state,comment,created_at FROM evidence_gap_operations WHERE gap_id=? ORDER BY revision`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EvidenceGapOperation{}
	for rows.Next() {
		var v EvidenceGapOperation
		if err = rows.Scan(&v.Revision, &v.State, &v.Comment, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// FindEvidenceGapCandidates explicitly performs bounded local retrieval. It
// neither changes material permissions nor processes untouched sources.
func (s *Store) FindEvidenceGapCandidates(ctx context.Context, kind, id, gapID, text string, semantic bool, configID string) (EvidenceGapCandidates, error) {
	return s.findEvidenceGapCandidates(ctx, kind, id, gapID, text, semantic, configID, true)
}
func (s *Store) findEvidenceGapCandidates(ctx context.Context, kind, id, gapID, text string, semantic bool, configID string, allowExpired bool) (EvidenceGapCandidates, error) {
	gaps, err := s.ListEvidenceGaps(ctx, kind, id)
	if err != nil {
		return EvidenceGapCandidates{}, err
	}
	found := false
	for _, g := range gaps {
		if g.ID == gapID && (allowExpired || g.State != "expired") {
			found = true
			if text == "" {
				text = g.Explanation
			}
			break
		}
	}
	if !found {
		return EvidenceGapCandidates{}, ErrNotFound
	}
	result, err := s.Retrieve(ctx, KnowledgeRetrieveQuery{Purpose: RetrieveLocal, Search: KnowledgeSearchQuery{Text: text, PerPage: 20, Page: 1}, Semantic: semantic, EmbeddingConfigID: configID})
	if err != nil {
		return EvidenceGapCandidates{}, err
	}
	out := EvidenceGapCandidates{Candidates: []EvidenceGapCandidate{}, Method: result.Method, Total: result.Total, Degradation: result.Degradation, Coverage: "仅检查本次本地索引匹配候选（最多20项）；未处理来源、未索引内容及其它查询措辞可能遗漏；不表示全库没有材料"}
	links := []LearningQuestionRelation{}
	if kind == "question" {
		links, err = s.ListLearningQuestionRelations(ctx, id)
		if err != nil {
			return out, err
		}
	}
	for _, hit := range result.Hits {
		if hit.SnapshotID == "" && hit.SourceID != "" {
			snapKind := models.SnapshotKindAudio
			if hit.SourceType == "document" {
				snapKind = models.SnapshotKindDocument
			}
			snapshot, e := s.getSourceSnapshotByUniqueKey(ctx, models.SourceType(hit.SourceType), hit.SourceID, snapKind, hit.Revision)
			if e == nil && snapshot.Status != models.SnapshotPurged {
				hit.SnapshotID = snapshot.ID
			}
		}
		c := EvidenceGapCandidate{KnowledgeSearchHit: hit, Href: "/knowledge/search?q=" + url.QueryEscape(text)}
		for _, l := range links {
			if l.State == "confirmed" && (l.SourceID != "" && l.SourceType == hit.SourceType && l.SourceID == hit.SourceID || l.ObjectID == hit.ObjectID || hit.SnapshotID != "" && l.ObjectID == hit.SnapshotID) {
				c.AlreadyLinked = true
			}
		}
		if hit.Kind == "understanding" {
			u, e := s.GetUnderstandingSnapshot(ctx, hit.ObjectID)
			if e != nil {
				return out, e
			}
			c.Href = "/questions/" + u.QuestionID + "/understandings"
			c.Limitation = "个人理解快照；关联不会改变其模型外发权限"
		}
		if hit.SnapshotID != "" {
			snap, segs, docs, e := s.SnapshotContent(ctx, hit.SnapshotID)
			if e != nil {
				c.Limitation = "冻结材料不可用"
			} else if snap.Kind == models.SnapshotKindAudio {
				matched := false
				for _, seg := range segs {
					if seg.ID == hit.SegmentID {
						c.Start, c.End = seg.Start, seg.End
						matched = true
						break
					}
				}
				if matched && c.Start < c.End {
					a, e := s.SnapshotAudioIdentity(ctx, snap.ID)
					c.Playable = e == nil && a.Status == models.AudioPlayable
				}
				if !c.Playable {
					c.Limitation = "原音身份不匹配或实际片段不可用"
				}
				c.Href = "/evidence/" + snap.ID
			} else {
				c.Href = "/evidence/" + snap.ID
				if len(docs) == 0 {
					c.Limitation = "文档段落不可用"
				}
			}
		} else if hit.SourceType == "episode" || hit.SourceType == "upload" {
			c.RequiresProcessing = true
			c.Limitation = "需要先处理并形成真实快照；本次不会自动处理"
		}
		out.Candidates = append(out.Candidates, c)
	}
	if len(out.Candidates) < 20 {
		rows, e := s.DB.QueryContext(ctx, `SELECT source_type,source_id,title FROM (SELECT 'episode' source_type,id source_id,title,created_at FROM episodes WHERE COALESCE(current_transcript_version,0)=0 AND archived_at IS NULL UNION ALL SELECT 'upload',id,original_filename,created_at FROM uploads WHERE COALESCE(current_transcript_version,0)=0 AND archived_at IS NULL) WHERE instr(lower(title),lower(?))>0 ORDER BY created_at DESC,source_type,source_id LIMIT 20`, text)
		sources := []KnowledgeSearchSource{}
		if e == nil {
			for rows.Next() {
				var src KnowledgeSearchSource
				if e = rows.Scan(&src.SourceType, &src.SourceID, &src.Title); e != nil {
					break
				}
				sources = append(sources, src)
			}
			if e == nil {
				e = rows.Err()
			}
			rows.Close()
		}
		if e != nil {
			return out, e
		}
		seen := map[string]bool{}
		for _, c := range out.Candidates {
			seen[c.SourceType+":"+c.SourceID] = true
		}
		for _, src := range sources {
			if len(out.Candidates) >= 20 {
				break
			}
			if seen[src.SourceType+":"+src.SourceID] || src.SourceType == "document" {
				continue
			}
			var version int
			table := questionSourceTable(src.SourceType)
			if table == "" {
				continue
			}
			if e = s.DB.QueryRowContext(ctx, `SELECT COALESCE(current_transcript_version,0) FROM `+table+` WHERE id=?`, src.SourceID).Scan(&version); e != nil {
				return out, e
			}
			if version > 0 {
				continue
			}
			c := EvidenceGapCandidate{KnowledgeSearchHit: KnowledgeSearchHit{Key: "source_metadata:" + src.SourceType + ":" + src.SourceID, Kind: "source_metadata", ObjectID: src.SourceID, SourceType: src.SourceType, SourceID: src.SourceID, Title: src.Title, Reason: "来源标题匹配；正文尚未处理，未检验内容"}, RequiresProcessing: true, Limitation: "需要先处理；本次不会自动转录、生成DJ或调用模型", Href: "/sources/" + url.PathEscape(src.SourceType) + "/" + url.PathEscape(src.SourceID)}
			for _, l := range links {
				if l.State == "confirmed" && l.SourceType == src.SourceType && l.SourceID == src.SourceID {
					c.AlreadyLinked = true
				}
			}
			out.Candidates = append(out.Candidates, c)
			out.Method = result.Method + "+source_titles"
			seen[src.SourceType+":"+src.SourceID] = true
		}
	}
	return out, nil
}

// ConfirmEvidenceGapCandidate re-resolves a current local hit and delegates to
// the existing question relationship CAS. A candidate is never authorization.
func (s *Store) ConfirmEvidenceGapCandidate(ctx context.Context, questionID, gapID, text, key string, expected int) (*LearningQuestion, error) {
	return s.ConfirmEvidenceGapCandidateWithRetrieval(ctx, questionID, gapID, text, key, expected, false, "", "")
}

// ConfirmEvidenceGapCandidateWithRetrieval rechecks the exact selected local retrieval method before linking.
func (s *Store) ConfirmEvidenceGapCandidateWithRetrieval(ctx context.Context, questionID, gapID, text, key string, expected int, semantic bool, configID, method string) (*LearningQuestion, error) {
	q, err := s.GetLearningQuestion(ctx, questionID)
	if err != nil {
		return nil, err
	}
	if q.Revision != expected {
		return nil, ErrConflict
	}
	result, err := s.findEvidenceGapCandidates(ctx, "question", questionID, gapID, text, semantic, configID, true)
	if err != nil {
		return nil, err
	}
	if method != "" && method != result.Method {
		return nil, ErrConflict
	}
	var link provider.LearningQuestionLink
	found := false
	for _, c := range result.Candidates {
		if c.Key == key {
			found = true
			switch c.Kind {
			case "understanding":
				link = provider.LearningQuestionLink{Kind: "understanding", ObjectID: c.ObjectID}
			case "source_note", "owner_reflection", "keypoint":
				k := c.Kind
				if k != "keypoint" {
					k = "note"
				}
				link = provider.LearningQuestionLink{Kind: k, ObjectID: c.ObjectID}
			default:
				if c.SnapshotID != "" {
					link = provider.LearningQuestionLink{Kind: "evidence", ObjectID: c.SnapshotID}
				} else if c.SourceID != "" {
					link = provider.LearningQuestionLink{Kind: "source", SourceType: c.SourceType, SourceID: c.SourceID}
				}
			}
			break
		}
	}
	if !found || link.Kind == "" {
		return nil, ErrNotFound
	}
	gaps, err := s.ListEvidenceGaps(ctx, "question", questionID)
	if err != nil {
		return nil, err
	}
	for _, g := range gaps {
		if g.ID == gapID && g.State == "expired" {
			links, e := s.ListLearningQuestionRelations(ctx, questionID)
			if e != nil {
				return nil, e
			}
			objectID := link.ObjectID
			if link.Kind == "source" {
				objectID = link.SourceType + ":" + link.SourceID
			}
			for _, l := range links {
				if l.State == "confirmed" && l.Kind == link.Kind && l.ObjectID == objectID {
					return q, nil
				}
			}
			return nil, ErrConflict
		}
	}
	return s.ChangeLearningQuestion(ctx, questionID, expected, LearningQuestionChange{Action: "link", Link: link})
}

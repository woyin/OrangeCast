package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

// FreezeQuestionStudyScope is an explicit admission preparation, never a GET.
// It loads bounded current bodies only after confirmed-membership and provider
// authorization filters; the admission transaction rechecks frozen identities.
func (s *Store) FreezeQuestionStudyScope(ctx context.Context, sessionID, input, providerName string, selectedKeys []string) (*provider.QuestionStudyScope, error) {
	if strings.TrimSpace(input) == "" || len(input) > 8192 || !utf8.ValidString(input) {
		return nil, ErrInvalidEditorialState
	}
	session, err := s.GetQuestionStudySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	question, err := s.FreezeLearningQuestion(ctx, session.QuestionID, false)
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	if len(selectedKeys) > 20 {
		return nil, ErrInvalidEditorialState
	}
	for _, key := range selectedKeys {
		if key == "" || len(key) > 300 || selected[key] {
			return nil, ErrInvalidEditorialState
		}
		selected[key] = true
	}
	result, err := s.Retrieve(ctx, KnowledgeRetrieveQuery{Purpose: RetrieveExternal, Search: KnowledgeSearchQuery{Question: question, SendProvider: providerName, MetadataOnly: true, PerPage: 200}})
	if err != nil {
		return nil, err
	}
	scope := &provider.QuestionStudyScope{Version: provider.QuestionStudyPromptVersion, Question: question, OwnerInput: input, Materials: []provider.QuestionStudyMaterial{}, History: []provider.QuestionStudyHistoryItem{}, Omissions: []string{}}
	if result.Total > len(result.Hits) {
		scope.Omissions = append(scope.Omissions, "确认范围超过200个候选；仅读取有界候选元数据")
	}
	found := map[string]bool{}
	sources := map[string]bool{}
	materialBytes := 0
	for _, hit := range result.Hits {
		if len(selected) > 0 && !selected[hit.Key] {
			continue
		}
		found[hit.Key] = true
		if len(scope.Materials) >= 20 {
			scope.Omissions = appendOnce(scope.Omissions, "材料超过20项，剩余对象未外发")
			continue
		}
		sourceKey := hit.SourceType + ":" + hit.SourceID
		if !sources[sourceKey] && len(sources) >= 8 {
			scope.Omissions = appendOnce(scope.Omissions, "材料超过8个来源，剩余来源未外发")
			continue
		}
		material, err := s.freezeQuestionStudyMaterial(ctx, hit, providerName)
		if errors.Is(err, errQuestionStudyMaterialCapacity) {
			scope.Omissions = appendOnce(scope.Omissions, "完整材料超过上下文容量，未截断或外发该材料")
			continue
		}
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrSnapshotInvalidated) {
			scope.Omissions = appendOnce(scope.Omissions, "部分材料版本、依据或外发权限已失效，未外发")
			continue
		}
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(material)
		if len(raw) > 10*1024 || materialBytes+len(raw) > 40*1024 {
			scope.Omissions = appendOnce(scope.Omissions, "完整材料超过上下文容量，未截断或外发该材料")
			continue
		}
		sources[sourceKey] = true
		scope.Materials = append(scope.Materials, *material)
		materialBytes += len(raw)
	}
	for key := range selected {
		if !found[key] {
			return nil, fmt.Errorf("%w: 所选材料不在当前已确认且可外发的范围", ErrConflict)
		}
	}
	history, err := s.QuestionStudyHistory(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(history) > 6 {
		scope.Omissions = append(scope.Omissions, "仅采用最近6轮已接受历史，较早轮未外发")
		history = history[len(history)-6:]
	}
	for _, turn := range history {
		rows, err := s.DB.QueryContext(ctx, `SELECT source_type,source_id FROM question_study_sources WHERE turn_id=?`, turn.ID)
		if err != nil {
			return nil, err
		}
		var refs []QuestionStudySource
		for rows.Next() {
			var ref QuestionStudySource
			if err = rows.Scan(&ref.SourceType, &ref.SourceID); err != nil {
				break
			}
			refs = append(refs, ref)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
		allowed := len(refs) > 0
		for _, ref := range refs {
			ok, e := s.canSendQuestionStudySource(ctx, QuestionStudySource{ref.SourceType, ref.SourceID}, providerName)
			if e != nil {
				return nil, e
			}
			if !ok {
				allowed = false
				break
			}
		}
		if !allowed {
			scope.Omissions = appendOnce(scope.Omissions, "历史回答的来源现已限制外发，该轮未外发")
			continue
		}
		scope.History = append(scope.History, provider.QuestionStudyHistoryItem{Ordinal: turn.Ordinal, OwnerInput: turn.OwnerInput, AcceptedJSON: turn.AcceptedJSON})
	}
	if len(scope.Materials) == 0 {
		return scope, fmt.Errorf("%w: 没有可用的已确认且可外发材料。%s", ErrInvalidEditorialState, strings.Join(scope.Omissions, "；"))
	}
	if _, err = provider.QuestionStudyMessages(*scope); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidEditorialState, err)
	}
	return scope, nil
}
func appendOnce(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

var errQuestionStudyMaterialCapacity = errors.New("question study material capacity")

func (s *Store) freezeQuestionStudyMaterial(ctx context.Context, hit KnowledgeSearchHit, name string) (*provider.QuestionStudyMaterial, error) {
	allowed, err := s.canSendQuestionStudySource(ctx, QuestionStudySource{hit.SourceType, hit.SourceID}, name)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrConflict
	}
	var body string
	var revision int
	err = s.DB.QueryRowContext(ctx, `SELECT substr(body,1,10241),revision FROM knowledge_search_docs WHERE key=? AND visibility='current'`, hit.Key).Scan(&body, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(body) > 10*1024 {
		return nil, errQuestionStudyMaterialCapacity
	}
	if revision != hit.Revision {
		return nil, ErrConflict
	}
	material := &provider.QuestionStudyMaterial{Key: hit.Key, Kind: hit.Kind, SourceType: hit.SourceType, SourceID: hit.SourceID, Revision: revision, Content: body}
	var refs []string
	switch hit.Kind {
	case "original", "document":
		refs = []string{hit.SegmentID}
	case "source_note", "owner_reflection":
		note, err := s.GetOwnerNote(ctx, hit.ObjectID)
		if err != nil {
			return nil, err
		}
		raw := note.CitationsJSON
		if hit.Kind == "owner_reflection" {
			raw = note.ReferencesJSON
		}
		if err = json.Unmarshal([]byte(raw), &refs); err != nil {
			return nil, err
		}
	case "keypoint":
		point, err := s.GetKeyPoint(ctx, hit.ObjectID)
		if err != nil {
			return nil, err
		}
		if point.StaleAt != "" || point.EvidenceStatus == "stale" || point.ProductionStatus == models.KeyPointDismissed || (point.QualityStatus != models.KeyPointReady && point.QualityStatus != models.KeyPointOwnerConfirmed) {
			return nil, ErrConflict
		}
		if err = json.Unmarshal([]byte(point.CitationsJSON), &refs); err != nil {
			return nil, err
		}
	default:
		return nil, ErrConflict
	}
	if hit.Kind != "owner_reflection" && len(refs) == 0 {
		return nil, ErrConflict
	}
	if len(refs) > 20 {
		return nil, ErrConflict
	}
	if len(refs) > 0 {
		snapshot, err := s.FreezeSourceSnapshot(ctx, models.SourceType(hit.SourceType), hit.SourceID)
		if err != nil {
			return nil, err
		}
		_, audio, docs, err := s.SnapshotContent(ctx, snapshot.ID)
		if err != nil {
			return nil, err
		}
		segments := map[string]provider.KnowledgeEvidenceSegment{}
		for _, seg := range audio {
			segments[seg.ID] = provider.KnowledgeEvidenceSegment{SegmentID: seg.ID, Text: seg.Text, Position: seg.Start}
		}
		for _, seg := range docs {
			segments[seg.ID] = provider.KnowledgeEvidenceSegment{SegmentID: seg.ID, Text: seg.Text, Position: float64(seg.Position)}
		}
		for _, id := range refs {
			segment, ok := segments[id]
			if !ok || strings.TrimSpace(id) == "" {
				return nil, ErrConflict
			}
			material.Segments = append(material.Segments, segment)
		}
		material.SnapshotID = snapshot.ID
		if (hit.Kind == "original" || hit.Kind == "document") && (len(material.Segments) != 1 || material.Content != material.Segments[0].Text) {
			return nil, ErrConflict
		}
	}
	material.ContentHash = provider.QuestionStudyMaterialHash(*material)
	return material, nil
}

func (s *Store) canSendQuestionStudySource(ctx context.Context, source QuestionStudySource, name string) (bool, error) {
	if !validSourceType(models.SourceType(source.SourceType)) {
		return false, ErrInvalidEditorialState
	}
	var live int
	err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM `+sourceTable(models.SourceType(source.SourceType))+` WHERE id=? AND archived_at IS NULL AND NOT EXISTS(SELECT 1 FROM source_snapshots WHERE source_type=? AND source_id=? AND status='purged')`, source.SourceID, source.SourceType, source.SourceID).Scan(&live)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return s.CanSendSourceToProvider(ctx, models.SourceType(source.SourceType), source.SourceID, name)
}

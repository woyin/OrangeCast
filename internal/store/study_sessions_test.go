package store

import (
	"context"
	"testing"

	"github.com/woyin/orangecast/internal/models"
)

// TestStudySession_Lifecycle 验证学习会话的创建/读取/列表与消息追加/过滤。
func TestStudySession_Lifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sourceID := "ep-1"

	// 创建会话
	sess, err := s.CreateStudySession(ctx, models.SourceEpisode, sourceID, "通胀专题")
	if err != nil {
		t.Fatalf("创建会话: %v", err)
	}
	if sess.Title != "通胀专题" || sess.SourceID != sourceID {
		t.Errorf("会话内容不符: %+v", sess)
	}

	// 读取
	got, err := s.GetStudySession(ctx, sess.ID)
	if err != nil || got.ID != sess.ID {
		t.Fatalf("读取会话失败: %v %+v", err, got)
	}
	// 未知名 → ErrNotFound
	if _, err := s.GetStudySession(ctx, "nope"); err != ErrNotFound {
		t.Errorf("未知名应 ErrNotFound，实际 %v", err)
	}

	// 列表（同一 source 有两个会话则都返回）
	sess2, _ := s.CreateStudySession(ctx, models.SourceEpisode, sourceID, "第二个")
	list, err := s.ListStudySessions(ctx, models.SourceEpisode, sourceID)
	if err != nil {
		t.Fatalf("列表: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("应有 2 个会话，实际 %d", len(list))
	}
	if list[0].ID != sess2.ID { // 最近创建在前
		t.Errorf("应按创建时间倒序，实际 %+v", list)
	}
}

// TestStudyMessages_AppendAndSuppress 验证消息追加、Reference 关联与抑制过滤。
func TestStudyMessages_AppendAndSuppress(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sess, _ := s.CreateStudySession(ctx, models.SourceEpisode, "ep-1", "会话")

	// user 消息
	user, err := s.AppendStudyMessage(ctx, sess.ID, "user", "通胀是什么", nil, false)
	if err != nil {
		t.Fatalf("追加 user 消息: %v", err)
	}
	if user.Role != "user" {
		t.Errorf("role 应为 user，实际 %q", user.Role)
	}
	// assistant 消息带 Reference
	asst, err := s.AppendStudyMessage(ctx, sess.ID, "assistant", "通胀是物价上涨", []string{"seg-0001"}, false)
	if err != nil {
		t.Fatalf("追加 assistant 消息: %v", err)
	}
	if len(asst.ReferenceSegmentIDs) != 1 || asst.ReferenceSegmentIDs[0] != "seg-0001" {
		t.Errorf("Reference 关联不符: %+v", asst.ReferenceSegmentIDs)
	}
	if asst.RelationKind != models.RelationReference {
		t.Errorf("assistant 应挂 Reference，实际 %q", asst.RelationKind)
	}
	// 被抑制的 assistant 消息
	if _, err := s.AppendStudyMessage(ctx, sess.ID, "assistant", "被抑制的回答", []string{"seg-0001"}, true); err != nil {
		t.Fatalf("追加抑制消息: %v", err)
	}

	// 默认过滤掉被抑制
	msgs, err := s.ListStudyMessages(ctx, sess.ID, false)
	if err != nil {
		t.Fatalf("ListStudyMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("默认应返回 2 条（过滤抑制），实际 %d", len(msgs))
	}
	// includeSuppressed=true 返回全部 3 条
	all, _ := s.ListStudyMessages(ctx, sess.ID, true)
	if len(all) != 3 {
		t.Errorf("includeSuppressed 应返回 3 条，实际 %d", len(all))
	}
	// 抑制标记正确
	for _, m := range all {
		if m.Content == "被抑制的回答" && !m.Suppressed {
			t.Error("被抑制消息应 suppressed=true")
		}
	}

	// 删除会话 → 消息级联删除
	if err := s.DeleteStudySession(ctx, sess.ID); err != nil {
		t.Fatalf("删除会话: %v", err)
	}
	after, _ := s.ListStudyMessages(ctx, sess.ID, true)
	if len(after) != 0 {
		t.Errorf("删除会话后消息应级联删除，实际 %d", len(after))
	}
}

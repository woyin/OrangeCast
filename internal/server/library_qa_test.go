package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/woyin/orangecast/internal/models"
	"github.com/woyin/orangecast/internal/provider"
)

func TestAskLibrary(t *testing.T) {
	srv := newTestServer(t)
	cookie := claimOwnerAndLogin(t, srv, "ask@example.com", "password123")
	id := seedExportableEpisode(t, srv)
	ctx := context.Background()
	if err := srv.store.IndexSearch(ctx, models.SourceEpisode, id, "好莱坞沟通课", "概览", []provider.Segment{
		{ID: "seg-0001", Start: 7, End: 12, Text: "通胀是物价上升"},
	}); err != nil {
		t.Fatal(err)
	}

	var gotSegments []provider.Segment
	qa := &fakeQA{}
	srv.bundleFor = func(tc provider.TaskConfig) (*provider.ProviderBundle, error) {
		return &provider.ProviderBundle{QA: qaFunc(func(q string, segs []provider.Segment) (*provider.QAResult, error) {
			gotSegments = segs
			return qa.Answer(q, segs)
		})}, nil
	}
	ask := func(q string) string {
		return postForm(t, srv, cookie, "/ask", "question="+q).Body.String()
	}

	// GET：显示表单。
	if body := doWithCookie(srv, cookie, http.MethodGet, "/ask").Body.String(); !strings.Contains(body, "问我的库") {
		t.Errorf("表单: %s", body)
	}

	// 成功：模型引用候选 c1 → 回答 + 带时间的原文依据。
	qa.result = &provider.QAResult{Answer: "通胀就是物价上升。", Sources: []provider.Source{{SegmentID: "c1"}}}
	body := ask("通胀是什么")
	if !strings.Contains(body, "通胀就是物价上升。") || !strings.Contains(body, "/sources/episode/"+id+"?t=7.0") || !strings.Contains(body, "0:07 – 0:12") {
		t.Errorf("成功页应含回答与依据链接: %s", body)
	}
	if len(gotSegments) != 1 || gotSegments[0].ID != "c1" || gotSegments[0].Text != "通胀是物价上升" {
		t.Errorf("送给模型的候选: %+v", gotSegments)
	}

	// 模型引用了不存在的编号 → 拒答，不把检索结果冒充依据。
	qa.result = &provider.QAResult{Answer: "编造的回答", Sources: []provider.Source{{SegmentID: "c99"}}}
	if body := ask("通胀是什么"); !strings.Contains(body, "证据不足") || strings.Contains(body, "编造的回答") {
		t.Errorf("无效引用应拒答: %s", body)
	}
	// 模型没有引用任何片段 → 拒答。
	qa.result = &provider.QAResult{Answer: "没有依据的回答"}
	if body := ask("通胀是什么"); !strings.Contains(body, "证据不足") {
		t.Errorf("无引用应拒答: %s", body)
	}
	// 模型调用失败。
	qa.err = errors.New("boom")
	if body := ask("通胀是什么"); !strings.Contains(body, "回答失败") {
		t.Errorf("调用失败: %s", body)
	}
	qa.err = nil

	// 问题太短。
	if body := ask("a"); !strings.Contains(body, "2–500") {
		t.Errorf("短问题: %s", body)
	}
	// 没有相关内容。
	if body := ask("完全无关的量子计算话题"); !strings.Contains(body, "没有找到相关内容") {
		t.Errorf("无召回: %s", body)
	}
	// 来源标记为仅本地：不得送给模型。
	if _, err := srv.store.DB.ExecContext(ctx, `UPDATE episodes SET model_data_policy='local_only' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	qa.result = &provider.QAResult{Answer: "不应出现", Sources: []provider.Source{{SegmentID: "c1"}}}
	gotSegments = nil
	if body := ask("通胀是什么"); !strings.Contains(body, "不允许发送") || strings.Contains(body, "不应出现") || gotSegments != nil {
		t.Errorf("local_only 来源不应外发: %s", body)
	}
	// 问答模型不可用。
	srv.bundleFor = func(provider.TaskConfig) (*provider.ProviderBundle, error) { return nil, errors.New("no key") }
	if body := ask("通胀是什么"); !strings.Contains(body, "问答模型不可用") {
		t.Errorf("bundle 失败: %s", body)
	}
	// 非 GET/POST。
	if rec := doWithCookie(srv, cookie, http.MethodPut, "/ask"); rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusForbidden {
		t.Errorf("PUT: %d", rec.Code)
	}
}

// qaFunc 把函数适配成 provider.QAProvider。
type qaFunc func(string, []provider.Segment) (*provider.QAResult, error)

func (f qaFunc) Answer(q string, segs []provider.Segment) (*provider.QAResult, error) {
	return f(q, segs)
}
func (qaFunc) Name() string { return "fake-qa-func" }

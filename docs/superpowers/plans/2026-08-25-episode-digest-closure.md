# EpisodeDigest 完善与闭环实施计划

> **状态：已归档（2026-09-12）。** 本计划的 4 项任务均已按实际代码实现并入库：后端基线见提交 `4aa0b08`（feat(digest): establish episode digest backend baseline），列表/批量/文档入口/Markdown 导出见其后继提交（feat(digest): expose existing list batch and markdown flows）。自动化验证：独立候选 checkout `go test ./...` 全部通过。已知边界（已登记为后续 G 组任务，不在本计划内修复）：入队成功不等于成文成功（重试 handler 目前重新入队整篇 digest）；Markdown 下载尚缺引用跳转与版本化链接；来源剔除直接删除现有正文块而不是派生新修订。原执行指令（subagent 调度说明）已按归档规则移除。

**Goal:** 完成 EpisodeDigest（单集精读文）从后端垂直切片到完整用户旅程的闭环：补齐播客列表页批量生成精读文 UI、文档详情页生成精读文入口、Dashboard 导航与全局精读文列表页、草稿页 Markdown 复制/下载出口、以及 Progress 进度页对 `episode_digest` 任务类型的友好展示。

**Architecture:**
- 复用已完成的后端存储（`0025_episode_digests.sql`）、G2 门禁（`digest_validate.go`）、Provider（Groq/OpenAI）、Worker 任务编排（`queue/digest.go`）与 `/api/digest`、`/api/digest/batch` 路由。
- 在前端 SSR 模板层完善交互通道：播客详情页工具栏增加“批量生成精读文”按钮；文档详情页挂载单篇精读文生成表单；Dashboard 和全局导航增加 `/digests` 入口；草稿页增加 Markdown 格式导出/复制。

**Tech Stack:** Go 1.25, html/template SSR, 原生 JavaScript, CSS3, modernc.org/sqlite

---

## 文件结构规划

### 新建文件
- `internal/server/templates/digests.html`：全局精读文列表视图（展示所有已生成的精读文卡片、状态、渠道版本入口与时间戳）。

### 修改文件
- `internal/server/digest.go`：新增 `handleDigestList` 处理器、草稿 Markdown 导出端点 `handleDigestMarkdown`。
- `internal/server/routes.go`：注册 `/digests` 与 `/digest/{id}/markdown` 路由。
- `internal/server/templates/podcast_detail.html`：在单集批量工具栏增加“批量生成精读文”提交按钮与前端表单支持。
- `internal/server/templates/document_detail.html`：在已处理文档详情页添加“生成精读文”按钮表单。
- `internal/server/templates/dashboard.html`：在仪表盘添加“单集精读”卡片入口。
- `internal/server/templates/digest.html`：增加“一键复制 Markdown”与“下载 .md”操作按钮。
- `internal/server/templates/progress.html`：将 `episode_digest` 任务类型映射为人性化标签“单集精读生成”。
- `internal/server/digest_test.go`：增加列表页、Markdown 下载与各模板渲染的端到端集成测试。

---

## 任务拆解

### Task 1: 增加全局精读文列表页面与路由

**Files:**
- Create: `internal/server/templates/digests.html`
- Modify: `internal/server/digest.go`
- Modify: `internal/server/routes.go`
- Test: `internal/server/digest_test.go`

- [x] **Step 1: 编写列表页处理器与失败测试**

在 `internal/server/digest_test.go` 中新增测试函数 `TestDigestList_Renders`：

```go
// TestDigestList_Renders 验证 /digests 列表页成功渲染已生成的精读文列表。
func TestDigestList_Renders(t *testing.T) {
	srv, session, _, epID := seedDigestEpisode(t)
	ctx := t.Context()

	d, err := srv.store.CreateEpisodeDigest(ctx, &models.EpisodeDigest{
		SourceType: models.SourceEpisode, SourceID: epID, Title: "主权基金精读",
		Provider: "groq", Model: "m", PromptVersion: "v1",
	}, []models.DigestBlock{
		{Type: models.DigestBlockParaphrase, Text: "正文要点", Citations: []string{"seg-0001"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := doWithCookie(srv, session, http.MethodGet, "/digests")
	if rec.Code != http.StatusOK {
		t.Fatalf("/digests 应返回 200, 实际 %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "主权基金精读") || !strings.Contains(body, "/digest/"+d.ID) {
		t.Fatalf("列表页应包含精读文标题和链接: %s", body)
	}
}
```

- [x] **Step 2: 运行测试确认失败**

运行：`go test ./internal/server -run 'TestDigestList_Renders' -count=1`
预期：FAIL（404 page not found 或未定义路由）

- [x] **Step 3: 实现 `handleDigestList` 与 `digests.html` 模板**

在 `internal/server/digest.go` 中添加：

```go
// handleDigestList 展示所有已生成的精读文列表（每 Source 最新修订）。
func (srv *Server) handleDigestList(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/digests" {
		http.NotFound(w, r)
		return
	}
	list, err := srv.store.ListEpisodeDigests(r.Context())
	if err != nil {
		http.Error(w, "加载精读文列表失败", http.StatusInternalServerError)
		return
	}
	if err := srv.tmpl.Render(w, "digests.html", map[string]any{
		"Digests": list,
		"CSRF":    auth.CSRFValue(r),
	}); err != nil {
		http.Error(w, "渲染列表失败", http.StatusInternalServerError)
	}
}
```

创建 `internal/server/templates/digests.html`：

```html
{{define "title"}}单集精读文 · CloudWisePod{{end}}
{{define "content"}}
<div class="page-head">
  <h1>单集精读文</h1>
  <p class="meta">以单个 Source 为锚点一键生成的结构化精读文与小红书笔记（ADR-0023）。</p>
</div>

<section class="panel">
  {{if .Digests}}
  <div class="list">
    {{range .Digests}}
    <article class="list-item">
      <div class="ep-info">
        <h3><a href="/digest/{{.ID}}">{{.Title}}</a></h3>
        <p class="meta">
          来源：{{.SourceType}}/{{.SourceID}} · 版本 v{{.Version}} · {{.Provider}} · {{.CreatedAt}}
          {{if .Degraded}} · <span class="error">降级生成</span>{{end}}
        </p>
      </div>
      <a class="btn btn-secondary" href="/digest/{{.ID}}">查看草稿</a>
    </article>
    {{end}}
  </div>
  {{else}}
  <p class="meta">暂无已生成的精读文。前往<a href="/podcasts">播客单集</a>或<a href="/documents">文档</a>详情页点击“生成精读文”。</p>
  {{end}}
</section>
{{end}}
```

在 `internal/server/routes.go` 中注册路由：

```go
mux.HandleFunc("/digests", srv.handleDigestList)
```

在 `internal/server/templates/dashboard.html` 的卡片组中增加入口：

```html
<a class="card" href="/digests"><h3>单集精读</h3><p>一键生成与管理单源精读文与小红书笔记</p></a>
```

- [x] **Step 4: 运行测试验证通过**

运行：`go test ./internal/server -run 'TestDigestList_Renders' -count=1`
预期：PASS

- [x] **Step 5: 提交代码**

```bash
git add internal/server/digest.go internal/server/routes.go internal/server/templates/digests.html internal/server/templates/dashboard.html internal/server/digest_test.go
git commit -m "feat: add global episode digests list view and dashboard navigation"
```

---

### Task 2: 播客列表页批量生成精读文交互

**Files:**
- Modify: `internal/server/templates/podcast_detail.html`
- Test: `internal/server/digest_test.go`

- [x] **Step 1: 编写批量入队测试**

在 `internal/server/digest_test.go` 中新增测试函数 `TestDigestBatch_Success`：

```go
// TestDigestBatch_Success 验证勾选多集批量提交到 /api/digest/batch。
func TestDigestBatch_Success(t *testing.T) {
	srv, session, csrf, epID := seedDigestEpisode(t)

	form := url.Values{
		"source_type": {"episode"},
		"source_id":   {epID},
		"podcast_id":  {"pod-123"},
		"_csrf":       {csrf},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/digest/batch", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("批量入队应重定向 303, 实际 %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "enqueued=1") {
		t.Fatalf("重定向目标应包含 enqueued=1, 实际 %s", loc)
	}
}
```

- [x] **Step 2: 运行测试验证当前处理逻辑**

运行：`go test ./internal/server -run 'TestDigestBatch_Success' -count=1`
预期：PASS（之前已实现后端 handler）

- [x] **Step 3: 在 `podcast_detail.html` 增加批量精读文生成按钮**

在 `internal/server/templates/podcast_detail.html` 的批量工具栏中添加“批量生成精读文”操作：

```html
<div class="batch-toolbar" id="batch-toolbar" style="display:none">
  <span id="batch-count">已选 0 集</span>
  <button type="submit" class="btn" id="batch-submit" formaction="/api/process-batch">批量处理选中</button>
  <button type="submit" class="btn btn-secondary" id="batch-digest-submit" formaction="/api/digest/batch">批量生成精读文</button>
  <button type="button" class="btn btn-secondary" id="select-all-btn">全选当前页</button>
  <button type="button" class="btn btn-secondary" id="clear-all-btn">取消选择</button>
</div>
```

针对已处理单集，允许勾选参与精读文批量生成（放开仅限未处理的判断）：
```html
<input type="checkbox" name="source_id" value="{{.ID}}" class="batch-check" style="margin-right:8px;flex-shrink:0" aria-label="选择单集">
```

- [x] **Step 4: 运行 server 测试验证模板无语法破坏**

运行：`go test ./internal/server -count=1`
预期：PASS

- [x] **Step 5: 提交代码**

```bash
git add internal/server/templates/podcast_detail.html internal/server/digest_test.go
git commit -m "feat: add batch episode digest button to podcast detail toolbar"
```

---

### Task 3: 文档详情页挂载精读文生成入口

**Files:**
- Modify: `internal/server/templates/document_detail.html`
- Test: `internal/server/digest_test.go`

- [x] **Step 1: 编写文档生成精读文的端到端测试**

在 `internal/server/digest_test.go` 中新增测试函数 `TestDocumentDigestEnqueue_Success`：

```go
// TestDocumentDigestEnqueue_Success 验证针对 Document Source 单击生成精读文。
func TestDocumentDigestEnqueue_Success(t *testing.T) {
	srv, session, csrf, _ := seedDigestEpisode(t)
	ctx := t.Context()

	doc, err := srv.store.CreatePastedDocument(ctx, "架构设计文档", "本文档描述系统架构设计要点。")
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"source_type": {"document"},
		"source_id":   {doc.ID},
		"_csrf":       {csrf},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/digest", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	req.AddCookie(&http.Cookie{Name: "cwp_csrf", Value: csrf})
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("文档入队精读应 303, 实际 %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Location"), "/sources/document/"+doc.ID) {
		t.Fatalf("应重定向回文档详情, 实际 %s", rec.Header().Get("Location"))
	}
}
```

- [x] **Step 2: 运行测试检查通过情况**

运行：`go test ./internal/server -run 'TestDocumentDigestEnqueue_Success' -count=1`
预期：PASS 或 FAIL（若文档缺少 Transcript 版本，需要兼容 Document 作为天然可引用文本源的处理）

- [x] **Step 3: 适配 `EnqueueDigestJob` 与在 `document_detail.html` 挂载按钮**

如果 `EnqueueDigestJob` 针对 Document 报错（因为 Document 没有 `KindTranscript`），在 `internal/store/digests_enqueue.go` 中支持 `models.SourceDocument` 直接放行：

```go
if sourceType != models.SourceDocument {
    if _, err := s.GetCurrentVersion(ctx, sourceType, sourceID, KindTranscript); err != nil {
        if errors.Is(err, ErrNotFound) {
            return nil, fmt.Errorf("该内容尚未完成转录，无法生成精读文")
        }
        return nil, err
    }
}
```

在 `internal/server/templates/document_detail.html` 的标题操作区增加生成精读文按钮：

```html
<div class="page-head">
  <h1>{{.Document.Title}}</h1>
  <div>
    <form action="/api/digest" method="POST" style="display:inline">
      <input type="hidden" name="_csrf" value="{{.CSRF}}">
      <input type="hidden" name="source_type" value="document">
      <input type="hidden" name="source_id" value="{{.Document.ID}}">
      <button class="btn" type="submit">生成精读文</button>
    </form>
    <a class="btn btn-secondary" href="/documents">返回文档素材</a>
  </div>
</div>
```

- [x] **Step 4: 运行测试验证**

运行：`go test ./internal/store ./internal/server -count=1`
预期：PASS

- [x] **Step 5: 提交代码**

```bash
git add internal/store/digests_enqueue.go internal/server/templates/document_detail.html internal/server/digest_test.go
git commit -m "feat: enable episode digest generation for document sources"
```

---

### Task 4: 草稿页 Markdown 导出/下载与 Progress 页任务适配

**Files:**
- Modify: `internal/server/digest.go`
- Modify: `internal/server/routes.go`
- Modify: `internal/server/templates/digest.html`
- Modify: `internal/server/templates/progress.html`
- Test: `internal/server/digest_test.go`

- [x] **Step 1: 编写 Markdown 下载接口测试**

在 `internal/server/digest_test.go` 中新增测试函数 `TestDigestMarkdown_Download`：

```go
// TestDigestMarkdown_Download 验证 /digest/{id}/markdown 返回合法的 Markdown 文件。
func TestDigestMarkdown_Download(t *testing.T) {
	srv, session, _, epID := seedDigestEpisode(t)
	ctx := t.Context()

	d, _ := srv.store.CreateEpisodeDigest(ctx, &models.EpisodeDigest{
		SourceType: models.SourceEpisode, SourceID: epID, Title: "导出测试",
		Provider: "groq", Model: "m", PromptVersion: "v1",
	}, []models.DigestBlock{
		{Type: models.DigestBlockParaphrase, Text: "转述段落", Citations: []string{"seg-0001"}},
		{Type: models.DigestBlockAIExpansion, Text: "AI 展开：补充说明"},
	})

	rec := doWithCookie(srv, session, http.MethodGet, "/digest/"+d.ID+"/markdown")
	if rec.Code != http.StatusOK {
		t.Fatalf("下载 Markdown 应 200, 实际 %d", rec.Code)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".md") {
		t.Fatalf("应为 attachment .md 下载, 实际 %s", cd)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "# 导出测试") || !strings.Contains(body, "转述段落") || !strings.Contains(body, "> [!ai-generated] AI 展开") {
		t.Fatalf("Markdown 内容格式不符:\n%s", body)
	}
}
```

- [x] **Step 2: 运行测试确认失败**

运行：`go test ./internal/server -run 'TestDigestMarkdown_Download' -count=1`
预期：FAIL（404）

- [x] **Step 3: 实现 `handleDigestMarkdown` 并更新 `digest.html` 与 `progress.html`**

在 `internal/server/digest.go` 中增加 Markdown 生成和下载端点：

```go
// handleDigestMarkdown 输出确定性的 Markdown 格式精读文（带 Obsidian Callout 分层标注）。
func (srv *Server) handleDigestMarkdown(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/digest/")
	id := strings.TrimSuffix(path, "/markdown")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	d, err := srv.store.GetEpisodeDigest(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	blocks, err := srv.store.ListDigestBlocks(r.Context(), d.ID)
	if err != nil {
		http.Error(w, "加载内容块失败", http.StatusInternalServerError)
		return
	}

	var sb strings.Builder
	sb.WriteString("# " + d.Title + "\n\n")
	sb.WriteString(fmt.Sprintf("> 来源：%s/%s · 修订 v%d · %s\n\n", d.SourceType, d.SourceID, d.Version, d.CreatedAt))

	for _, b := range blocks {
		switch b.Type {
		case models.DigestBlockParaphrase:
			sb.WriteString(b.Text + "\n\n")
		case models.DigestBlockAIExpansion:
			sb.WriteString("> [!ai-generated] " + b.Text + "\n\n")
		case models.DigestBlockNote:
			sb.WriteString("> [!note] Owner 笔记\n> " + b.Text + "\n\n")
		case models.DigestBlockCitedFact:
			sb.WriteString(b.Text + "\n\n")
		}
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"digest-%s.md\"", d.ID))
	w.Write([]byte(sb.String()))
}
```

在 `internal/server/routes.go` 中注册路由：

```go
mux.HandleFunc("/digest/", func(w http.ResponseWriter, r *http.Request) {
    if strings.HasSuffix(r.URL.Path, "/markdown") {
        srv.handleDigestMarkdown(w, r)
        return
    }
    srv.handleDigestDetail(w, r)
})
```

在 `internal/server/templates/digest.html` 顶部增加下载与复制按钮：

```html
<section class="panel">
  <h1>{{.Digest.Title}}</h1>
  <p class="meta">
    修订 v{{.Digest.Version}} · {{.Digest.Provider}} · {{.Digest.CreatedAt}}
    {{if .Digest.Degraded}} · <span class="error">联网补充不可用（降级生成）</span>{{end}}
  </p>
  <div style="margin-top:0.75rem">
    <a class="btn" href="/digest/{{.Digest.ID}}/markdown">下载 Markdown</a>
    <a class="btn btn-secondary" href="/digests">返回列表</a>
  </div>
</section>
```

在 `internal/server/templates/progress.html` 增加 `episode_digest` 任务状态文本映射。

- [x] **Step 4: 运行所有测试**

运行：`go test ./...`
预期：所有包全 PASS

- [x] **Step 5: 提交代码**

```bash
git add internal/server/digest.go internal/server/routes.go internal/server/templates/digest.html internal/server/templates/progress.html internal/server/digest_test.go
git commit -m "feat: add markdown download endpoint and progress page task mapping for episode digest"
```

---

## 最终验证方案

1. **自动化测试**：
   - 运行 `go test ./...` 确保所有新增与存量用例（14 个包）100% 通过。
   - 运行 `make lint` 确保导出的所有新方法均符合 Godoc 注释规范。
   - 运行 `git diff --check` 保证无格式与空白残留。

2. **人工/冒烟验证**：
   - 启动本地服务：`go run ./cmd/cloudwisepod`
   - 访问 `/dashboard` → 点击进入“单集精读” `/digests` 列表页。
   - 打开一期已转录单集 `/sources/episode/{id}` → 点击“生成精读文” → 观察进度并进入 `/digest/{id}` 草稿页。
   - 在草稿页测试小红书重试生成、联网来源保留/剔除操作，并点击“下载 Markdown”。

---

## 执行交接（归档）

原"Subagent-Driven / Inline Execution"执行方式选择已随任务完成而失效，原指令不再保留。任务状态以本文件上方勾选与实际代码为准；后续修复进入学习-DJ-创作原子计划（`2026-09-12-learning-dj-creation-atomic-plan.md`）的 G 组任务。

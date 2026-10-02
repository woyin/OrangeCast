# U01–U05 理解快照工程验证记录

验证日期：2026-10-02。对象：剩余 47 项计划的 U01–U05；本记录不能替代全仓 V03 门禁或真实用户质量评分。未读取、改写真实 `.env`，浏览器使用临时数据库和临时目录。

## 交付合同

- 迁移 `0083_understanding_snapshots.sql`：理解头、不可变版本、精确 Reference、选择命令回执、显式 embedding 选择以及问答理解依赖。
- 保存与选择当前分别操作；均使用问题/理解头修订 CAS、UUID 请求身份及负载 hash。保存不会自动选择，也不会解决问题。
- 无来源也可保存 Owner 答案，不创建假 Source、Citation 或 OwnerNote。来源、笔记、重点、证据、文章 Reference 的确切版本在保存事务内验证。
- 默认 `local_only`；允许外发时聚合自身、全部引用来源及文章内嵌理解的权限。Reference 不能被用作来源引语。
- 当前版本进入检索、文章选材及问题冻结；历史只经显式历史读取。embedding 还必须选择该当前 snapshot ID，有引用时全部实际来源须处于已选索引范围。
- 来源 purge 保留 Owner 答案，清除 Reference 正文和其派生冻结材料投影，停止相关待执行文章/问答；理解向量即时失效。
- `/questions/{id}/understandings` 提供当前、历史差异、明确选择、完整旧版复制、笔记显式导入与模型草稿导入；模型导入不自动保存，必须再次确认。

## 当前验证

| 验证 | 实际结果及范围 |
| --- | --- |
| `go test ./internal/store ./internal/server ./internal/provider -run 'TestUnderstanding\|TestQuestionStudy\|TestQuestionRecoveryV4' -race -count=1` | 通过；store 35.469s、server 13.319s、provider 1.582s。该次包含 Source Reference 精确版本测试；其后补充有界输入、手工重点真实版本 0 与理解检索的问题标题投影；更新后的最终定向验证由下方补记。 |
| 理解、检索、发现、更新、embedding 联合 race | store 103.651s、server 17.296s，通过；后续理解业务改动由上行新的定向验证覆盖，未将旧证据标为最终全仓结果。 |
| `node testdata/understandings.cjs` | 本问题/理解头草稿命名空间、旧头保留、明确模型导入、确认复位、焦点及成功后清理通过。 |
| `node testdata/question-study.cjs` / `question-study-adopt.cjs` | 既有对话草稿及采用行为保持，均通过。 |
| 新理解 Store 集成测试 | 11 个集成测试（另有 4 组独立失败边界测试）：无来源、不可变/当前指针、UUID 重放与不同负载、CAS、并发唯一、写入故障回滚、完整引用权限交集、Source 精确版本、手工重点真实版本 0 与正文改变失效、旧笔记版本、purge/嵌套文章 redaction、无来源真实问答准入、显式 embedding 与当前改变失效、一致性数据库备份恢复。 |
| 理解 HTTP | 2 个基础测试及 3 组独立 HTTP 边界/引用/恢复测试：RequireAuth/CSRF、Owner 确认、UUID 重放、双窗口 409、明确选择、旧引用警告、分页、purge、坏输入及不完整恢复。 |

## 独立浏览器实测

使用 `agent-browser`，独立 session `cwp-u05`、`CWP_BROWSER_ACCEPTANCE=1` 临时 fixture、`127.0.0.1:18094`。这是实际浏览器操作，未调用真实 AI。

1. 创建无来源问题，保存 v1；保存后当前仍未选择。
2. 两个标签打开同一理解头，首标签保存，第二标签旧修订提交返回 `conflict`，原输入“旧窗口的不同答案必须保留”保留在编辑器中。
3. 明确选择 v1，保存 v2；当前仍为 v1，历史完整保留两份正文，显示回答变化。
4. 用最新编译的第二份临时 fixture 测试历史复制：回答、不确定之处、下一步完整回填，Owner 确认复位为 false，焦点在 answer。
5. 模型草稿到达时保留已有文字；明确导入后“AI待确认示例”仅进入编辑器，确认仍为 false，焦点在 answer，原稿 backup 存在。
6. 浏览器 `errors` 无错误输出。两份 fixture 均通过 stop file 正常退出，终端显示 PASS；浏览器 session 已关闭。未将浏览器工具长等待误判为服务终止。

原首份浏览器 fixture 485.406s、最新编辑器 fixture 569.426s，时长包括人工编排与并行工程等待，不是业务性能基准。

## 实际限制

- Store 一致性数据库备份恢复已覆盖；正式 v2 ZIP/整套恢复组合由 V01/E05 的组合验证归档，不能用这里的单库测试代替。
- 全仓覆盖率、lint、完整 race、构建和最终发布验收由根代理 V03 汇总。本记录没有宣称模块单独达到 95%；单独运行 `TestUnderstanding` 的覆盖诊断并不是全套覆盖率。
- 用户没有真实个人笔记；真人“理解更清楚”、真实模型有效性与费用及实体手机验收不能由合成 fixture 替代。工程测试不因这些外部条件中止。

## 最终模块补记

- 最新生产代码的 `go test ./internal/store ./internal/server -run '^TestUnderstanding' -race -count=1` 通过：store 34.081s、server 20.115s；包括手工重点真实版本 0、旧引用 Changed 提示和 HTTP 保留 Owner 回答测试。
- 新增独立 `understanding_http_failures_test.go` 后，`go test ./internal/server -run '^TestUnderstandingHTTP' -race -coverprofile=/tmp/cwp-u-http.cover -count=1` 通过（16.912s）。相对根代理 `/tmp/cwp-v4-coverage-current.out` 实际新增 35 条 server 已执行语句；这是模块补测贡献，不是全包覆盖率达标声明。
- 故障恢复用独立临时数据库真实 SQLite 缺表/缺当前对象场景验证响应，未修改生产行为或降低门禁；正常流程覆盖精确 note/source 选项、21 版本分页和来源 purge 后 Owner 答案保存。

## U03.3 来源清理后的真实浏览器补验

2026-10-02，独立 session `cwp-u03-purge`、`CWP_UNDERSTANDING_PURGE_BROWSER=1`，新增 `internal/server/understanding_browser_purge_test.go` 启动临时 SQLite/生产 Router 模板；未读取真实 `.env`，未调用模型。

1. 实际登录，打开问题 `fbe4f1e7-b15c-453b-97ad-e0cfbd7b3dbe` 的理解历史，v1 显示 Owner 答案“Owner答案：来源清理后仍保留我的解释。”及真实 note Reference 正文“我希望先理清来源，再形成自己的理解。”。
2. 通过 test-only 文件阶段让 fixture 对真实 episode 执行 `DELETE`，由正式数据库 purge trigger 清理 Reference；Go harness 实读 exact snapshot 验证 `Purged=true`、`Body=""`。
3. 浏览器重新打开同一历史页，当前和历史均显示来源清理提示；Owner 答案仍可读，原 Reference 正文不再出现在页面。
4. 真实 DOM 检查输出 `ownerAnswerVisible=true`、`purgeWarningVisible=true`、`oldReferenceBodyAbsent=true`；历史 `data-references` 中 exact v1 Reference 也为 `Body=""`、`Purged=true`，复制不会夹带已清理正文。
5. 浏览器 errors 无输出。截图 [清理前截图](../validation/v4/understanding-purge/cwp-u03-before-purge.png)、[清理后截图](../validation/v4/understanding-purge/cwp-u03-after-purge.png)；fixture 经 stop file 正常关闭，浏览器 session 已关闭。

这是 actual browser + 实际 purge，不以 HTTP 断言代替 UI 验收；来源清理阶段由 fixture 触发，未声称测试了资料删除按钮。

## 原生 SSR 冲突草稿恢复修复

V02 真实旅程发现原生表单收到 409 仅错误文字，JavaScript 的草稿保存不能替代 SSR 恢复。已新增 `understanding_recovery.html`：返回对应 400/404/409 的私有恢复表单，保留提交的有界回答、待确认条件、下一步、Reference JSON、模型策略/provider 和原问题/理解头修订、parent、UUID；不自动改成最新修订，重新确认 checkbox 为空。提示先打开当前理解并人工核对合并。

`TestUnderstandingSSRConflictRetainsExactSubmittedDraftIdentity` 验证 stale 409、所有原始身份字段、HTML逃逸、不落盘、JSON原短错误合同，以及坏修订/超限字段仍保留其他合法草稿。超过整表单限额无法可靠解析时仍返回原有 400，未伪造不完整提交。JSON客户端继续使用原 CWPForms 错误处理，成功 redirect 与 RequireAuth/CSRF不变。

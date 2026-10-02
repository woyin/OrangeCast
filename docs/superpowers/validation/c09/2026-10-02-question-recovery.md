# C09 问答组合验收证据（阶段记录）

新增 `internal/queue/question_recovery_v4_test.go` 三个父测试，使用临时 Store、真实 Worker 与本地 HTTP 模型协议桩。三来源条件分歧在生成/检查两阶段进入历史，每阶段有且只有一份准确用量 receipt；同 UUID 双窗口并发复用同一冻结轮次，不同负载拒绝；空范围、未确认建议与 local_only 均不创建付费任务或调用模型。

2026-10-02 执行：

```sh
go test ./internal/queue ./internal/store ./internal/server \
  -run 'TestQuestionRecoveryV4|TestQuestionStudyGeneration|TestQuestionStudyCommit|TestLegacyStudy|TestQuestionStudyScope' \
  -race -count=1 -json -timeout=8m
```

实际通过 37 个父测试（queue 22 / store 9 / server 6），另有 queue 14 个子测试。覆盖当前新旧问答生成/审校、已知响应 receipt 故障无新调用恢复、unknown 显式付费 attempt、stop/purge 与权限边界。不是全库 race 或最终质量门禁。

浏览器使用 agent-browser 独立 session `c09`，真实生产 router/Worker + 隔离临时 SQLite、loopback `127.0.0.1:18129`。配置 `CWP_BROWSER_STAGE=p11` 和确定性 `browser-generate/browser-review`，未读取 .env、未调用真实模型、未修改默认数据库。

实际旅程：登录 → 问题页 → 新会话 → 输入问题并确认范围 → 提交 → 在轮询中输入下一问草稿 → accepted 提示 → 明确刷新至会话 v3。两次截图显示草稿均为“下一问草稿保留”；刷新后显示已通过检查来源观点、生成/检查任务费用入口与冻结依据。

- [轮询与下一问草稿](question-poll-draft.png)
- [已审校回答与下一问草稿](question-accepted-draft.png)

浏览器与 harness 已正常关闭；`TestBrowserAcceptanceHarness` 通过，70.390s。截图只能证明这条确定性协议桩旅程。

仍待组合收口：旧播放器新持久 POST 的真实浏览器旅程、切页持续播放/转写竞争、理解 aggregate refs 新接口落稳后的权限/历史失效集成；真实模型输出、人工质量、真实个人资料明确独立待验。不能凭本记录勾选 C09 全部完成。

补充：`TestQuestionRecoveryV4UnderstandingReferenceWithdrawalBlocksDispatch` 单独 race 通过（16.563s）。真实 current understanding 的精确快照进入付费 Scope，理解引用独立来源的冻结 evidence；入队后撤回该聚合引用来源权限，Worker 无模型调用、无 receipt、无历史，验证并未仅检查问题显式关联来源。

第二次组合 race 在上述理解用例及新增 legacy 无引用/采用事务失败用例落稳后通过 **41 个父测试**（queue 25 / store 10 / server 6），queue 14 个子测试；另有 opt-in harness 默认跳过，不计通过数。

旧问答真实浏览器：独立 `TestLegacyStudyBrowserAcceptanceHarness`、18130、临时 SQLite，QA HTTP 桩生成/检查各延迟 1 秒。两轮正常问答均经检查后进入历史；pending 时通过实际 `CWPNavigation.visit('/questions')` 切页，原音仍 `paused=false/currentTime=0.824126`，pending command 留存；回源页恢复显示两轮回答。SQLite 核对生成/检查各 2 唯一 receipt，生成合计 input/output 64/24，检查 66/24。模拟损坏 envelope 后显示“明确发起新的付费尝试”，自动轮询未重发；手动点击后增加一个 failed job，仍一轮 unknown，未捏造准确用量。结束供应商调用合计 6（正常两轮各 2 + 未知初始 1 + 显式新尝试 1）。harness 通过 349.286s，浏览器/harness 已关闭。

- [旧问答切页持续播放](legacy-pending-playing.png)
- [未知响应显式新付费尝试](legacy-unknown-explicit.png)

发现并交付 owner 修复：下一问本地草稿在旧 StudyChat 切页后为空，目前尚未验收修复。早期 UI link 点击/URL 等待未完成的尝试不作为导航证据；最终成功导航证据来自实际应用导航方法。首次 harness 目录遗漏已由 owner 修复，重跑成功；不计为业务失败。

理解聚合引用用例已扩展为两个子场景并重新 race 通过（16.182s）：入队后撤权阻止 dispatch；已完成两阶段并确认一轮历史后撤回仅理解所引用来源权限，下一轮冻结材料中历史回答被排除，原 2 次调用保持不变。此新增历史子场景不计入前述 41 父 / 14 子整组旧数字。

旧 StudyChat 草稿缺口已修复并在 fresh fixture 复验通过：提交后立即编辑“下一问本地草稿”，切 `/questions` 时 `audio.paused=false`，单独 draft key 已持久保存；返回来源页 input 恢复该草稿；accepted 后 pending key 为 null，而 input 草稿保持不变。[修复后已审校回答与草稿](legacy-accepted-draft-fixed.png)。最终供应商调用严格 2（生成+检查），证明切页恢复没有重发。fresh harness 通过 87.170s，浏览器/harness 均已关闭。前述“尚未验收修复”描述为此前发现时状态，本段为实际修复验收结果。

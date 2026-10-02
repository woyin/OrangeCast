# F01–F06 质量案例验收记录

2026-10-02；在隔离临时数据库和自建内容上验证。没有使用默认数据库、个人笔记或真实模型；本记录证明流程与技术边界，不能替代真实文章质量的人工评分。

## 本轮实测

- `TestArticleQualityHTTPPrivacyAnchorsAndCAS`：Owner/CSRF、exact revision/段落哈希、UUID 重放、变更请求冲突；原生 HTML 表单无 JSON Accept、无需 JavaScript，服务端预填接纳/分类/退役 UUID，成功 303，409 页面保留原 request key/version/feedback identity 和 Owner 期望；接纳使用标题/原版本/类别/摘要反馈选项。
- agent-browser 的真实 Chromium 会话：原文章 revision 1 的段落入口进入质量页，文档 marker 保留，确认普通导航；记录自建反馈后新下拉选项自动加入并选择，显式接纳后版本为 1；使用旧版本再次提交得到实际冲突提示，导航至偏好并返回，Owner 期望和原 UUID 完全相同；退役提交得到“已保存”，再次普通导航显示 v1 retired。
- 浏览器退役截图：[退役截图](../validation/v4/quality-retired.png)（仅自建验收内容）。最终隔离浏览器 harness 由 stop file 正常关闭。
- JS VM 测试加载真实 CWPForms，覆盖注册/挂载/回挂、409/未知结果草稿、取消、成功版本推进。
- 定向 race：server/store/evalset/quality-eval 均通过；backup 在可读标题投影变化断言修正后单独 race 通过。恢复后 immutable Owner 字段一致，源 purge 清冻结正文和模型可发权限，标题投影显示原文章不可用。

## 运行与人工边界

`quality-eval` 默认仅预览；明确 `--run` 才从 .env 加载真实配置并隔离运行写作与审阅，独立 receipt 阻止未知结果自动付费重试。技术测试不赋予人工分数。没有真实个人笔记或人工五维评分时，报告仍标 pending；缺失 usage/price 保持 unknown，不能宣称真实质量回归通过。

CLI 与 evalset 的本轮覆盖率分别 96.0% 与 97.5%；全项目门禁由主代理统一执行。

## 真实文本接口与预算补验（同日）

已使用 `.env` 既有文本配置、独立 scratch 数据库、自建来源和实验笔记以及显式接纳案例，执行生产 `quality-eval --run`。没有读取默认数据库或发送个人资料，也没有修改 `.env`。私有目录 0700、数据库与回执 0600；仓库仅保留以下脱敏结果。

原预算规则的真实预检结果：新库 monthly budget 未配置，模型价格 unknown，因此允许显式调用但不虚构费用。生产 CLI 已复用正常预算账本：稳定输出目录/案例/阶段身份，在 HTTP 前预占并原子转 pending；已有 pending 身份不允许丢失回执后重新付费调用。配置预算时缺价/超额均在 HTTP 前拒绝；未知费用在 `usage_records` 保持 NULL。记账故障保留真实响应回执，缓存恢复只补账与结算，不重复模型调用。

真实写作只发送一次，6,853 ms 后收到响应：冻结模型 `google/gemini-3.8-flash-high`，响应模型 `gemini-3.8-flash`；模型合同不一致，所以写作判失败并保留真实正文和数字用量，不自动替换模型继续运行。输入用量 1,428，输出用量 983，retry 0；费用 unknown，usage receipt 共 1 条，预算 dispatch 为 pending_remote。审阅阶段没有发送，因此本轮不能声明“两阶段真实通过”。原目录再次运行返回既有失败，账本仍 1 条、没有新 HTTP。

本轮 write receipt SHA-256：`e423e8bf0a1c05165dc8d4b14ca859173e371b26b86cb8044798a2a1a9ab0b0a`。未赋人工分数；人工质量验收仍 pending。模型名称映射应由明确配置合同解决，不能把不一致响应伪装成冻结模型。

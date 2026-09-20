# R25/R26 验收记录：学习与创作闭环

日期：2026-09-21（Asia/Singapore）  
分支：`fix/learning-creation-recovery`  
浏览器：`agent-browser 0.26.0`，Chrome for Testing `148.0.7778.97`  
应用：测试专用浏览器夹具，监听 `127.0.0.1:18085`  
视口：桌面 `1280×900`；移动 `375×812`、`360×800`

## 测试边界

验收服务由 `internal/server/browser_acceptance_test.go` 在显式设置
`CWP_BROWSER_ACCEPTANCE=1` 时启动。它使用临时 SQLite 数据目录、真实生产
router/模板/队列/worker 和本地 WAV→MP3 媒体转码，只替换模型、TTS 和原始媒体
下载边界为确定性 Fake Provider。生产启动路径没有 Fake Provider 开关，避免测试
模型进入正式部署。

夹具服务启动后打印 `BROWSER_ACCEPTANCE_READY`，浏览器完成操作后通过
`CWP_BROWSER_STOP_FILE` 优雅退出。这个测试服务不会读取或修改仓库 `data/`。

## R25 浏览器结果

### 桌面学习路径

1. 登录夹具 Owner，打开 `/sources/episode/<fixture>/dj`。
2. 点击“自动播放全集”，页面进入“播放中”，先播放 AI 解说，再进入原音区间；暂停、上一段、下一段、停止和“继续听原节目”均可见且可操作。
3. 进入原音区间后，“收藏这段”和“记下我的理解”解除禁用。点击收藏得到“已保存”；通过浏览器 prompt 写入个人理解，得到“已保存”，播放器未被导航打断。
4. 将速度改为 `1.25×` 后暂停，刷新页面出现“继续听（上次位置）”；点击后实际 `HTMLMediaElement.playbackRate=1.25`，速度选择框同步显示 `1.25`。保存的 `plan_id` 和清单项位置用于恢复。
5. 页面控制台错误和浏览器错误采集均为空。

### 桌面创作路径

1. 打开 `/workbench`，在 R25 方案编辑卡片修改 OwnerClaim、提纲和风格，点击“保存新修订”，页面保持该修订。
2. 将确认按钮滚动到视口后点击“确认精确版本授权”，卡片变为 `CONFIRMED · REVISION V3 · CONFIRMED V3`。
3. 打开未审校文章内容包，页面明确显示“当前修订尚未通过交付门禁”，缺少主张审校和风格审校时不能导出。
4. 打开已通过审校的文章内容包，下载 Markdown 成功；文件包含正文和“来源”区块。点击“登记已发布”后页面显示“已登记创作历史”。

### 移动视口

| 视口 | 页面 | 结果 | 证据 |
|---|---|---|---|
| 375×812 | DJ 学习页 | `scrollWidth=375`、`clientWidth=375`，无横向溢出；标题、播放、速度、收藏和笔记控件可见 | [dj-375.png](evidence/2026-09-recovery/dj-375.png) |
| 360×800 | 创作工作台 | `scrollWidth=360`、`clientWidth=360`，无横向溢出；工作台表单可滚动访问 | [workbench-360.png](evidence/2026-09-recovery/workbench-360.png) |

### 本次发现并修复

- DJ 原音项此前把 `[]string` 直接用 Go 默认格式写入 `data-segments`，浏览器得到 `[seg-0001]`，无法解析为 JSON；现在通过 `json` 模板函数输出可解析值。
- 原音项此前缺少 `data-position/data-idx`，手动播放也没有携带片段身份；现在清单项和手动按钮都带稳定位置、highlight 和 Segment 身份。
- 播放进入可锚定原音后，收藏和个人理解按钮此前一直禁用；现在按当前条目的 Segment 身份同步解锁。
- 续听恢复了倍速，但速度选择框此前仍显示 `1×`；现在点击“继续听”后同步恢复显示保存的倍率。

## R26 真实质量与外部条件

| 项目 | 当前结论 | 证据/限制 |
|---|---|---|
| 真实中文长访谈 | 待验收 | R25 使用确定性中文夹具；未调用真实 Provider |
| 真实英文信息密集节目 | 待验收 | 未调用真实 Provider |
| 多窗归并、重点覆盖、引用支持、重复与限定条件 | 待验收 | 自动测试覆盖契约，未做真实模型输出抽样 |
| 两类文章可读性 | 待验收 | 未生成真实模型文章 |
| GROQ 按次调用 | 未执行 | 本地 `.env` 仅核实 `GROQ_API_KEY` 已设置；本轮没有未经确认的付费/外部调用 |
| OpenAI | 不可用 | `OPENAI_API_KEY` 未设置 |
| Kokoro 真实 WAV 听感 | 待验收 | `cloudwisepod tts-check` 报告 `kokoro` 不在 PATH；R25 使用 Fake Narration，仅验证播放器接线 |
| 手机锁屏/后台播放 | 待验收 | 本机没有真机设备；桌面 Chrome 移动视口不代表设备能力 |
| Owner 主观评分 | 待确认 | 没有把自动测试或模型结果冒充 Owner 验收 |

因此，R25 的桌面与移动交互验收完成；R26 的真实内容质量、真实 TTS、真机后台播放和 Owner 主观评分保持明确待验收。

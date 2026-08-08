# ADR-0019：Narration 解说音轨——为 Highlight 引入 TTS

状态：已确认
日期：2026-08-08
关联：推翻 ADR-0016 第 5 节"明确拒绝 TTS 朗读"的决定；在 ADR-0018 信息分层（PrimarySource / Derivative / CitedDerivative / GeneratedDerivative）之上扩展。

## 背景

ADR-0016（2026-08-04）为 AI DJ 模式引入 Highlight，并明确拒绝 TTS，给出三条理由：

1. CloudWisePod 是 EvidenceArchive，TTS 生成的语音是 AI 虚构的新音频产物，违背 EvidenceArchive 定位（ADR-0004）。
2. TTS 合成音色与原音交替会稀释证据感，让用户困惑"这段是原话还是 AI 说的"。
3. 主流 TTS 是付费 Provider，违背"默认零成本"承诺（ADR-0009）。

ADR-0016 末尾留了出口："未来若 Owner 坚持要 TTS，必须新开 ADR 明确记录违背 EvidenceArchive 定位的后果。" 本 ADR 即是那次坚持。

Owner 希望在 AI DJ 模式消费 Highlight 时，每段精华一开始有 AI 解说（朗读该 Highlight 的 Gist）作为"串场导入"，配合原音区间交替播放，获得更接近电台 DJ 的引导体验。

## 决策

引入新领域概念 **Narration（解说音轨）**，并正式推翻 ADR-0016 第 5 节的 TTS 禁令。三条反对理由在 ADR-0018 信息分层升级后全部消解（详见下文）。

### 1. Narration 的领域定位（CONTEXT.md「信息分层」）

Narration 是 GeneratedDerivative 的音频形态：AI 将一个 GeneratedDerivative（首版仅 Gist）的文字用 TTS 合成为音频产物。

- **只读 GeneratedDerivative，永不读可核验内容**。首版仅合成 Highlight.Gist（AI 重新组织的解说词，非逐字原文）。整集 Summary、KeyPoint、Quote 等可核验/忠实内容一律不读。这是守住"原话权威感"的关键线：TTS 的角色是 DJ 串场主持人（解说），不是内容播音员（朗读原话要点）。
- **Narration 不进入 EvidenceAudio，不作为 Citation 或 Reference 的核验依据**，仅供收听。与 EvidenceAudio 是对照关系：EvidenceAudio 是原音事实层的音频，Narration 是衍生层的 AI 解说音频，二者不可混用。
- **明确标注为 AI 生成音、非原音**。

被否决的替代：读整集 Summary（CitedDerivative，听觉上会伪装原话，污染原话权威感）；Cited/Generated 都读不区分（推翻上轮分级）。

### 2. 三条反对理由的消解

**第一条（违背 EvidenceArchive 定位）消解**：ADR-0018 信息分层升级已把 EvidenceArchive（事实层 = PrimarySource）和 Derivative（衍生层）正式分开。Narration 属衍生层，有明确的家，根本不在 EvidenceArchive 里。ADR-0016 写这条时（2026-08-04）还没有这个分层——那时所有东西挤在 EvidenceArchive，TTS 无处安放。现在分层已成，第一条理由的前提不再成立。

**第二条（稀释证据感）消解**：用"显著合成音色 + 固定开场白"在听觉通道复刻 Cited/Generated 的视觉分级。Narration 统一使用与原音明显不同的合成音色（播客原音千差万别，合成音一听可辨），且每段强制以"AI 解说："开场。这让 Owner 一耳朵就知道这是 AI 串场、不是主播原话，等同于上轮为文字建的视觉分级在音频通道的等价物。

**第三条（违背零成本）消解**：Narration 的默认 TTS Provider 是一个自托管的免费 TTS 引擎（见下节），与 Groq 同属"零成本"阵营。ADR-0016 写"主流 TTS 是付费"是 2026-08 的事实陈述，不是原则禁令；存在免费 TTS 且选它为默认，这条理由就不再适用。

### 3. TTS Provider：默认免费自托管，付费按单次授权

- **默认 Provider：Kokoro（自托管、Apache 2.0、~82M 参数）**，通过进程外 CLI 调用，与 Go 主二进制解耦。Kokoro 是当前免费 TTS 第一梯队质量，CPU/GPU 均可跑。选进程外而非内嵌库：隔离依赖、便于单独升级引擎、不污染 Go 二进制体积。
- **付费 TTS（OpenAI TTS / ElevenLabs 等）作为可选 Provider**，严格沿用 ADR-0009：按单次任务显式授权，绝不静默全局切换或失败后自动降级到付费。

被否决的替代：把 Narration 设为"第一个默认付费功能"破例（腐蚀 ADR-0009 底线，开滑坡）；把成本决策完全推给 Owner（甩锅）；放弃 Narration（功能价值归零）。

### 4. Highlight 加稳定 ID

每个 Highlight 携带程序生成的稳定 ID（Highlight.ID）。Narration 通过 highlight_id 关联到所属 Highlight。HighlightSet 刷新（重新生成高光）时，程序尽量保留旧 ID 或按 Citation 集合重映射，避免 Narration 错挂到错误区间。

被否决的替代：用 Gist 文本指纹做软关联（脆弱，Gist 文字一变就断；且同义 Gist 会碰撞）。

### 5. Narration 版本化粒度：按段独立

每个 Highlight 的 Gist 各有一段独立版本化的 Narration（ArtifactVersion `kind='narration'`，带 highlight_id），而非整集一组。换音色/模型时可只重生成某几段，满意的段落不受影响；TTS 合成有成本（即便免费本地引擎也是 CPU/时间），版本化避免重复付费、可回退。

被否决的替代：整集一组版本化（刷新强制全部重合成，浪费成本且覆盖满意段落）；不版本化覆盖更新（回退不了）。

### 6. 触发时机：紧接 Highlight 生成后自动合成

Narration 在 analyze 流水线末尾、Highlight 生成成功后自动合成，失败不阻塞（与 Highlight 同级容错：失败仅 log，主流程继续）。Narration 依赖 Highlight 产物（要读其 Gist），紧跟 Highlight 触发最自然，零额外流水线复杂度。

被否决的替代：单独 ProcessingJob（跨 Job 依赖复杂，且本地 Kokoro 合成秒级不必进队列）；按需在 DJ 页触发（首次进入页面是哑的，违背"Highlight 出现解说就在"的诉求）。

## 取舍

- **代价**：schema 增加 Narration（ArtifactVersion kind='narration'）+ Highlight.ID；引入 Kokoro 进程外依赖（部署需带引擎二进制/模型）；DJ 播放序列由"文字 Gist + 原音"扩展为"Narration 解说音轨 + 原音"交替；听觉分级（合成音色 + 开场白）需在合成时强制。
- **收益**：在不放宽 ADR-0008 证据契约、不破坏 ADR-0018 信息分层的前提下，为 Highlight 增加 AI 解说音轨，DJ 模式从"读字 + 听原音"升级为"听 AI 串场 + 听原音"；三条原反对理由全部消解而非破例；零成本底线由自托管 Kokoro 守住。

## 不变

- EvidenceAudio 仍是唯一的原音事实层音频，Narration 永不进入它、永不作为核验依据。
- ADR-0008 证据契约（Citation 可核验、金句逐字）一条不动。
- 默认零成本承诺（ADR-0009）由 Kokoro 守住，付费 TTS 仍按单次授权。
- TTS 永不读可核验内容（Summary/KeyPoint/Quote/原音区间）。

---

## 附录：实现层约束 R1–R4（2026-08-08 第二轮 grilling 结论）

以上决策定义领域语言；以下四条约束规定领域语言如何落到代码与 schema，作为实施的硬约束。

### R1. Kokoro 通过进程外 CLI 调用，接口抽象为 NarrationProvider

TTS 引擎通过进程外 CLI 调用（os/exec），与现有 audio.go 调用 ffmpeg/ffprobe 同构；不内嵌 ONNX 运行时（避免 C/C++ 依赖破坏纯 Go 二进制分发，违背 ADR-0003），不跑常驻 HTTP 服务（避免多进程生命周期管理）。Narration 合成收口在 provider 包的新接口 NarrationProvider（与 HighlightProvider 同构）：输入文本 + 音色，输出 wav 文件路径。Kokoro 是默认实现，付费 TTS（OpenAI/ElevenLabs）作为可选实现，沿用 ADR-0009 单次授权。

被否决的替代：内嵌 ONNX（破坏纯 Go 分发）；本地 HTTP 服务（常驻进程管理负担）；输出转 mp3（多一次 ffmpeg 转码，wav 无损且 Kokoro 原生输出，浏览器支持 wav）。

### R2. Narration 存独立 narrations 目录，不进备份包

NarrationDir = DATA_DIR/narrations（与 EvidenceDir 同级）。文件命名 {sourceType}_{sourceID}_{highlightID}_{version}.wav。物理隔离 EvidenceAudio——目录与 serve 路径均不混用，让"Narration 不进 EvidenceAudio"在物理层也成立。Narration 不进入备份包：它是可重新生成的衍生层产物，全新实例恢复后按需重合成；备份只保核心证据（EvidenceAudio + DB），符合 ADR-0010"备份必须能在全新实例恢复核心数据"——Narration 不属于必须恢复的核心。

被否决的替代：和 EvidenceAudio 同目录靠文件名前缀区分（混淆温床，命名写错即污染证据流）；存为 DB BLOB（SQLite 不适合二进制大对象，多版本累积膨胀）。

### R3. DJ 页引入自动连播模式，Narration 缺失时跳过不阻塞原音

DJ 页从"高光片段列表 + 手动播放"升级为"自动 DJ 连播"：一个"自动播放全集"按钮，按下后按顺序播放 Narration₁ → 原音区间₁ → Narration₂ → 原音区间₂...（结尾 KeyPoints 仍文字显示、不朗读，见第 1 节）。每段可暂停/跳过。每个 Narration 与原音区间旁也保留单独手动播放按钮。前端 JS 维护播放队列，一段 ended 自动接下一段。

Narration 缺失处理（硬约束）：Highlight 已生成但 Narration 合成失败/未装引擎时，自动连播序列跳过该段 Narration、直接播原音区间，并在 Gist 文字旁显示"AI 解说未生成"小标记。Narration 是增强而非依赖，绝不因缺失而阻塞原音消费。

被否决的替代：Narration 仅作孤立按钮（浪费音轨价值，无引导消费）；默认进页面就自动播（被现代浏览器 autoplay 策略拦截，需用户手势触发）。

### R4. 新表 narrations，二维版本空间（highlight_id × version）

不复用 artifact_versions，因为其 current 指针机制（source 表上的 current_X_version 列）无法表达"每个 highlight_id 各有一个 current"——这是二维版本空间（highlight_id × version），artifact_versions 是一维（source × kind → version 序列）。新表 narrations 天然二维：每行 (highlight_id, version)，current 取每 highlight_id 的 MAX(version)（不加 is_current 列，避免"多个 current"不一致风险）。schema：

```sql
CREATE TABLE narrations (
    id              TEXT PRIMARY KEY,
    source_type     TEXT NOT NULL,
    source_id       TEXT NOT NULL,
    highlight_id    TEXT NOT NULL,
    version         INTEGER NOT NULL,
    voice           TEXT NOT NULL,
    model           TEXT NOT NULL,
    relpath         TEXT NOT NULL,
    duration_seconds REAL NOT NULL,
    char_count      INTEGER NOT NULL,
    provider        TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_type, source_id, highlight_id, version)
);
CREATE INDEX idx_narrations_highlight ON narrations(source_type, source_id, highlight_id);
```

注意：artifact_versions 现有的 SetCurrentVersion/GetCurrentVersion 只覆盖 transcript 与 knowledge_card，KindHighlight 会落到默认 current_card_version——这是既有的指针机制缺陷，与本次升级无关，但实施时若触及应一并修正或单独立项。

被否决的替代：复用 artifact_versions 加 kind='narration'（current 指针无法扩展为"N 个 per-highlight current"；version 号在 N 段共享序列里语义不清）；复用但按 highlight_id 取最新（DJ 页查询扭曲，GROUP BY 复杂）。

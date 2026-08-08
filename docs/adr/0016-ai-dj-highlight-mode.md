# ADR-0016：AI DJ 模式与 Highlight

状态：已确认  
日期：2026-08-04

## 背景
Owner 希望让 AI 判断一集播客中"最有价值的部分"，以"AI 总结 + 原音播放"交替的方式快速消费高光内容，而不必听完完整一集。这与现有 Evidence-first 契约（ADR-0008：Citation 时间范围来自 Segment，不能由 AI 估算）有表面张力。

## 决策

### 1. 新增领域概念 Highlight（高光片段）
Highlight 是 AI 按价值密度选出的连续音频区间，作为独立的 ArtifactVersion（`kind = 'highlight'`）持久化，单独版本化、单独可刷新。Source 新增 `current_highlight_version` 指针。

### 2. Evidence-first 契约不破
Highlight 的 Citation 是一组 Segment ID 的集合，**程序取 min(start)–max(end) 算时间范围**，AI 只能选 Segment ID，不能自行估算时间。这和 Chapter/Quote 的 Citation 模式一致，Highlight 只是粒度更粗。

### 3. Highlight 结构
```
Highlight {
    Citations: []SegmentID   // 区间本身是 CitedDerivative，程序算时间范围
    Gist: string             // GeneratedDerivative：AI 生成的"为什么这段值得听"的说明（非逐字原文）
    References: []SegmentID  // Gist 的参考关系，程序算时间范围，AI 只能选 Segment ID
}
```
Gist 复用现有词汇（Chapter.Gist 同名同义），是 AI 重新组织的概括，非逐字原文。Gist 是 GeneratedDerivative，其与 Segment 的关联是 Reference（非 Citation），表示参考了这些片段但不声称逐字忠实。所属区间本身仍是 CitedDerivative。

### 4. DJ 播放序列
DJ 模式是一个**编排好的播放清单页面**，不是生成的音频文件：
- 每个 Highlight：显示 Gist（文字）+ 播放按钮（播放该区间的原 EvidenceAudio 片段）
- 结尾：显示整集 KnowledgeCard 的 KeyPoints（文字），不朗读
- **不做 TTS**：Gist 和 KeyPoints 都是页面文字显示，只有原音区间是音频播放

### 5. 不扩展产品边界
明确拒绝 TTS 朗读。理由：
- CloudWisePod 是 EvidenceArchive，不是内容生产工具。TTS 生成的语音是 AI 虚构的新音频产物，不是原音的证据，违背 EvidenceArchive 定位（ADR-0004）。
- TTS 合成音色与原音交替会稀释原音的"证据感"，让用户困惑"这段是原话还是 AI 说的"。
- 主流 TTS 是付费 Provider，违背"默认零成本"承诺（ADR-0009）。

## 取舍
- Highlight 独立 ArtifactVersion（而非 KnowledgeCard 字段）：DJ 高光判断和卡片内容分析是不同 AI 任务，prompt/模型/失败原因不同，解耦避免互相拖垮。
- 不做 TTS：牺牲了"纯音频体验"（耳朵全程不用看屏幕），换取保住产品边界、证据感、零成本。
## 后果
- Highlight 作为独立产物，schema 复用现有 `artifact_versions` + Source 指针，无新表。
- DJ 模式是只读编排层，不产生新音频，不增加 EvidenceAudio 负担。
- 未来若 Owner 坚持要 TTS，必须新开 ADR 明确记录违背 EvidenceArchive 定位的后果。

---

## 修订记录

- 2026-08-07：在信息分层升级（PrimarySource / Derivative / CitedDerivative / GeneratedDerivative / Reference）下正名 Gist 的关联类型。原文描述 Gist 的证据来自 Citations，但 Gist 一直是非逐字、重新组织语言的产物，与 Citation 的可核验语义冲突。本次修订将 Gist 明确为 GeneratedDerivative，关联类型改为 Reference；Highlight 区间本身的 Citation 不变。决策结论中 Highlight 独立版本化与 Gist 非逐字未变；但不做 TTS 一条后被 2026-08-08 修订（见下）推翻。

- 2026-08-08：第 5 节关于不做 TTS 的决定被 ADR-0019 推翻。ADR-0019 引入 Narration（解说音轨），为 Highlight 的 Gist 合成 TTS 解说音轨。三条原反对理由在 ADR-0018 信息分层升级后全部消解——Narration 属衍生层不进 EvidenceAudio（第一条）；显著合成音色加固定开场白实现听觉分级（第二条）；默认 TTS 改用自托管 Kokoro 守住零成本（第三条）。第 4 节 DJ 播放序列里关于不做 TTS 的部分相应失效，改为 Narration 解说音轨与原音区间交替，详见 ADR-0019。

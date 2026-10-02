# E04.3 / E05.3 真实浏览器导出验收

2026-10-02，agent-browser 独立 cwp-export-ui/cwp-export-reader sessions。使用最新同问题双用途 scratch harness http://127.0.0.1:18131，由 /root/question_recovery_acceptance 管理生命周期。不改生产代码和数据库既有来源/问题/预算。

实际 UI：登录 → 来源 original WAV 播放 → 顶栏学习成果导出 → 中文标题选择“学习问题：自建问题1：怎样核对主动回忆条件？”→ 历史版本/来源摘录 → 预览 → 明确确认 → 生成 → ready → 浏览器真实下载。

根 audio 连续播放有效第二轮：开始 t=75.570255 paused=false；预览完成 t=84.548040 paused=false 同一audio，URL ?preview_id=e08f9a83-9ba0-4627-ac6b-23e5ded76c5e；生成完成 t=85.205373 paused=false 同一audio，URL #export-c415dd80-cbe2-4eea-809a-3a8728efc1fe；后台ready t=94.512098 paused=false 同一audio；下载后 t=110.102321 paused=false 同一audio。证明不仅DOM一致且实际播放时间推进。初轮接近120秒自然结束，随后UI后退并恢复播放；初轮不作为生成不断播证据。

CLI特殊情况：offviewport click落到HTML，事件trace确认；scrollintoview后真正button click+submit顺利。没有生产UI bug修改。

实际下载 learning-export.zip SHA256 2f52d22f52f0b4957406519b4b0496203db8fc3eb412f8a1294ba446875fd78d。
原包完整解压至 unpacked，不改ZIP/Markdown/manifest。9个manifest清单文件全部尺寸/SHA256/UTF8匹配，36个Markdown内部链接全部真实目标存在，包含同article revision1/history_passed及revision3/passed、理解version1/history/version2/current。检查明细见 archive-proof.json。

独立 localhost18133静态reader.html只是读取/安全渲染真实原始Markdown并展示原文件hash，未伪造内容。实际浏览器打开index.md，点击第二篇中文文章，article-r3.md显示hash与manifest一致、中文正文/原始材料/关联；滚动点击来源链接进入真实source-r1.md，中文seg-1 20–40秒、seg-2 60–90秒及hash显示正确。截图 index-reader.png/article-reader.png/source-reader.png。

模型调用零新增：浏览器动作仅导出，本次未抓调用counter起始值，实际最终计数由fixture owner提供；不得由此次UI/ZIP证据单独推断。真实外部模型生成质量、个人笔记质量、实体手机不在本验收范围。

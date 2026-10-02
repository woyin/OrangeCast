# E04.3 / E05.3 Theme、撤回和缺失ZIP最终浏览器验收

日期 2026-10-02。新 opt-in TestLearningExportThemeBrowserHarness，独立 scratch DB及18135端口，未碰18131/C09最终同题反思fixture。使用已有 personalV4Seed、自建local WAV、IndexKeyPoints→SetKeyPointQualityStatus→CreateTheme→AddKeyPointToTheme 真实领域pipeline，2个来源/2个KeyPoint，无fake UUID Theme。Owner personal-v4@example.com 只限此scratch。

原生产缺口实测：旧build Theme成功ready后 ArchiveSource撤回来源，点ready下载导致普通anchor全页导航至/.../download，409文本页，根audio丢失same=false。原harness最终question=0/article=0，明确stop退出PASS。

小修：learning-exports.js受view scope下载fetch，401/登录redirect走既有清理/expire；校验ZIP MIME+attachment和100MiB加1MiB archive overhead；非2xx就地反馈、不跳页；成功blob触发下载并try/finally revoke；scopeabort不得迟到下载或改反馈。模板新增每export inline aria-live反馈。SSR下载端点/状态契约保留。

fresh修复build真实操作：原音source3播放，中文title下拉选择“主题：跨集主动回忆：来源与理解”，preview→明确确认→create→ready→browser真实blobdownload theme.zip成功。

实际连续播放：开始 t=10.111812 paused=false；创建后t=21.083711 paused=false sameaudio=true；blob下载后t=36.539982 paused=false same=true；missingZIP失败后t=73.794294 paused=false same=true；来源撤回失败后t=85.925926 paused=false same=true。来源撤回的是Theme source1，播放第三独立source3，不用失效音频模拟保持播放。

下载成功文件 theme.zip，SHA256 3ef4e5bf2d16ff95c7ff49c56682c841c184953ba085932ed6f69e08d3b7515c。5个manifest文件(index+2 KeyPoint+2 Source)全部大小/UTF8/SHA匹配，证明即时revoke不破坏实际下载。详见 archive-proof.json。

失败分支真实click：
- export16d4e5f8-f417-4275-ae8f-5025965c9d30的真实ZIP由harness os.Remove删除，点击原ready下载返回404，“导出或范围不存在，或当前不可下载。”就地显示。
- export1e2c0ec1-1307-46fa-98a1-e80cdd313ee2的来源通过store.ArchiveSource撤回，点击原ready下载返回409，“预览已过期或范围已改变，请重新预览并确认。”就地显示。
- 两分支保留相同viewURL、Theme选择74bbd5c5-a087-4ee5-8e62-9f5e2c0d2d06、includeDrafts=true、未保存“尚未保存的学习草稿：下载失败也应保留”；上述同audio持续推进。截图missing-file.png和withdrawn-source.png。

最终harness模型计数question=0 article=0，131.05s PASS；完整TestLearningExport目标race通过11.876s，Node涵盖真实CWPForms导航、409/404/network/401/loginHTMLredirect/successblob资源回收/abort。

收尾：写明确stop控制，worker/context/server清理退出，18135 lsof无监听，export-theme独立浏览器已close。证据保留/tmp/cwp-export-theme-fixed；没有改master文档或提交。已有问题ZIPreader阅读证据仍在/tmp/cwp-export-ui-proof-yrIys4。

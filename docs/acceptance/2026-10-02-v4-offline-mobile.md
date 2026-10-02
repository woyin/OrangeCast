# V4 离线学习实体手机验收

工程准备与实体设备验收分开记录。本表初始所有实体设备行均为待验证，没有使用桌面 viewport、合成录音或静态测试替代手机证据。不得为验收擅自部署。

## 环境登记

| 字段 | iOS Safari | Android Chrome |
| --- | --- | --- |
| 设备型号、OS、浏览器版本 | 待提供实体设备 | 待提供实体设备 |
| HTTPS URL/证书与当前源码 commit | 待登记 | 待登记 |
| 日期、时区、网络、空闲存储 | 待登记 | 待登记 |
| 原音 SHA、来源 snapshot、设备 namespace/epoch | 待登记（不记录 cookie/密钥） | 待登记 |

## 操作矩阵

| 场景 | 操作及证据 | iOS | Android |
| --- | --- | --- | --- |
| 明确授权 | 在线登录、访问 `/offline`，先确认默认禁用，再授权设备；授权24小时、scope `/`；记录实际SW/IDB能力结果 | 待验 | 待验 |
| 完整原音下载 | 下载真实本地 EvidenceAudio；对照SHA/size/快照；取消一次再明确重下；断网seek中间及EOF，坏Range416 | 待验 | 待验 |
| 冻结区间补听 | 明确下载已有补听区间，离线选择区间并seek；核对start/end边界、末尾暂停、刷新不自动播放与笔记绝对时间；同步后补听进度独立于原音进度 | 待验 | 待验 |
| 关页重开 | 离线关闭标签后从 `/offline` 重开；只在原namespace有效期允许读取，动态SSR/登录页不能从缓存伪装登录 | 待验 | 待验 |
| 后台15分钟 | 播放原音后锁屏/切换应用15分钟，记录是否停播/中断及恢复位置，不把平台中断记成成功 | 待验 | 待验 |
| 两段自然录音 | 使用既有VoiceDraft录两段自然内容，记录权限、格式、时长、保存/恢复；ASR必须另行明确点击 | 待验 | 待验 |
| 文字保存与明确同步 | 离线写一条听后笔记、一条整理草稿和原音进度；重开保留；回网先核授权再明确同步 | 待验 | 待验 |
| 响应丢失去重 | 人为丢弃第一次已提交响应，重放同UUID/hash；数据库只有一条笔记/回执，旧CAS与异hash冲突保留文字 | 待验 | 待验 |
| 来源删除/替换 | 另一在线设备删除来源/替换原音后回网；先撤回核验再播放和同步；旧文字可复制不改绑来源 | 待验 | 待验 |
| 授权到期/时钟回退 | 缩短隔离夹具授权或改变夹具时间；到期停止媒体、内容锁定，待同步文字保留锁定 | 待验 | 待验 |
| 退出/换登录 | 两标签播放并保留待同步草稿；原站明确登出，媒体即时停、Cache/IDB/内存清理；新session不继承旧内容 | 待验 | 待验 |
| 配额/驱逐 | 模拟QuotaExceeded、删除Blob但保留metadata；显示未保存/重下，不能显示ready或删未同步文字 | 待验 | 待验 |
| 安全更新 | 两标签分别播放/录音/同步在途，安装新SW等待；全部空闲明确确认后更新；坏新静态hash保留旧可用缓存 | 待验 | 待验 |

每项记录实际通过/失败/受平台限制、可复核截图或日志、修复后重新验证时间。离线设备无法实时得知服务端撤回，24小时是应用降低旧副本可用窗口的策略，不是防本机篡改的机制。浏览器配额和驱逐不保证永久存储。

## 工程证据入口

- `go test ./internal/store -run '^TestOffline' -count=1 -race`
- `go test ./internal/server -run '^TestOfflineHTTP' -count=1 -race`
- `node scripts/offline-browser.test.js`
- `python3 scripts/offline-browser-acceptance.py`：桌面浏览器与独立HTTP夹具。
- `CWP_OFFLINE_GO_BROWSER=1 CWP_OFFLINE_GO_STOP=/tmp/cwp-offline-go-stop go test ./internal/server -run '^TestOfflineGoBrowserHarness$' -v -count=1 -timeout=30m`：启动显式启用的隔离Go验收服务（先移除上次停止文件）。另终端执行 `python3 scripts/offline-go-browser-acceptance.py http://127.0.0.1:18777 /tmp/cwp-offline-go-browser-evidence`；完成后创建停止文件结束服务。该驱动检验真实Go路由和数据库，仍属于桌面夹具证据。
- 独立浏览器应对照网络、Cache、IndexedDB和数据库，不仅观察页面文案。

实体设备当前未提供，所有上述手机结果保持待验。工程实现或桌面确定性检查通过不能将这些行改为通过。

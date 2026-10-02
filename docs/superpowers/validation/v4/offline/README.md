# V4 离线学习桌面验收证据

2026-10-02。本文记录当前冻结离线代码的工程与桌面夹具结果，不表示全项目最终关门。实体 iOS Safari、Android Chrome 均待验，见[手机操作矩阵](../../../../acceptance/2026-10-02-v4-offline-mobile.md)。没有数据库、ZIP、私有节目或用户语料落盘；音频为测试生成的8秒 WAV，文字和截图均为自建夹具。

## 当前结果与适用范围

| 层次 | 实际结果 | 范围与限制 |
| --- | --- | --- |
| Node 模块测试 | 24/24，exit 0 | 本次再次执行并保存[完整终态日志](node-24.log)。涵盖期限、范围、命令hash/CAS/ack、同文件/同包重试、发布失败和暂存清理；不能代替浏览器或手机。 |
| 独立 HTTP + 真 Chromium/IDB/SW | 26 PASS，exit 0 | `scripts/offline-browser-acceptance.py`，Chrome148。真实原生IDB事务/存储与SW；HTTP业务由Python夹具模拟，不是Go业务。schema2/实际IDB版本2只读复制、owner隔离、同ID复用零请求、发布tx主动abort保旧ready、staging清理通过。完整stdout未单独落盘；[工具终态观察记录](chromium-26-observation.json)保存session53638的exit0及26个实际断言，不冒称原始完整日志。 |
| 真实 Go Router + SQLite + Chromium | driver exit 0；harness PASS 154.44秒 | fresh build，`TestOfflineGoBrowserHarness`与`scripts/offline-go-browser-acceptance.py`。真实服务路由、临时数据库与native按钮操作。保存[driver PASS摘录](real-go-driver-excerpt.log)、[脱敏数据库结果](database-summary.json)。[154.44秒工具终态摘录](go-harness-observation.txt)来自session24234，不冒称原始完整Go进程日志。 |
| Go 定向 race | server24.395秒/store20.864秒，PASS | `go test ./internal/server ./internal/store -run '^TestOffline' -count=1 -race -timeout=5m` 当前执行结果；显式browser harness默认跳过。全项目最终race由主任务另登记。 |

真实Go失回执夹具在事务提交后断开连接，浏览器重试7次仍仅1条笔记、1条整理草稿和1条原音进度；两个已有补听区间各自保存0.75秒/revision3、1.5秒/revision1，原音revision1不被覆盖。专用shell真实区间1–2秒两边seek夹紧、末尾暂停、暂停seek1.5秒后刷新精确暂停恢复均通过。来源版本变化409清下载并锁定可复制待同步文字，明确退出清设备私有数据。

root播放器暂停补听后，通过真实header链接导航`/offline`已通过，未先调用`CWPListening.stop()`。此前直接automation goto曾超时，原因未完全确定；没有把那次超时作为产品链接导航失败，也没有删去该证据限制。

## 故障注入与浏览器设置的区别

无serviceWorker API、注册SecurityError、IDB.open失败是document API故障注入：均显示在线回退、隐藏私有内容，恢复后原3条pending仍在，动态SSR仍可正常使用。这三项不是“浏览器设置已禁用SW API”。

另实际调用CDP `Network.setBypassServiceWorker(true)`：已下载原音请求到Go返回404，dashboard返回200且`fromServiceWorker=false`，草稿不变。浏览器API仍存在，所以bypass不能冒称API不支持。记录见[脱敏网络结果](chrome-network-settings.json)。mainpage block `/sw.js`不能阻止worker进程注册，未将该失败尝试记为成功。

## 已查看的当前截图

以下六张真实Go截图均由执行代理查看；协调代理还亲自查看这些六张及schema图，内容均为自建夹具。

- [专用shell冻结补听选择](screenshots/real-go-shell-excerpt.png)
- [无SW API的在线回退](screenshots/real-go-unsupported-sw.png)
- [注册权限错误回退](screenshots/real-go-denied-sw.png)
- [IDB失败回退](screenshots/real-go-failed-idb.png)
- [CDP绕过后的真实在线SSR](screenshots/real-go-chrome-worker-bypassed.png)
- [来源变化后的锁定文字](screenshots/real-go-source-change-locked.png)
- [实际IDB版本2的只读复制出口](screenshots/synthetic-schema2-copy.png)

截图能支持界面状态，范围字节/事务去重与独立进度还须结合测试断言和JSON，不能只凭截图推断。元数据UUID已统一为fixture-id，正文/anchor JSON已从结果JSON移除，音频SHA保留以核对自建字节。所有保存制品的[SHA256清单](artifact-sha256.json)可核验文件一致性。

## 重现入口

`node scripts/offline-browser.test.js`；`python3 scripts/offline-browser-acceptance.py`。真实Go先用`CWP_OFFLINE_GO_BROWSER=1 CWP_OFFLINE_GO_STOP=/tmp/cwp-offline-go-stop go test ./internal/server -run '^TestOfflineGoBrowserHarness$' -v -count=1 -timeout=30m`启动（移除旧stop文件），再运行`python3 scripts/offline-go-browser-acceptance.py http://127.0.0.1:18777 /tmp/cwp-offline-go-browser-evidence`；最后创建stop文件停止服务。两个本轮浏览器/HTTP/Go测试进程均已清理。

代码冻结后没有再改产品代码，仅测试与证据资料新增。手机后台15分钟、自然录音、真实HTTPS与系统驱逐仍未实测；viewport、桌面WAV和document故障注入均不填手机通过状态。

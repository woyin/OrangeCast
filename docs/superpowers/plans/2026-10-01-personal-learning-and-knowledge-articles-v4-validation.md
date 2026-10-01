# 第四轮实施与验证记录

实施起点：2026-10-01，HEAD `3024642f1263c4c03c5d097dfa504ba652950cf6`，迁移0069。用户已授权按设计、计划及任务清单完成全部开发。现有未提交修改为同轮设计文档及领域术语，予以保留。

## 证据规则

只记录当前执行的验证。第三轮race、覆盖率与浏览器结果为继承证据，不作为第四轮通过记录。测试仅使用独立临时目录；不迁移或写入Owner默认数据库，不修改`.env`。

工程、真实模型、Owner人工质量及实体手机状态分别登记。Owner尚无个人笔记，真实3集/20笔记/2问题及人工评分待日常积累；不重复索取资料。

## 固定输入

`internal/evalset/personal_learning_v4.go`提供3集自建来源、20条笔记、2个问题和40条固定检索查询；包含来源/Owner身份、不同适用目的、反例、没有效果数字和5条无答案查询。查询的相关段落必须存在于同一语料。哈希由`LearningEvaluationCorpus.Fingerprint()`产生，配置与生产代码指纹随实际评测写入报告。

## 当前技术记录

| 任务 | 验证 | 结果 | 提交/证据 |
| --- | --- | --- | --- |
| A01 | 固定自建语料完整性与既有PersonalLearning评测通过（evalset/server当前运行） | 技术验证完成；独立待验见下 | `1c7c7ca` |

| A02 | TestCommonFormActions通过：禁用状态恢复、单请求、401/409、未知结果及卸载隔离 | 技术验证完成；独立待验见下 | `bcb68d3` |

| A03 | KnowledgeForm、KnowledgeGeneration、KnowledgeUpdate当前测试通过；实际Node脚本保留父稿草稿、命名action与局部导航 | 技术验证完成；独立待验见下 | `d9f1efd` |

| A04 | 当前QuestionForm、DailyReview、LearningReview、AutomationForms及共用接口测试通过；实际Node验证查看依据不清解释和CAS草稿身份 | 技术验证完成；独立待验见下 | `3a58e4a` |

| A05 | 首页/偏好CAS与去重、问题暂停/删除降级、0069升级/备份恢复当前race通过；实际360/375px无溢出、选择保存与10次持续播放导航通过 | 技术验证完成；独立待验见下 | `50b7019` |

| A06 | 当前RunControl/RunLane race及0068升级通过；NonModelJob核对预算/用量SQL成功且记录0、不解析Provider、已知结果不重复组装 | 技术验证完成；独立待验见下 | `126717b` |

| A07 | 同机1万/5万固定索引20次FTS与采样内存基线通过；首页/队列真实浏览器资源观察、固定Fake评测与语料指纹登记 | 技术验证完成；独立待验见下 | `d050836` |

| R01 | 当前验证：TestListeningReflectionDraft race 通过（11.748s）；真实0069前缀升级、独立备份恢复、并发开始、重放、CAS、上限、取消、到期、purge清理；无模型调用。 | 技术验证完成；独立待验见下 | `f388fee` |

| R02 | 当前验证：TestListeningReflection store/server race 通过（12.077s/6.274s）；提交故障注入证明笔记、关系、修订、回执全部回滚；已保存重放唯一、异hash/旧修订409、删除笔记不重建、问题/来源失效、HTTP鉴权/CSRF/64KiB/只读GET。 | 技术验证完成；独立待验见下 | `53b2dfe` |

| R03 | 当前验证：实际Node控制器与HTTP测试通过（0.745s）；独立真实浏览器验证A播放/B浏览、刷新捕获与文字恢复、正式保存归属A、唯一笔记和本地清理、360/375px无横向溢出；截图已查看，harness 129.954s；物理手机待验。 | 技术验证完成；独立待验见下 | `b4b2d3c` |

| R04 | 当前验证：VoiceReflection/VoiceHTTP/VoiceRecovered/ListeningReflection store/server race 通过；Node验证录音使用整理A的捕获、明确采用所选栏、其他Owner栏保持、迟到建议不改栏、录音不支持时保留原捕获；故障采用回滚、重放、独立Voice保存拦截、统一笔记与删除音频；浏览器故障综合证据归R06。 | 技术验证完成；独立待验见下 | `d6ae413` |

| R05 | 当前验证：ListeningReflection/DashboardNextActions/LearningHome race 通过（server 6.411s/store 17.080s）；SharedPlaybackController/RootEditor/Logout 通过；真实audio ended关闭/启用对照、无记录/无焦点变化、真实循环与控制睡眠期限对照，截图已查看；复用0070已安装默认关闭偏好，无额外模型迁移。 | 技术验证完成；独立待验见下 | `26e7513` |

| R06 | 当前相关store/server/queue race通过；真实v2备份/恢复/重放/purge、Worker到期清理、实际浏览器offline开关与双窗口CAS/本地副本恢复，跨窗口退出旧播放缺口修复后重测/login且spec=null/正文副本空/本地清理；截图已查看。物理手机与真实Owner质量独立待验。 | 技术验证完成；独立待验见下 | `bf521e9` |

## 独立待验

- 真实个人资料和Owner五维评分：待验证。
- 新embedding连接及真实召回收益：待验证。
- 新问题对话和用途模式真实接口：待验证。
- iOS Safari/Android Chrome实体手机、自然录音、HTTPS、后台15分钟与离线：待验证。

## M0 当前浏览器证据

独立p9测试实例，真实Router/模板/SQLite/Worker，确定性Provider；当前相关race结果store19.051s、queue8.464s、server8.023s。浏览器创建自建问题、保存首页当前选择，360/375px页面宽度与scrollWidth相等；保存后焦点回到page-view。播放中10次局部导航每次为同一audio且paused=false。截图已实际查看，保存在忽略目录`data/eval/personal-learning-v4/m0/`。测试实例已停止，真实模型和手机未参与。

FTS测量：Apple M4 / darwin arm64，固定10,000/50,000条合成索引，20次；第一轮p95为37.92/181.9ms。后续内存采样测量单列，采样Go Heap不能表示进程RSS峰值。浏览器此轮首页资源2ms、队列1ms是本地单次观察，不是p95，也不含外部供应商耗时。

内存采样轮：同机同语料20次，FTS p95 38.92/184.6ms，采样Go HeapInuse最大5.867/5.891MiB，约266KB/4,341分配每次。语料指纹`6c35837f84aa1659d969fce4056e5f2471bb8b631e11196d87a10a014a72e4de`；当前evalset固定输入检查通过，人工及设备为空。M0迁移落点0070学习偏好、0071新运行类别，历史前缀0067/0068/0069升级及偏好一致性备份恢复已检查。

### M1 编辑器浏览器证据（R03）

- 当前独立 scratch harness，2026-10-01；自建来源 A `df4fe57d-3191-41db-b3b9-5b3a985bbd79`，浏览来源 B `635e402b-7742-4d7f-befa-244d9d4c98c9`。
- 点击实际播放和“听后整理”，捕获原音位置 `0.014088` 秒（实际读数）；浏览 B 保留 A、原文字与同一 audio DOM。刷新 B 后恢复 A 捕获及原文字，没有自动模型请求。
- 通过真实表单保存一条笔记，记录归属 A，返回唯一笔记 `b8f84b8d-dbf7-4602-978a-2536f07b89f6`；编辑器关闭、本地条目清除。360/375px scrollWidth 分别等于 viewport。
- 截图已实际查看：`data/eval/personal-learning-v4/m1/reflection-360.png` 与 `reflection-saved-375.png`。harness 已停止（PASS 129.954s）；未访问默认数据库或真实模型。
- Node 实际控制器另验证了服务器问题修订冻结、保存响应丢失后的同 UUID/同 payload 重试、当前会话本地恢复、退出清理及无锚点拒绝。物理手机与真实 Owner 样本仍待验。

### M1 可选自然结束提示（R05）

- 独立浏览器通过真正的 audio `ended`（自建12秒音频，显式 seek 到末段后实际播放结束）验证：默认关闭时不提示，整理记录数0；在首页明确启用并保存后自然结束显示轻提示，焦点未变化，记录仍0。截图 `data/eval/personal-learning-v4/m1/natural-end-375.png` 已实际查看。
- 浏览器实际循环0–1秒期间持续播放且没有新自然结束提示；关闭循环后用控制器将测试睡眠期限缩短到30ms，实际 tick 后暂停且 sleepExpired=true，无提示。这是控制期限测试，不是物理手机15分钟后台验证。
- 初次使用 CLI 点击后立即导航未确认设置落库；最终通过真实 DOM 表单激活并核对服务器设置后重做。只记录上述已确认结果，不把未完成设置的样本计为通过。
- Node 验证 DJ/片段/循环/错误/未自然结束/睡眠到期等十种排除场景；提示不创建请求、默认关闭、同一结束标识只提示一次。

### M1 故障与恢复（R06）

- 当前 store/server/queue 相关 race：PASS 11.984s / 9.132s / 5.525s；包含真正 v2 tar.gz backup.Create/Restore（正式笔记、确认问题关系、私人草稿、原请求回执，恢复后重放不重复），恢复后 Source purge 清除正文/回执与笔记。0069前缀升级证据见R01。本轮新表迁移至0073，无远端模型。
- Worker 启动及每小时清理到期整理；测试实际 Run 启动维护清除私人正文，没有 Provider 调用。
- 独立真实浏览器 offline on/off：离线保留文字和冻结 Source，未确认 start UUID 保留；联网明确同步后同ID且服务器只有1个草稿。截图 `m1/reflection-offline-recovered-375.png` 已实际查看。
- 双标签窗口：B 同步到修订3，A旧修订409且保留A文字；明确读取服务器后展示B文字，下方仍保留A副本，没有自动采用。截图 `m1/reflection-conflict-375.png` 已实际查看。新副本在保存/丢弃/退出清空。
- 此测试发现旧的跨窗口播放退出缺口：之前另一标签草稿清空而 audio spec 尚在。已修复单向 storage reset → expire(false)（不会广播循环）；重测真实另标签点击退出后第一标签到/login，根 spec=null、整理正文/副本为空、本地条目=null。该scratch harness PASS 109.903s并已停止。
- 丢失保存响应的同UUID/同payload再次核对、双击/输入保护、迟到ASR、权限不支持/取消边界由实际控制器与事务测试支持；不将自建/Fake工程测试计为真实Owner或真实供应商质量。物理手机录音/后台/离线仍待产品验收。

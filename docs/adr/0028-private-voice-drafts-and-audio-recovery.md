# 私有语音草稿、转录事实与备份v2

个人录音是VoiceNoteDraft，不是SourceUpload或SourceEvidence。录音按钮在权限等待前冻结当前播放的节目、快照、原音位置和DJ身份。实际录音开始才暂停节目；停止后需要Owner明确继续播放。录音、本地文字与ASR建议分别保存，只有显式保存才以OwnerReflection进入已有笔记体系。问题关系、历史锚点、幂等笔记和草稿状态在一个事务提交。

单条录音上限5分钟／20MB。服务器用ffprobe确认真实容器及唯一音频流，用受限ffmpeg解码量计算时长，兼容没有duration元数据的浏览器WebM。归一化到16kHz单声道PCM WAV，私有文件使用服务器UUID、0600权限与受控目录。不能读取任意路径、视频流或远程播放列表。浏览器按MediaRecorder能力选择格式；录音或存储能力不足时保留文字入口。[MediaRecorder](https://developer.mozilla.org/en-US/docs/Web/API/MediaRecorder)、[getUserMedia](https://developer.mozilla.org/en-US/docs/Web/API/MediaDevices/getUserMedia)、[ffmpeg时长限制](https://ffmpeg.org/ffmpeg.html#Main-options)。

语音转录使用现有transcribe队列，source_type=voice_note只是任务对象身份，不是内容Source类型。供应商、模型、连接指纹、原始音频指纹、文字修订及音频分钟预估在明确转写时冻结。VOICE_ASR_*可配置独立音频端点，默认沿用当前转录设置；POD文本连接不被当作ASR连接。连接改变阻止外发；有合法响应断点时可无文件、无凭据恢复本地应用。

通用ASR在请求前持久化remote_call_started；响应先存完整断点（包含artifact JSON会省略的model/usage），再幂等记账，再提交业务结果。没有已知响应的已发请求不会自动重发。Groq音频上传不使用隐式HTTP重试。Source转录的不可变版本、current指针、依据失效、分析续接与complete结果原子提交；个人录音只保存独立文字建议。删除草稿后的迟到结果不恢复内容，已发生用量仍保留。未知结果显式重试创建新attempt；已知响应恢复继承原付费身份。

ASR模型覆盖单独作用于音频适配器。OpenAI默认gpt-4o-mini-transcribe使用json；whisper-1使用verbose_json及segment时间戳。无时间戳响应适合个人转写，但不能制造节目Segment或直接继续来源分析。供应商没有报告model时仍记unknown。[官方转录接口](https://developers.openai.com/api/reference/cli/resources/audio/subresources/transcriptions/methods/create)。

音频分钟预估与文本token价格分表。Owner在语音笔记页面为确切provider/model登记每分钟预估价格；月预算缺少价格时阻断。该价格仅用于全局事务性预算预占；未登记实际音频token计量价格或没有可靠实际计量时，费用保持未知。已验证文件时长、实际报告token和已知费用分别存储，不能将秒数或预估当作文本token或供应商账单。

本地IndexedDB最多10条／50MB，按认证会话的单向指纹隔离，退出或另一窗口登出时停止麦克风并清理。恢复、切页和GET不上传或转写。后台/崩溃中断可能只保留已送达的录音块，恢复界面明确提示检查。服务器未保存草稿7天到期；保存后录音默认删除，显式保留才长期存储。删除意图先入数据库，文件清理失败可重试；一小时以上无数据库引用的中间文件会被回收。

备份格式升级到v2，可恢复v1。CLI明确传入语音目录，数据库快照中已保存且明确勾选保留的录音按指纹和长度校验并进入独立voice-notes清单；临时草稿录音、清单外、已删除及无引用文件不进入长期备份。恢复后的临时草稿可保留文字和历史位置，页面按实际文件状态明确提示录音不可用。存在录音却未传目录时备份失败，不能静默丢失。恢复校验路径、普通文件类型、重复项、长度和哈希，私有录音保持0600权限。旧schema升级不改写历史笔记或人工锚点。

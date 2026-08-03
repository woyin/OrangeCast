# ADR-0014：EvidenceAudio 按时长自适应码率

EvidenceAudio 仍以 16kHz、单声道 MP3 持久保存，但不再固定为 64kbps。为满足 Groq 约 25MB 的单文件转录上限，系统依据原始音频时长选择最高可用标准码率，并以 22MiB 预算为 multipart 请求留出余量：约一小时内容通常为 48kbps，较长内容进一步降低。若 16kbps 仍无法容纳，任务明确失败并要求后续分段转录；不伪造不完整 Transcript。该选择以长内容较低的回听音质换取 EvidenceAudio 既可长期核验又可作为同一份转录输入，保留分段转录作为未来扩展而非悄然截断内容。

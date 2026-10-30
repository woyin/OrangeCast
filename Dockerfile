# 构建阶段：纯 Go 静态编译（modernc.org/sqlite 无 cgo，可 CGO_ENABLED=0 静态链接）
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/orangecast ./cmd/orangecast

# 可选目标 kokoro（docker build --target kokoro）：在 Debian 上带上 Kokoro TTS（解说音轨）。
# onnxruntime 无 musl 轮子，故不用 alpine；模型权重不进镜像，运行时挂载到 /models。
# 默认目标仍是文末的 alpine 阶段（发布镜像不变）。
FROM python:3.12-slim-bookworm AS kokoro
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata ffmpeg \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir kokoro-onnx soundfile
COPY --from=builder /out/orangecast /app/orangecast
COPY scripts/kokoro scripts/kokoro_cli.py /app/scripts/
WORKDIR /app
VOLUME ["/app/data"]
ENV DATA_DIR=/app/data PORT=8080 KOKORO_BINARY=/app/scripts/kokoro KOKORO_VENV=/usr/local KOKORO_MODEL_DIR=/models
EXPOSE 8080
ENTRYPOINT ["/app/orangecast"]

# 运行阶段：alpine + ca-certificates + ffmpeg（音频转码适配 Groq 上传限制）
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata ffmpeg
COPY --from=builder /out/orangecast /app/orangecast
WORKDIR /app
# 单一持久数据目录（ADR-0010）：SQLite + evidence + tmp + backups 全在 /app/data
VOLUME ["/app/data"]
ENV DATA_DIR=/app/data
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/app/orangecast"]

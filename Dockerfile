# 构建阶段：纯 Go 静态编译（modernc.org/sqlite 无 cgo，可 CGO_ENABLED=0 静态链接）
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/cloudwisepod ./cmd/cloudwisepod

# 运行阶段：alpine + ca-certificates + ffmpeg（音频转码适配 Groq 上传限制）
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata ffmpeg
COPY --from=builder /out/cloudwisepod /app/cloudwisepod
WORKDIR /app
# 单一持久数据目录（ADR-0010）：SQLite + evidence + tmp + backups 全在 /app/data
VOLUME ["/app/data"]
ENV DATA_DIR=/app/data
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/app/cloudwisepod"]

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
# 数据目录：SQLite 库 + 上传音频临时文件
VOLUME ["/app/data", "/app/tmp"]
ENV DB_PATH=/app/data/cloudwisepod.db
ENV TEMP_DIR=/app/tmp
EXPOSE 8080
ENTRYPOINT ["/app/cloudwisepod"]

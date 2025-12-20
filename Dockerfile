# -----------------------------
# 1️⃣ Builder Stage
# -----------------------------
FROM registry.cn-hangzhou.aliyuncs.com/devflow/golang:1.25 AS builder

WORKDIR /app

ENV GOPROXY=https://goproxy.cn,direct

# 提前复制 go.mod / go.sum，提高缓存命中率
COPY go.mod go.sum ./
RUN go mod download

# 复制整个项目
COPY . .

# 编译 DevFlow Controller 主程序
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o devflow-controller cmd/main.go

# -----------------------------
# 2️⃣ Runtime Stage
# -----------------------------
FROM alpine:3.19

WORKDIR /app

# 安装证书，保证 https 请求可用
RUN apk add --no-cache ca-certificates bash

# 复制二进制
COPY --from=builder /app/devflow-controller .

# 创建非 root 用户
RUN adduser -D devuser
USER devuser


ENTRYPOINT ["./devflow-controller"]
# Build stage
# 注意:go.mod 要求 go >= 1.26.1,这里 float 到最新 1.26.x patch (golang:1.26-alpine),
# 自动跟进 patch 安全更新但不主版本漂移。
FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO disabled — pure Go binary, runs on any glibc/musl distro
RUN CGO_ENABLED=0 GOOS=linux go build -o docker-host-master .

# Final stage
FROM alpine:latest

WORKDIR /app

# 装基础工具:tzdata 校时 + ca-certs 调 https
RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Shanghai

COPY --from=builder /app/docker-host-master .
# 配置全走 env vars,镜像里不带任何配置文件

# token store JSON 文件 + audit log 目录
RUN mkdir -p /var/lib/docker-host-master /var/log/docker-host-master

# (audit log + token store 目录在上面已经一并 mkdir)

EXPOSE 8090

CMD ["./docker-host-master"]

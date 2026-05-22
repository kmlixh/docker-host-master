# Build stage
FROM golang:1.23-alpine AS builder

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

# 创建 audit log 目录
RUN mkdir -p /var/log/docker-host-master

EXPOSE 8090

CMD ["./docker-host-master"]

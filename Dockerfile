# 构建阶段
FROM golang:1.27.3-alpine3.21 AS builder
WORKDIR /app

# 设置非交互模式，避免 tzdata 安装时需要用户输入
ENV TZDATA_NONINTERACTIVE=true

# 安装构建依赖
RUN apk update && apk add --no-cache tzdata make ca-certificates && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone

# 设置 Go 代理为国内镜像，解决网络问题
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=off

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN make build

# 运行阶段
FROM alpine:3.21.3
WORKDIR /app

# 设置非交互模式，避免 tzdata 安装时需要用户输入
ENV TZDATA_NONINTERACTIVE=true

# 更新包索引并安装运行时依赖
RUN apk update && apk add --no-cache tzdata ca-certificates && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone

# 创建非 root 用户运行应用
RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

COPY --from=builder /app/bin/dataEncrypt .
COPY --from=builder /app/configs ./configs

# 设置文件权限
RUN chown -R appuser:appgroup /app

USER appuser

EXPOSE 8000 50051
CMD ["./dataEncrypt", "-conf", "./configs/config.yaml"]
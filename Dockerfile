# 构建阶段：必须与 go.mod 的 go 1.27.3 对齐；用 -alpine 浮动 tag，
# 不要写死 -alpine3.21（阿里云加速器上没有该 tag，会 403）
FROM golang:1.27.3-alpine AS builder
WORKDIR /app

# 版本号：默认 dev，正式构建用 --build-arg VERSION=$(git describe --tags --always) 注入。
# 通过 make 的变量传给 ldflags（-X main.Version），容器日志与 trace 里的 service.version 才有值。
ARG VERSION=dev

# 设置非交互模式，避免 tzdata 安装时需要用户输入
ENV TZDATA_NONINTERACTIVE=true

# 安装构建依赖
RUN apk update && apk add --no-cache tzdata make ca-certificates git && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone

# 设置 Go 代理为国内镜像，解决网络问题
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=off

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# 显式传入 VERSION，覆盖 Makefile 里基于 git describe 的取值：构建上下文已忽略 .git，
# 否则 make 会输出 "git: No such file" 且版本为空。
RUN make build VERSION=${VERSION}

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

COPY --from=builder /app/bin/logisticsServer .
COPY --from=builder /app/configs ./configs

# 设置文件权限
RUN chown -R appuser:appgroup /app

USER appuser

EXPOSE 8000 50051
CMD ["./logisticsServer", "-conf", "./configs/config.yaml"]
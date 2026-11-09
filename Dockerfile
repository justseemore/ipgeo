# syntax=docker/dockerfile:1
#
# ipgeo HTTP 服务镜像
#
#   docker build -t ipgeo-server:0.1.0 .
#   docker run --rm -p 8080:8080 ipgeo-server:0.1.0
#
# 可选构建参数：
#   WITH_DATA=true|false   是否把 xdb 烤进镜像（默认 true）
#   IP2REGION_BASE=...     数据源地址（可换内网镜像）
#   DATA_VERSION=...       数据版本标记，仅用于失效「下载层」缓存（CI 每天传日期）
#   VERSION=...            注入 main.version
#
#   docker build --build-arg WITH_DATA=false -t ipgeo-server:nodata .
#   docker buildx build --platform linux/amd64,linux/arm64 -t you/ipgeo-server:0.1.0 --push .

# ---------------------------------------------------------------- 1. 编译
# 固定用构建机平台 + GOOS/GOARCH 交叉编译：构建 arm64 不需要 QEMU 模拟。
ARG GO_VERSION=1.26
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

WORKDIR /src
ENV CGO_ENABLED=0

# 先只拷依赖清单：改代码不会让依赖层失效
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -buildvcs=false \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/ipgeo-server ./cmd/server

# ---------------------------------------------------------------- 2. xdb 数据
# 数据与 CPU 架构无关，固定构建机平台，避免两套架构重复下载 122MB。
FROM --platform=$BUILDPLATFORM alpine:3.21 AS data
ARG WITH_DATA=true
ARG IP2REGION_BASE=https://github.com/fa1seut0pia/ip2region-xdb/releases/latest/download
# 仅用于击穿下载层缓存：同一天重复构建命中缓存，跨天强制拉最新数据。
ARG DATA_VERSION=1
RUN set -eu; \
    mkdir -p /data; \
    echo "data version: ${DATA_VERSION}"; \
    if [ "$WITH_DATA" = "true" ]; then \
      for f in ip2region_v4.xdb ip2region_v6.xdb; do \
        echo "==> 下载 $f"; \
        wget -q -T 180 -O "/data/$f" "$IP2REGION_BASE/$f"; \
        sz=$(wc -c < "/data/$f"); \
        [ "$sz" -ge 1048576 ] || { echo "$f 体积异常: $sz bytes" >&2; exit 1; }; \
        echo "    ok: $sz bytes"; \
      done; \
    fi

# ---------------------------------------------------------------- 3. 运行时
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 -s /sbin/nologin app \
 && mkdir -p /data && chown 10001:10001 /data

COPY --from=build /out/ipgeo-server /usr/local/bin/ipgeo-server
COPY --from=data  /data/            /data/

ENV IPGEO_ADDR=":8080" \
    IPGEO_V4_XDB="/data/ip2region_v4.xdb" \
    IPGEO_V6_XDB="/data/ip2region_v6.xdb" \
    IPGEO_LRU_ENTRIES="200000" \
    IPGEO_WATCH_INTERVAL="30s" \
    TZ="Asia/Shanghai"

USER 10001:10001
EXPOSE 8080

# 用二进制自身探活，运行时可换成 scratch/distroless
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD ["/usr/local/bin/ipgeo-server", "-healthcheck"]

ENTRYPOINT ["/usr/local/bin/ipgeo-server"]

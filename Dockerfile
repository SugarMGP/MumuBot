# syntax=docker/dockerfile:1

# 前端产物与架构无关，在构建机架构上完成
FROM --platform=$BUILDPLATFORM oven/bun:1.4.4-alpine AS assets
WORKDIR /src/web
COPY web/package.json web/bun.lock web/bunfig.toml ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

# go-webp 依赖 libwebp，cgo 必须按目标架构构建
FROM --platform=$TARGETPLATFORM golang:1.26.5-alpine AS build
RUN apk add --no-cache build-base libwebp-dev libwebp-static
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=assets /src/internal/web/assets/dist/ internal/web/assets/dist/
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
# 整份静态链接，产物不依赖任何系统动态库
ENV CGO_LDFLAGS="-static -lwebp -lsharpyuv -lm"
RUN CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags=netgo,osusergo -trimpath -ldflags="-s -w -X mumu-bot/internal/version.Build=${VERSION}" -o /out/mumu-bot .

FROM alpine:3.23
ENV TZ=Asia/Shanghai
RUN apk add --no-cache ca-certificates su-exec tzdata && \
    addgroup -S mumu && adduser -S -G mumu mumu
WORKDIR /app
COPY --from=build /out/mumu-bot /app/mumu-bot
COPY --chmod=755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN mkdir -p /app/stickers /app/media && chown -R mumu:mumu /app
EXPOSE 7468
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/app/mumu-bot"]

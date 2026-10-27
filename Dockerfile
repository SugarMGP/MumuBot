# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM oven/bun:1.3.14-alpine AS assets
WORKDIR /src
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile
COPY internal/web/assets/src/ internal/web/assets/src/
COPY internal/web/views/ internal/web/views/
COPY scripts/ scripts/
RUN bun run gen:icons
RUN bun run build

# 模板生成与架构无关，留在构建机执行，避免目标架构下重复生成
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS gen
WORKDIR /src
RUN go install github.com/a-h/templ/cmd/templ@v0.3.1020
COPY . .
COPY --from=assets /src/internal/web/assets/dist/ internal/web/assets/dist/
COPY --from=assets /src/internal/web/views/icons.templ internal/web/views/icons.templ
RUN templ generate ./internal/web/views

# go-webp 依赖 libwebp，cgo 必须按目标架构构建
FROM --platform=$TARGETPLATFORM golang:1.26.5-alpine AS build
RUN apk add --no-cache build-base libwebp-dev libwebp-static
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=gen /src/internal/web/views/ internal/web/views/
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
# 整份静态链接，产物不依赖任何系统动态库
ENV CGO_LDFLAGS="-static -lwebp -lsharpyuv"
RUN CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X mumu-bot/internal/version.Build=${VERSION}" -o /out/mumu-bot .

FROM alpine:3.23
ENV TZ=Asia/Shanghai
RUN apk add --no-cache ca-certificates su-exec tzdata && \
    addgroup -S mumu && adduser -S -G mumu mumu
WORKDIR /app
COPY --from=build /out/mumu-bot /app/mumu-bot
COPY config/config.example.yaml /app/config-defaults/config.yaml
COPY config/persona_group.prompt /app/config-defaults/persona_group.prompt
COPY config/persona_private.prompt /app/config-defaults/persona_private.prompt
COPY config/mcp.example.json /app/config-defaults/mcp.json
COPY --chmod=755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN mkdir -p /app/config /app/stickers /app/media && chown -R mumu:mumu /app
EXPOSE 7468
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/app/mumu-bot"]

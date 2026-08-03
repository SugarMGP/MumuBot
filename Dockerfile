# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM node:22-alpine AS assets
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY internal/web/assets/src/ internal/web/assets/src/
COPY internal/web/views/ internal/web/views/
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS build
WORKDIR /src
RUN go install github.com/a-h/templ/cmd/templ@v0.3.1020
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=assets /src/internal/web/assets/dist/ internal/web/assets/dist/
RUN templ generate ./internal/web/views
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/mumu-bot .

FROM alpine:3.23
ENV TZ=Asia/Shanghai
RUN apk add --no-cache ca-certificates su-exec tzdata && \
    addgroup -S mumu && adduser -S -G mumu mumu
WORKDIR /app
COPY --from=build /out/mumu-bot /app/mumu-bot
COPY config/config.example.yaml /app/config-defaults/config.yaml
COPY config/persona.prompt /app/config-defaults/persona.prompt
COPY config/mcp.example.json /app/config-defaults/mcp.json
COPY --chmod=755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN mkdir /app/config /app/stickers && chown -R mumu:mumu /app
EXPOSE 7468
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/app/mumu-bot"]

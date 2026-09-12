# syntax=docker/dockerfile:1
FROM golang:1.25.10-bookworm AS go-build
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY gen ./gen
COPY migrations ./migrations
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    set -eu; mkdir /out; \
    for name in ingest entity task dispatcher search opctl search-admin gateway-simulator executor-simulator verify web-gateway demo-init; do \
      CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/$name ./cmd/$name; \
    done

FROM debian:bookworm-slim AS backend
RUN groupadd -g 10001 meshops && useradd -u 10001 -g 10001 -M meshops
WORKDIR /app
COPY --from=go-build /out/ ./
COPY configs ./configs
COPY testdata/sources ./testdata/sources
COPY migrations ./migrations
COPY deploy/demo/configs ./deploy/demo/configs
USER 10001:10001
ENTRYPOINT ["/app/demo-init", "exec"]

FROM canal/canal-server:v1.1.8 AS canal
COPY --from=go-build /out/demo-init /app/demo-init
ENTRYPOINT ["/app/demo-init", "canal", "/alidata/bin/main.sh"]
CMD ["/home/admin/app.sh"]

FROM node:24.15.0-bookworm-slim AS web-build
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
RUN npm run build

FROM nginx:1.28-alpine AS web
COPY deploy/demo/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=web-build /web/dist /usr/share/nginx/html
EXPOSE 8080

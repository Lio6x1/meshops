FROM golang:1.25.10-bookworm
WORKDIR /src
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    mkdir /out && go test -race -json ./... > /out/unit-race.jsonl && \
    go test -race -c -o /out/bus.test ./internal/bus && \
    go test -race -c -o /out/state.test ./internal/state

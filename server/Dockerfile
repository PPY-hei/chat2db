ARG GO_IMAGE=golang:1.25-alpine
ARG ALPINE_IMAGE=alpine:3.21

FROM ${GO_IMAGE} AS builder

WORKDIR /src

RUN apk add --no-cache gcc musl-dev

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/chat2db-server ./cmd/server

FROM ${ALPINE_IMAGE}

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S chat2db && \
    adduser -S chat2db -G chat2db

WORKDIR /app

COPY --from=builder /out/chat2db-server /app/chat2db-server

RUN mkdir -p /app/data/tasks && chown -R chat2db:chat2db /app

USER chat2db

EXPOSE 8080

VOLUME ["/app/data"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/health >/dev/null || exit 1

ENTRYPOINT ["/app/chat2db-server"]

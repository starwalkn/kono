# Stage 1: Build Go kono binary
FROM golang:1.25.4-bookworm AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

ARG TARGETOS
ARG TARGETARCH

RUN make all GOOS=${TARGETOS} GOARCH=${TARGETARCH}

# Stage 2: Final minimal image
FROM debian:12-slim AS runtime

WORKDIR /app

COPY --from=builder /app/.bin/kono /usr/local/bin/kono

COPY --from=builder /app/build/plugins /usr/local/lib/kono/plugins
COPY --from=builder /app/build/middlewares /usr/local/lib/kono/middlewares

EXPOSE 7805/tcp

STOPSIGNAL SIGTERM

ENTRYPOINT ["kono"]
CMD ["serve"]

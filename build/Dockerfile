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

# Install LuaJIT and some utilities
RUN apt-get update && apt-get install -y \
    procps \
    tini \
    luajit \
    luarocks \
    build-essential \
    liblua5.1-0-dev \
    && rm -rf /var/lib/apt/lists/*

# LuaJIT dependencies
RUN luarocks --lua-version=5.1 install lua-cjson

COPY lua /usr/local/lib/kono/luaworker

ENV LUA_PATH="/usr/local/lib/kono/luaworker/?.lua;;"

RUN mkdir -p /tmp

COPY --from=builder /app/.bin/kono /usr/local/bin/kono

COPY --from=builder /app/build/plugins /usr/local/lib/kono/plugins
COPY --from=builder /app/build/middlewares /usr/local/lib/kono/middlewares

COPY build/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 7805/tcp

STOPSIGNAL SIGTERM

ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
CMD ["serve"]

# Stage 1: Build dashboard
FROM node:18 AS dashboard-builder

WORKDIR /app

COPY dashboard/static/package.json ./
COPY dashboard/static/tsconfig.json ./

RUN npm install

COPY dashboard/static/app.ts ./
COPY dashboard/static/index.html ./
COPY dashboard/static/style.css ./
COPY dashboard/static/fonts ./fonts

RUN npx tsc

# Stage 2: Build Go kono binary
FROM golang:1.25.4-bookworm AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH

RUN make all GOOS=${TARGETOS} GOARCH=${TARGETARCH}

# Stage 3: Final minimal image
FROM debian:12-slim AS runtime

WORKDIR /app

COPY --from=builder /app/.bin/kono /bin/kono
COPY --from=builder /app/build/plugins /kono/plugins
COPY --from=builder /app/build/middlewares /kono/middlewares

COPY --from=dashboard-builder /app/dist /dashboard/static/dist
COPY --from=dashboard-builder /app/index.html /dashboard/static/index.html
COPY --from=dashboard-builder /app/style.css /dashboard/static/style.css
COPY --from=dashboard-builder /app/fonts /dashboard/static/fonts

EXPOSE 7805/tcp 7806/tcp

STOPSIGNAL SIGTERM

CMD ["/bin/kono", "serve"]

# Stage 1: Build Go tokka binary
FROM golang:1.24.2-bullseye AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN make all

# Stage 2: Final minimal image
FROM gcr.io/distroless/base-debian12 AS runtime

WORKDIR /app

COPY --from=builder /app/.bin/tokka /bin/tokka
COPY --from=builder /app/build/plugins /tokka/plugins
COPY --from=builder /app/build/middlewares /tokka/middlewares

EXPOSE 8090 8095

CMD ["/bin/tokka"]

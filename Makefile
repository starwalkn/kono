GOOS=linux
GOARCH=amd64
PLUGIN_OUT=build/plugins
MIDDLEWARE_OUT=build/middlewares

.PHONY: all build plugins clean lint

all: build plugins

build:
	mkdir -p .bin
	CGO_ENABLED=1 go build -o .bin/tokka ./cmd/main.go

plugins:
	mkdir -p $(PLUGIN_OUT)
	mkdir -p $(MIDDLEWARE_OUT)
	CGO_ENABLED=1 go build -buildmode=plugin -o $(MIDDLEWARE_OUT)/logger.so ./builtin/middlewares/logger/middleware.go
	CGO_ENABLED=1 go build -buildmode=plugin -o $(MIDDLEWARE_OUT)/recoverer.so ./builtin/middlewares/recoverer/middleware.go
	CGO_ENABLED=1 go build -buildmode=plugin -o $(MIDDLEWARE_OUT)/compressor.so ./builtin/middlewares/compressor/middleware.go

	CGO_ENABLED=1 go build -buildmode=plugin -o $(PLUGIN_OUT)/camelify.so ./builtin/plugins/camelify/plugin.go
	CGO_ENABLED=1 go build -buildmode=plugin -o $(PLUGIN_OUT)/snakeify.so ./builtin/plugins/snakeify/plugin.go

clean:
	rm -rf build

lint:
	golangci-lint run

test:
	go test -v -coverprofile=coverage.out ./...
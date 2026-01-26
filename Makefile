GOOS ?= linux
GOARCH ?= amd64
PLUGIN_OUT=build/plugins
MIDDLEWARE_OUT=build/middlewares

.PHONY: all build plugins clean lint test

all: clean build plugins

build:
	mkdir -p .bin
	CGO_ENABLED=1 go build -o .bin/tokka ./cmd/gateway/main.go

plugins:
	mkdir -p $(PLUGIN_OUT)
	mkdir -p $(MIDDLEWARE_OUT)

	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=1 go build -buildmode=plugin -o $(MIDDLEWARE_OUT)/logger.so ./builtin/middlewares/logger
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=1 go build -buildmode=plugin -o $(MIDDLEWARE_OUT)/recoverer.so ./builtin/middlewares/recoverer
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=1 go build -buildmode=plugin -o $(MIDDLEWARE_OUT)/compressor.so ./builtin/middlewares/compressor
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=1 go build -buildmode=plugin -o $(MIDDLEWARE_OUT)/auth.so ./builtin/middlewares/auth

	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=1 go build -buildmode=plugin -o $(PLUGIN_OUT)/camelify.so ./builtin/plugins/camelify
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=1 go build -buildmode=plugin -o $(PLUGIN_OUT)/snakeify.so ./builtin/plugins/snakeify

clean:
	rm -rf build/middlewares build/plugins .bin

lint:
	golangci-lint run

test:
	go test -v -coverprofile=coverage.out ./...

GOOS ?= linux
GOARCH ?= amd64
PLUGIN_OUT=build/plugins
MIDDLEWARE_OUT=build/middlewares

.PHONY: all build plugins

all: build plugins

build:
	mkdir -p .bin
	CGO_ENABLED=1 go build -o .bin/tokka ./cmd/gateway/main.go

plugins:
	mkdir -p $(PLUGIN_OUT)
	mkdir -p $(MIDDLEWARE_OUT)

	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=1 go build -buildmode=plugin -o $(MIDDLEWARE_OUT)/logger.so ./builtin/middlewares/logger/middleware.go
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=1 go build -buildmode=plugin -o $(MIDDLEWARE_OUT)/recoverer.so ./builtin/middlewares/recoverer/middleware.go
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=1 go build -buildmode=plugin -o $(MIDDLEWARE_OUT)/compressor.so ./builtin/middlewares/compressor/middleware.go

	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=1 go build -buildmode=plugin -o $(PLUGIN_OUT)/camelify.so ./builtin/plugins/camelify/plugin.go
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=1 go build -buildmode=plugin -o $(PLUGIN_OUT)/snakeify.so ./builtin/plugins/snakeify/plugin.go

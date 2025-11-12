GOOS=linux
GOARCH=amd64
PLUGIN_OUT=build/plugins
MIDDLEWARE_OUT=build/middlewares

.PHONY: all build plugins clean lint

all: build plugins

build:
	mkdir -p build
	CGO_ENABLED=1 go build -o build/bravka ./cmd/main.go

plugins:
	mkdir -p $(PLUGIN_OUT)
	mkdir -p $(MIDDLEWARE_OUT)
	CGO_ENABLED=1 go build -buildmode=plugin -o $(MIDDLEWARE_OUT)/logger.so ./builtin/middlewares/logger/middleware.go
	CGO_ENABLED=1 go build -buildmode=plugin -o $(MIDDLEWARE_OUT)/recoverer.so ./builtin/middlewares/recoverer/middleware.go

clean:
	rm -rf build

lint:
	golangci-lint run

test:
	go test -v -coverprofile=coverage.out ./...
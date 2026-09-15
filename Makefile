.DEFAULT_GOAL := help

GO ?= go
BIN := bin/natop
VERSION ?= dev
SERVER ?=
CONFIG ?=
ARGS ?=
IMAGE ?= natop:local
NETWORK ?= bridge
DOCKER_ARGS ?=
ARCH ?= $(shell $(GO) env GOARCH)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: help build build-linux run demo test vet check fmt docker-build docker-run docker-demo clean

help: ## Show available commands and examples
	@awk 'BEGIN {FS = ":.*## "; printf "\nnatop\n\n"} /^[a-zA-Z_-]+:.*## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@printf '\nExamples:\n  make demo\n  make run SERVER=nats://localhost:4222\n  make run CONFIG=examples/connections.yaml\n  make docker-run NETWORK=my-network SERVER=nats://my-nats:4222\n\n'

build: ## Build the standalone binary at bin/natop
	@mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) .

build-linux: ## Cross-compile Linux; ARCH=amd64 or arm64 (defaults to host architecture)
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/natop-linux-$(ARCH) .

run: build ## Build and run; accepts SERVER=, CONFIG=, and ARGS=
ifneq ($(strip $(CONFIG)),)
	./$(BIN) --config "$(CONFIG)" $(ARGS)
else
ifneq ($(strip $(SERVER)),)
	./$(BIN) --server "$(SERVER)" $(ARGS)
else
	./$(BIN) $(ARGS)
endif
endif

demo: build ## Run an interactive demo without NATS
	./$(BIN) --demo $(ARGS)

test: ## Run focused tests, including disposable embedded NATS servers
	$(GO) test ./...

vet: ## Run Go's static checks
	$(GO) vet ./...

check: vet ## Run static checks and race-enabled tests
	$(GO) test -race ./...

fmt: ## Format Go source
	$(GO) fmt ./...

docker-build: ## Build the Docker image; IMAGE= and VERSION= are configurable
	docker build --build-arg VERSION="$(VERSION)" -t "$(IMAGE)" .

docker-run: docker-build ## Run in one Docker network; NETWORK= SERVER= [CONFIG=] [DOCKER_ARGS=]
ifneq ($(strip $(CONFIG)),)
	docker run --rm -it --network "$(NETWORK)" -e TERM=xterm-256color -v "$(abspath $(CONFIG)):/config/config.yaml:ro" $(DOCKER_ARGS) "$(IMAGE)" --config /config/config.yaml $(ARGS)
else
	docker run --rm -it --network "$(NETWORK)" -e TERM=xterm-256color $(DOCKER_ARGS) "$(IMAGE)" --server "$(if $(SERVER),$(SERVER),nats://localhost:4222)" $(ARGS)
endif

docker-demo: docker-build ## Run the demo in Docker
	docker run --rm -it -e TERM=xterm-256color "$(IMAGE)" --demo $(ARGS)

clean: ## Remove locally built binaries
	rm -rf bin

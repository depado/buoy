.DEFAULT_GOAL := build

export CGO_ENABLED = 0

VERSION = $(shell git describe --abbrev=0 --tags 2> /dev/null || echo "0.1.0")
SUFFIX = $(shell git describe --exact-match --tags > /dev/null 2>&1 || echo "-dev")
BUILD = $(shell git rev-parse HEAD 2> /dev/null || echo "undefined")
BUILDDATE = $(shell LANG=en_us_88591 date)
DAEMON = buoy
CLIENT = buoyctl
LDFLAGS = -ldflags "-X 'github.com/depado/buoy/internal/version.Version=$(VERSION)$(SUFFIX)' \
		-X 'github.com/depado/buoy/internal/version.Build=$(BUILD)' \
		-X 'github.com/depado/buoy/internal/version.BuildDate=$(BUILDDATE)' -s -w"

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: daemon client ## Build both binaries

.PHONY: daemon
daemon: ## Build the buoy daemon
	go build $(LDFLAGS) -o $(DAEMON) ./cmd/buoy

.PHONY: client
client: ## Build the buoyctl CLI
	go build $(LDFLAGS) -o $(CLIENT) ./cmd/buoyctl

.PHONY: install
install:
	go install $(LDFLAGS) ./cmd/buoy
	go install $(LDFLAGS) ./cmd/buoyctl

.PHONY: tmp
tmp: ## Build and output the binary in /tmp
	go build $(LDFLAGS) -o /tmp/$(DAEMON) ./cmd/buoy
	go build $(LDFLAGS) -o /tmp/$(CLIENT) ./cmd/buoyctl

.PHONY: docker
docker: ## Build the docker image
	docker build -t $(DAEMON):latest $(if $(filter undefined,$(BUILD)),,-t $(DAEMON):$(BUILD) )-f Dockerfile .

.PHONY: release
release: ## Create a new release on Github
	goreleaser release

.PHONY: snapshot
snapshot: ## Create a new snapshot release
	goreleaser release --snapshot --clean

.PHONY: lint
lint: ## Runs the linter
	golangci-lint run

.PHONY: test
test: ## Run the test suite
	CGO_ENABLED=1 go test -race -coverprofile="coverage.txt" ./...

.PHONY: clean
clean: ## Remove the binaries
	if [ -f $(DAEMON) ] ; then rm $(DAEMON) ; fi
	if [ -f $(CLIENT) ] ; then rm $(CLIENT) ; fi
	if [ -f coverage.txt ] ; then rm coverage.txt ; fi

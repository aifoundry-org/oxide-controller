.PHONY: all build link

all: build link

BINDIR ?= bin
ARCH ?= $(shell go env GOARCH)
OS ?= $(shell go env GOOS)
CONTROLLER_GENERIC ?= $(BINDIR)/oxide-controller
CONTROLLER ?= $(CONTROLLER_GENERIC)-$(OS)-$(ARCH)

build: $(CONTROLLER)

$(BINDIR):
	@mkdir -p $@

$(CONTROLLER): $(BINDIR)
	@go build -o $@

link: $(CONTROLLER) $(CONTROLLER_GENERIC)

$(CONTROLLER_GENERIC): $(CONTROLLER)
	@ln -sf $(notdir $(CONTROLLER)) $@

test:
	go test -v ./...

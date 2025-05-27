.PHONY: all build link

all: build link

BINDIR ?= bin

# determine my architecture and OS
MYARCH = $(shell go env GOARCH)
MYOS = $(shell go env GOOS)

# default to the current architecture and OS
ARCH ?= $(MYARCH)
OS ?= $(MYOS)
CONTROLLER_NAME ?= oxide-controller
CONTROLLER_GENERIC ?= $(BINDIR)/$(CONTROLLER_NAME)
CONTROLLER ?= $(CONTROLLER_GENERIC)-$(OS)-$(ARCH)
TARGET ?= /usr/local/bin/$(CONTROLLER_NAME)

IMAGE_NAME ?= aifoundryorg/oxide-controller

PLATFORMS ?= linux/amd64 linux/arm64 linux/riscv64 darwin/amd64 darwin/arm64
CONTROLLERS := $(foreach plat,$(PLATFORMS),$(CONTROLLER_GENERIC)-$(subst /,-,$(plat)))

build-all: $(CONTROLLERS)

build: $(CONTROLLER)

install: build
	@cp $(CONTROLLER) $(TARGET)

$(BINDIR):
	@mkdir -p $@

$(CONTROLLER_GENERIC)-%: $(BINDIR)
	@GOOS=$(word 1,$(subst -, ,$*)) GOARCH=$(word 2,$(subst -, ,$*)) go build -o $@ ./cmd/

link: $(CONTROLLER) $(CONTROLLER_GENERIC)

$(CONTROLLER_GENERIC): $(CONTROLLER)
ifeq ($(ARCH),$(MYARCH))
ifeq ($(OS),$(MYOS))
	@ln -sf $(notdir $(CONTROLLER)) $@
endif
endif

test:
	go test -v ./...

image:
	docker build -t oxide-controller:latest .

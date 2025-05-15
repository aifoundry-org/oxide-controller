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

build: $(CONTROLLER)

install: build
	@cp $(CONTROLLER) $(TARGET)

$(BINDIR):
	@mkdir -p $@

$(CONTROLLER): $(BINDIR)
	@go build -o $@

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

# © Copyright Deduptar Authors (see CONTRIBUTORS.md)

SHELL := bash
.SHELLFLAGS := -eu -c
MAKEFLAGS += --no-builtin-rules
MAKEFLAGS += --warn-undefined-variables

OUTDIR := build
BINNAME := deduptar
DEBUGBIN := $(OUTDIR)/debug/$(BINNAME)
RELEASEBIN := $(OUTDIR)/release/$(BINNAME)
SOURCES := main.go $(wildcard tarops/* cli/*)
GOBUILD := CGO_ENABLED=0 go build -buildvcs=true

all: dev

include Makefile-test

.PHONY: dev release clean

cli/CONTRIBUTORS.md: CONTRIBUTORS.md
	cat $< > $@

cli/LICENSE.txt: LICENSE.txt
	cat $< > $@

dev: $(OUTDIR)/debug/$(BINNAME)

release: $(OUTDIR)/release/$(BINNAME)

$(DEBUGBIN): $(SOURCES)
	$(GOBUILD) -o $(@)

$(RELEASEBIN): $(SOURCES)
	$(GOBUILD) -o $(@) -tags release -buildmode=exe -pgo=auto -ldflags="-s -w" -trimpath

clean: test-clean
	rm -rf $(OUTDIR)/

test: test-runtests .WAIT test-clean
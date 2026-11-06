# make install: build aat into BINDIR, and copy the Claude Code skills and
# agent into CLAUDE_DIR. Override either, e.g. make install BINDIR=~/bin
#
# BINDIR defaults to the folder of the aat already on your PATH, so an
# install updates the copy you run; failing that, where go install puts it.

BINDIR     ?= $(patsubst %/,%,$(dir $(shell command -v aat 2>/dev/null)))
ifeq ($(BINDIR),)
BINDIR     := $(shell go env GOBIN)
endif
ifeq ($(BINDIR),)
BINDIR     := $(shell go env GOPATH)/bin
endif
CLAUDE_DIR ?= $(HOME)/.claude
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: install install-bin install-claude test

install: install-claude

# Build to a temporary file and move it into place: on macOS, a binary
# overwritten in place with cp is killed when it next runs.
install-bin:
	@echo "installing aat in $(BINDIR)"
	mkdir -p "$(BINDIR)"
	go build -ldflags "-X github.com/ohnotnow/agent-acceptance-tracker/internal/aat.Version=$(VERSION)" -o "$(BINDIR)/.aat.new" ./cmd/aat
	mv "$(BINDIR)/.aat.new" "$(BINDIR)/aat"
	@echo "installed $(BINDIR)/aat ($(VERSION))"

# The skills and agent are built into the binary; it installs them, asking
# before it replaces a copy that differs ([y/N/d], d shows the diff).
install-claude: install-bin
	"$(BINDIR)/aat" install-skills --dir "$(CLAUDE_DIR)"

test:
	go test ./...

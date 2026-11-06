# Build everything into dist/ for the current machine, or GOARCH=arm64 make.
GOARCH ?= amd64
VERSION ?= dev

.PHONY: all web panel agent cli release test clean

all: agent panel

web:
	cd web && npm ci && VITE_IKE_API=http npm run build

# The panel embeds the web UI, so build the UI first.
panel: web
	rm -rf agent/internal/panel/ui/dist && cp -r web/dist agent/internal/panel/ui/dist
	cd agent && CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -trimpath -ldflags "-s -w" -o ../dist/ike-panel-linux-$(GOARCH) ./cmd/ike-panel

agent:
	cd agent && for b in ike-agent ike-mcp ike; do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -trimpath -ldflags "-s -w" -o ../dist/$$b-linux-$(GOARCH) ./cmd/$$b || exit 1; done

# The ike CLI and ike-mcp also run on people's own computers.
CLIENT_TARGETS = linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
LDFLAGS = -s -w

cli:
	cd agent && for t in $(CLIENT_TARGETS); do os=$${t%/*}; arch=$${t#*/}; ext=""; [ $$os = windows ] && ext=.exe; \
		for b in ike ike-mcp; do \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o ../dist/$$b-$$os-$$arch$$ext ./cmd/$$b || exit 1; \
		done; done

# Everything a GitHub release needs, with .sha256 files the installer checks.
release: clean
	$(MAKE) panel GOARCH=amd64 VERSION=$(VERSION)
	$(MAKE) panel GOARCH=arm64 VERSION=$(VERSION)
	$(MAKE) agent GOARCH=amd64 VERSION=$(VERSION)
	$(MAKE) agent GOARCH=arm64 VERSION=$(VERSION)
	$(MAKE) cli VERSION=$(VERSION)
	cp installer/install.sh dist/installer.sh
	cd dist && for f in *; do case $$f in *.sha256) ;; *) sha256sum $$f > $$f.sha256 ;; esac; done

test:
	cd agent && go vet ./... && go test ./...
	cd web && npm run typecheck
	shellcheck installer/install.sh

clean:
	rm -rf dist

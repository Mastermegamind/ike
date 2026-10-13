# Build everything into dist/ for the current machine, or GOARCH=arm64 make.
GOARCH ?= amd64
VERSION ?= dev

.PHONY: all web panel agent test clean

all: agent panel

web:
	cd web && npm ci && npm run build

# The panel embeds the web UI, so build the UI first.
panel: web
	rm -rf agent/internal/panel/ui/dist && cp -r web/dist agent/internal/panel/ui/dist
	cd agent && CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -trimpath -ldflags "-s -w" -o ../dist/ike-panel-linux-$(GOARCH) ./cmd/ike-panel

agent:
	cd agent && for b in ike-agent ike-mcp; do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -trimpath -ldflags "-s -w" -o ../dist/$$b-linux-$(GOARCH) ./cmd/$$b || exit 1; done

test:
	cd agent && go vet ./... && go test ./...
	cd web && npm run typecheck
	shellcheck installer/install.sh

clean:
	rm -rf dist

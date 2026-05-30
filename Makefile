VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build vet fmt test cross clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/reasonix ./cmd/reasonix
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/reasonix-plugin-example ./cmd/reasonix-plugin-example

vet:
	go vet ./...

fmt:
	gofmt -w .

test:
	go test ./...

cross:
	@mkdir -p dist
	@for p in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "build $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/reasonix-$$os-$$arch$$ext ./cmd/reasonix; \
	done

clean:
	rm -rf bin dist

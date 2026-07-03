GO_BUILD_ENV := CGO_ENABLED=0
GO_BUILD_FLAGS := -ldflags="-s -w"
MODULE_BINARY := bin/viam-data-mirror

.PHONY: build lint update test module.tar.gz packages module all setup clean upload

build: $(MODULE_BINARY)

# Single-arch host/dev build (e.g. `make bin/viam-data-mirror`).
$(MODULE_BINARY): Makefile go.mod *.go mirror/*.go
	GOOS=$(VIAM_BUILD_OS) GOARCH=$(VIAM_BUILD_ARCH) $(GO_BUILD_ENV) go build $(GO_BUILD_FLAGS) -o $(MODULE_BINARY) .

lint:
	gofmt -s -w .

update:
	go get go.viam.com/rdk@latest
	go mod tidy

test:
	go test ./...

module.tar.gz: build
	tar -czf module.tar.gz $(MODULE_BINARY) meta.json

# `make packages` cross-compiles every architecture locally and produces
# one tarball per arch at bin/<goos>-<goarch>/module.tar.gz, ready for `make upload`.
# Each tarball contains `bin/viam-data-mirror` and `meta.json` at the top level,
# matching the entrypoint path declared in meta.json
packages: bin/linux-amd64/module.tar.gz bin/linux-arm64/module.tar.gz bin/darwin-arm64/module.tar.gz

bin/%/module.tar.gz: Makefile go.mod *.go mirror/*.go meta.json
	@set -e; os=$$(echo $* | cut -d- -f1); arch=$$(echo $* | cut -d- -f2); \
	  workdir=bin/$*; bin=viam-data-mirror; \
	  mkdir -p $$workdir/bin; \
	  echo ">> building $$os/$$arch -> $$workdir/bin/$$bin"; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -o $$workdir/bin/$$bin .; \
	  tar -czf $$workdir/module.tar.gz -C $$workdir bin/$$bin -C $(CURDIR) meta.json

module: test module.tar.gz

all: test packages

setup:
	go mod tidy

clean:
	rm -rf bin module.tar.gz

VERSION ?= 0.3.0

# Build every arch, then print the upload commands. Override the version with
# e.g. `make upload VERSION=1.2.3`.
upload: packages
	@echo viam module upload --version \"$(VERSION)\" --platform \"linux/amd64\" bin/linux-amd64/module.tar.gz
	@echo viam module upload --version \"$(VERSION)\" --platform \"linux/arm64\" bin/linux-arm64/module.tar.gz
	@echo viam module upload --version \"$(VERSION)\" --platform \"darwin/arm64\" bin/darwin-arm64/module.tar.gz

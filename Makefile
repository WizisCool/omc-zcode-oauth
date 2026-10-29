PLUGIN_ID ?= zcode
VERSION ?= 0.0.0-dev
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
GO_LDFLAGS ?= -s -w -X main.pluginVersion=$(VERSION)
PLUGIN_EXT = $(if $(filter darwin,$(GOOS)),dylib,$(if $(filter windows,$(GOOS)),dll,so))

.PHONY: test vet build package clean

test:
	cd go && GOPROXY=off GOSUMDB=off go test ./...

vet:
	cd go && GOPROXY=off GOSUMDB=off go vet ./...

build:
	mkdir -p build
	cd go && CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -buildmode=c-shared -ldflags "$(GO_LDFLAGS)" -o ../build/$(PLUGIN_ID).$(PLUGIN_EXT) .
	rm -f build/$(PLUGIN_ID).h

package: build
	python3 .github/scripts/package-release.py --library build/$(PLUGIN_ID).$(PLUGIN_EXT) --archive build/$(PLUGIN_ID)_$(VERSION)_$(GOOS)_$(GOARCH).zip --checksum build/$(PLUGIN_ID)_$(VERSION)_$(GOOS)_$(GOARCH).zip.sha256

clean:
	rm -rf build

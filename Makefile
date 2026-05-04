# Micelio Go Backend
BINARY := micelio
PKG := github.com/micelio/micelio
CMD := ./cmd/micelio

# Version from git tag or default
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.sourceDir=$(CURDIR)"

.PHONY: build build-dashboard build-with-dashboard test lint clean bench bench-short bench-cpu bench-mem release clean-release install sync app clean-app

build:
	go build $(LDFLAGS) -o $(BINARY) $(CMD)

# Build dashboard assets and copy into embed directory
build-dashboard:
	cd dashboard && npm run build
	rm -rf cmd/micelio/dashboard/dist/*
	cp -r dist/dashboard/* cmd/micelio/dashboard/dist/

# Build Go binary with embedded dashboard
build-with-dashboard: build-dashboard build

test:
	go test ./...

lint:
	golangci-lint run ./...

bench:
	go test ./... -bench=. -benchmem -count=3

bench-short:
	go test ./... -bench=. -benchmem -benchtime=100ms -count=1

bench-cpu:
	go test ./internal/analysis/ -bench=BenchmarkPageRank -cpuprofile=cpu.prof -benchmem -count=1
	@echo "Run: go tool pprof -http=:8080 cpu.prof"

bench-mem:
	go test ./internal/extract/ -bench=BenchmarkExtractPageData_Large -memprofile=mem.prof -benchmem -count=1
	@echo "Run: go tool pprof -http=:8080 mem.prof"

clean:
	rm -f $(BINARY) cpu.prof mem.prof

# Cross-compile for all platforms
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

release: clean-release
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$$(echo $$platform | cut -d/ -f1); \
		arch=$$(echo $$platform | cut -d/ -f2); \
		output="dist/micelio-$$os-$$arch"; \
		if [ "$$os" = "windows" ]; then output="$$output.exe"; fi; \
		echo "Building $$os/$$arch..."; \
		GOOS=$$os GOARCH=$$arch go build $(LDFLAGS) -o $$output ./cmd/micelio; \
	done
	@echo "Generating checksums..."
	@cd dist && shasum -a 256 micelio-* > checksums.txt
	@echo "Release binaries + checksums.txt in dist/"

clean-release:
	rm -rf dist/

install: build
	install -m 755 micelio $(shell go env GOPATH)/bin/
	@if [ -f "/Applications/$(APP_NAME)/Contents/MacOS/micelio" ]; then \
		cp micelio "/Applications/$(APP_NAME)/Contents/MacOS/micelio"; \
		echo "Updated /Applications/$(APP_NAME)"; \
	fi

# Build and sync all binaries (CWD + GOPATH + App bundle)
sync: build
	@install -m 755 micelio $(shell go env GOPATH)/bin/
	@if [ -f "/Applications/$(APP_NAME)/Contents/MacOS/micelio" ]; then \
		cp micelio "/Applications/$(APP_NAME)/Contents/MacOS/micelio"; \
	fi
	@echo "Synced: ./micelio, $(shell go env GOPATH)/bin/micelio, /Applications/$(APP_NAME)"

# macOS .app bundle
APP_NAME := Micelio.app
APP_DIR := dist/$(APP_NAME)
MACOS_DIR := packaging/macos
# Strip leading "v" so plutil gets a version like "1.0.0", not "v1.0.0".
APP_VERSION := $(patsubst v%,%,$(VERSION))

app: build-with-dashboard
	@echo "Building $(APP_NAME)..."
	@rm -rf "$(APP_DIR)"
	@mkdir -p "$(APP_DIR)/Contents/MacOS" "$(APP_DIR)/Contents/Resources"
	@cp "$(MACOS_DIR)/Info.plist" "$(APP_DIR)/Contents/"
	@plutil -replace CFBundleVersion -string "$(APP_VERSION)" "$(APP_DIR)/Contents/Info.plist"
	@plutil -replace CFBundleShortVersionString -string "$(APP_VERSION)" "$(APP_DIR)/Contents/Info.plist"
	@cp "$(MACOS_DIR)/launcher" "$(APP_DIR)/Contents/MacOS/"
	@cp "$(BINARY)" "$(APP_DIR)/Contents/MacOS/"
	@if [ -f "$(MACOS_DIR)/AppIcon.icns" ]; then \
		cp "$(MACOS_DIR)/AppIcon.icns" "$(APP_DIR)/Contents/Resources/"; \
	else \
		echo "No icon found — run: bash $(MACOS_DIR)/create-icns.sh"; \
	fi
	@echo "Created $(APP_DIR) (version $(APP_VERSION))"
	@echo "Install: cp -r $(APP_DIR) /Applications/"

clean-app:
	rm -rf "$(APP_DIR)"

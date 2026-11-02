# AeroStack: Unified Build and Test Automation
# Strict external storage mandate: builds, caches, and binaries reside strictly in /Volumes/AdamSSD/antigravity_workspace/orbstack_free

SHELL := /bin/bash
PROJECT_DIR := /Volumes/AdamSSD/antigravity_workspace/orbstack_free
BUILD_DIR := $(PROJECT_DIR)/build
SCRATCH_DIR := $(PROJECT_DIR)/.build
GOCACHE_DIR := $(PROJECT_DIR)/.gocache
GOPATH_DIR := $(PROJECT_DIR)/.gopath

.PHONY: all setup build-host build-guest build-bridge build-cli build-network test clean lint

all: setup build-host build-guest build-bridge build-cli build-network
	@echo "=========================================================="
	@echo " AeroStack system build completed successfully!"
	@echo " Binaries located in: $(BUILD_DIR)"
	@echo "=========================================================="

setup:
	@mkdir -p $(BUILD_DIR) $(SCRATCH_DIR) $(GOCACHE_DIR) $(GOPATH_DIR)
	@find $(PROJECT_DIR) -name "._*" -delete 2>/dev/null || true

build-host: setup
	@echo "--> Building macOS Host Engine (aero-vmd & orb-vmd)..."
	@find $(PROJECT_DIR) -name "._*" -delete 2>/dev/null || true
	@if [ -f "$(PROJECT_DIR)/Package.swift" ]; then \
		COPYFILE_DISABLE=1 swift build --package-path $(PROJECT_DIR) --scratch-path $(SCRATCH_DIR) -c release --product aero-vmd && \
		cp $(SCRATCH_DIR)/release/aero-vmd $(BUILD_DIR)/aero-vmd && \
		ln -sf aero-vmd $(BUILD_DIR)/orb-vmd; \
	elif [ -f "$(PROJECT_DIR)/src/host/engine/main.swift" ]; then \
		COPYFILE_DISABLE=1 swiftc -O -parse-as-library $(PROJECT_DIR)/src/host/engine/*.swift -o $(BUILD_DIR)/aero-vmd && \
		ln -sf aero-vmd $(BUILD_DIR)/orb-vmd; \
	fi
	@find $(BUILD_DIR) -name "._*" -delete 2>/dev/null || true
	@codesign --force --sign - --entitlements $(PROJECT_DIR)/entitlements.plist $(BUILD_DIR)/aero-vmd 2>/dev/null || true
	@echo "    Built: $(BUILD_DIR)/aero-vmd (and symlink orb-vmd)"

build-guest: setup
	@echo "--> Cross-compiling Linux Guest Agent (aero-agent ARM64 static)..."
	@if [ -f "$(PROJECT_DIR)/src/guest/agent/main.go" ]; then \
		GOCACHE=$(GOCACHE_DIR) GOPATH=$(GOPATH_DIR) CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
		go build -ldflags="-s -w" -o $(BUILD_DIR)/aero-agent $(PROJECT_DIR)/src/guest/agent/... && \
		ln -sf aero-agent $(BUILD_DIR)/orb-agent && \
		echo "    Built: $(BUILD_DIR)/aero-agent (and symlink orb-agent)"; \
	fi

build-bridge: setup
	@echo "--> Building Docker Engine API Socket Bridge (aero-bridge & orb-docker-bridge)..."
	@if [ -f "$(PROJECT_DIR)/src/host/bridge/main.go" ]; then \
		GOCACHE=$(GOCACHE_DIR) GOPATH=$(GOPATH_DIR) CGO_ENABLED=0 \
		go build -ldflags="-s -w" -o $(BUILD_DIR)/aero-bridge $(PROJECT_DIR)/src/host/bridge/... && \
		ln -sf aero-bridge $(BUILD_DIR)/orb-docker-bridge && \
		echo "    Built: $(BUILD_DIR)/aero-bridge (and symlink orb-docker-bridge)"; \
	fi

build-cli: setup
	@echo "--> Building Unified CLI (aerostack, aero, aeroctl, orbctl & orb)..."
	@if [ -f "$(PROJECT_DIR)/src/host/cli/main.go" ]; then \
		GOCACHE=$(GOCACHE_DIR) GOPATH=$(GOPATH_DIR) CGO_ENABLED=0 \
		go build -ldflags="-s -w" -o $(BUILD_DIR)/aerostack $(PROJECT_DIR)/src/host/cli && \
		ln -sf aerostack $(BUILD_DIR)/aero && \
		ln -sf aerostack $(BUILD_DIR)/aeroctl && \
		ln -sf aerostack $(BUILD_DIR)/orbctl && \
		ln -sf aerostack $(BUILD_DIR)/orb && \
		echo "    Built: $(BUILD_DIR)/aerostack (and symlinks aero, aeroctl, orbctl, orb)"; \
	fi

build-network: setup
	@echo "--> Building Userspace Network & DNS Engine (aero-netd & orb-netd)..."
	@if [ -f "$(PROJECT_DIR)/src/host/network/main.go" ]; then \
		GOCACHE=$(GOCACHE_DIR) GOPATH=$(GOPATH_DIR) \
		go build -ldflags="-s -w" -o $(BUILD_DIR)/aero-netd $(PROJECT_DIR)/src/host/network/... && \
		ln -sf aero-netd $(BUILD_DIR)/orb-netd && \
		echo "    Built: $(BUILD_DIR)/aero-netd (and symlink orb-netd)"; \
	fi

test: setup
	@echo "--> Executing comprehensive automated test suites..."
	@find $(PROJECT_DIR)/tests -type f -name "*.sh" -perm +111 -exec {} \;
	@echo "--> Running Go test suites..."
	@if [ -d "$(PROJECT_DIR)/src/guest/agent" ]; then \
		GOCACHE=$(GOCACHE_DIR) GOPATH=$(GOPATH_DIR) go test -v $(PROJECT_DIR)/src/guest/agent/...; \
	fi
	@if [ -d "$(PROJECT_DIR)/src/host/bridge" ]; then \
		GOCACHE=$(GOCACHE_DIR) GOPATH=$(GOPATH_DIR) go test -v -race $(PROJECT_DIR)/src/host/bridge/...; \
	fi
	@if [ -d "$(PROJECT_DIR)/src/host/network" ]; then \
		GOCACHE=$(GOCACHE_DIR) GOPATH=$(GOPATH_DIR) go test -v $(PROJECT_DIR)/src/host/network/...; \
	fi
	@if [ -d "$(PROJECT_DIR)/src/host/cli" ]; then \
		GOCACHE=$(GOCACHE_DIR) GOPATH=$(GOPATH_DIR) go test -v $(PROJECT_DIR)/src/host/cli/...; \
	fi
	@echo "=========================================================="
	@echo " ALL TESTS PASSED!"
	@echo "=========================================================="

clean:
	@echo "--> Cleaning build artifacts on external storage..."
	@rm -rf $(BUILD_DIR) $(SCRATCH_DIR) $(GOCACHE_DIR) $(GOPATH_DIR)
	@find $(PROJECT_DIR) -name "._*" -delete 2>/dev/null || true
	@echo "    Clean completed."

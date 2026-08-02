# DQL Makefile
# Development workflow for the DQL project

# ==============================================================================
# Variables
# ==============================================================================

# Project configuration
COVERAGE_DIR := coverage
LINT_CONFIG := .golangci.yml

# Version and metadata
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GO_VERSION := $(shell go version | cut -d' ' -f3)

# Go commands and flags
GOCMD := go
GOTEST := $(GOCMD) test
GOGET := $(GOCMD) get
GOMOD := $(GOCMD) mod
GOFMT := $(GOCMD) fmt
GOVET := $(GOCMD) vet

# Test flags
TEST_FLAGS := -v -race -timeout=5m
COVERAGE_FLAGS := -coverprofile=$(COVERAGE_DIR)/coverage.out -covermode=atomic
BENCH_FLAGS := -bench=. -benchmem -benchtime=5s

# Directories to test/lint
# Every module in this repository. The LSP binary is a separate module so the
# library stays dependency-free — which means `go list ./...` from the root
# never sees it, and a plain `go test ./...` would report success without ever
# having built it.
MODULES := $(shell find . -name go.mod -not -path "./.git/*" -exec dirname {} \; | sort)

TEST_DIRS := $(shell go list ./... 2>/dev/null)
LINT_DIRS := ./...

# Tools
GOLANGCI_LINT := golangci-lint

# Colors for output
COLOR_RESET := \033[0m
COLOR_GREEN := \033[32m
COLOR_YELLOW := \033[33m
COLOR_BLUE := \033[34m
COLOR_RED := \033[31m

# ==============================================================================
# Main targets
# ==============================================================================

.PHONY: all
## all: Run format, lint, and test (default target)
all: fmt lint test

.PHONY: help
## help: Show this help message
help:
	@echo "$(COLOR_BLUE)DQL Makefile$(COLOR_RESET)"
	@echo ""
	@echo "$(COLOR_GREEN)Usage:$(COLOR_RESET)"
	@echo "  make [target]"
	@echo ""
	@echo "$(COLOR_GREEN)Targets:$(COLOR_RESET)"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/  /'

# ==============================================================================
# Test targets
# ==============================================================================

.PHONY: test
## test: Run all tests with race detector
test:
	@echo "$(COLOR_GREEN)Running tests...$(COLOR_RESET)"
	@for m in $(MODULES); do (cd $$m && $(GOTEST) $(TEST_FLAGS) ./...) || exit 1; done
	@echo "$(COLOR_GREEN)✓ All tests passed$(COLOR_RESET)"

.PHONY: test-short
## test-short: Run tests with -short flag
test-short:
	@echo "$(COLOR_GREEN)Running short tests...$(COLOR_RESET)"
	@for m in $(MODULES); do (cd $$m && $(GOTEST) -short $(TEST_FLAGS) ./...) || exit 1; done

.PHONY: test-verbose
## test-verbose: Run tests with verbose output
test-verbose:
	@echo "$(COLOR_GREEN)Running tests (verbose)...$(COLOR_RESET)"
	@$(GOTEST) $(TEST_FLAGS) -v $(TEST_DIRS)

.PHONY: test-coverage
## test-coverage: Run tests with coverage report
test-coverage:
	@echo "$(COLOR_GREEN)Running tests with coverage...$(COLOR_RESET)"
	@mkdir -p $(COVERAGE_DIR)
	@$(GOTEST) $(TEST_FLAGS) $(COVERAGE_FLAGS) $(TEST_DIRS)
	@$(GOCMD) tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html
	@echo "$(COLOR_GREEN)✓ Coverage report: $(COVERAGE_DIR)/coverage.html$(COLOR_RESET)"
	@$(GOCMD) tool cover -func=$(COVERAGE_DIR)/coverage.out | grep total | awk '{print "Total coverage: " $$3}'

.PHONY: test-coverage-text
## test-coverage-text: Run tests and show coverage summary
test-coverage-text:
	@echo "$(COLOR_GREEN)Running tests with coverage...$(COLOR_RESET)"
	@mkdir -p $(COVERAGE_DIR)
	@$(GOTEST) $(TEST_FLAGS) $(COVERAGE_FLAGS) $(TEST_DIRS)
	@$(GOCMD) tool cover -func=$(COVERAGE_DIR)/coverage.out

.PHONY: test-race
## test-race: Run tests with race detector
test-race:
	@echo "$(COLOR_GREEN)Running tests with race detector...$(COLOR_RESET)"
	@$(GOTEST) -race -timeout=10m $(TEST_DIRS)

.PHONY: bench
## bench: Run benchmarks
bench:
	@echo "$(COLOR_GREEN)Running benchmarks...$(COLOR_RESET)"
	@$(GOTEST) $(BENCH_FLAGS) $(TEST_DIRS)

.PHONY: bench-compare
## bench-compare: Run benchmarks and save to file for comparison
bench-compare:
	@echo "$(COLOR_GREEN)Running benchmarks (saving to bench.txt)...$(COLOR_RESET)"
	@$(GOTEST) $(BENCH_FLAGS) $(TEST_DIRS) | tee bench.txt

# ==============================================================================
# Linting and formatting
# ==============================================================================

.PHONY: lint
## lint: Run golangci-lint
lint:
	@echo "$(COLOR_GREEN)Running linter...$(COLOR_RESET)"
	@if command -v $(GOLANGCI_LINT) >/dev/null 2>&1; then \
		$(GOLANGCI_LINT) run $(LINT_DIRS) --timeout=5m; \
		echo "$(COLOR_GREEN)✓ Linting passed$(COLOR_RESET)"; \
	else \
		echo "$(COLOR_RED)Error: golangci-lint not found. Run 'make install-tools' to install$(COLOR_RESET)"; \
		exit 1; \
	fi

.PHONY: lint-fix
## lint-fix: Run golangci-lint with auto-fix
lint-fix:
	@echo "$(COLOR_GREEN)Running linter with auto-fix...$(COLOR_RESET)"
	@$(GOLANGCI_LINT) run $(LINT_DIRS) --fix --timeout=5m

.PHONY: fmt
## fmt: Format Go code
fmt:
	@echo "$(COLOR_GREEN)Formatting code...$(COLOR_RESET)"
	@$(GOFMT) $(TEST_DIRS)
	@echo "$(COLOR_GREEN)✓ Code formatted$(COLOR_RESET)"

.PHONY: fmt-check
## fmt-check: Check if code is formatted
fmt-check:
	@echo "$(COLOR_GREEN)Checking code format...$(COLOR_RESET)"
	@UNFORMATTED=$$(gofmt -l .); \
	if [ -n "$$UNFORMATTED" ]; then \
		echo "$(COLOR_RED)Code is not formatted. Run 'make fmt'$(COLOR_RESET)"; \
		echo "$$UNFORMATTED"; \
		exit 1; \
	fi

.PHONY: vet
## vet: Run go vet
vet:
	@echo "$(COLOR_GREEN)Running go vet...$(COLOR_RESET)"
	@$(GOVET) $(TEST_DIRS)
	@echo "$(COLOR_GREEN)✓ Vet passed$(COLOR_RESET)"

.PHONY: tidy
## tidy: Tidy go modules
tidy:
	@echo "$(COLOR_GREEN)Tidying modules...$(COLOR_RESET)"
	@$(GOMOD) tidy
	@echo "$(COLOR_GREEN)✓ Modules tidied$(COLOR_RESET)"

.PHONY: tidy-check
## tidy-check: Check if go.mod is tidy
tidy-check:
	@echo "$(COLOR_GREEN)Checking if modules are tidy...$(COLOR_RESET)"
	@$(GOMOD) tidy
	@git diff --exit-code go.mod go.sum || \
		(echo "$(COLOR_RED)go.mod or go.sum is not tidy. Run 'make tidy'$(COLOR_RESET)" && exit 1)

.PHONY: verify
## verify: Run all verification checks (fmt-check, vet, tidy-check, lint)
verify: fmt-check vet tidy-check lint
	@echo "$(COLOR_GREEN)✓ All verification checks passed$(COLOR_RESET)"

# ==============================================================================
# Dependency management
# ==============================================================================

.PHONY: deps
## deps: Download dependencies
deps:
	@echo "$(COLOR_GREEN)Downloading dependencies...$(COLOR_RESET)"
	@$(GOMOD) download
	@echo "$(COLOR_GREEN)✓ Dependencies downloaded$(COLOR_RESET)"

.PHONY: deps-update
## deps-update: Update all dependencies
deps-update:
	@echo "$(COLOR_GREEN)Updating dependencies...$(COLOR_RESET)"
	@$(GOGET) -u ./...
	@$(GOMOD) tidy
	@echo "$(COLOR_GREEN)✓ Dependencies updated$(COLOR_RESET)"

# ==============================================================================
# Security and quality
# ==============================================================================

.PHONY: security
## security: Run security scan with gosec
security:
	@echo "$(COLOR_GREEN)Running security scan...$(COLOR_RESET)"
	@if command -v gosec >/dev/null 2>&1; then \
		gosec -exclude=G115 -exclude-dir=vendor ./...; \
		echo "$(COLOR_GREEN)✓ Security scan completed$(COLOR_RESET)"; \
	else \
		echo "$(COLOR_YELLOW)Warning: gosec not found. Run 'make install-tools' to install$(COLOR_RESET)"; \
	fi

.PHONY: vuln-check
## vuln-check: Check for known vulnerabilities
vuln-check:
	@echo "$(COLOR_GREEN)Checking for vulnerabilities...$(COLOR_RESET)"
	@if command -v govulncheck >/dev/null 2>&1; then \
		govulncheck ./...; \
		echo "$(COLOR_GREEN)✓ Vulnerability check completed$(COLOR_RESET)"; \
	else \
		echo "$(COLOR_YELLOW)Warning: govulncheck not found. Run 'make install-tools' to install$(COLOR_RESET)"; \
	fi

# ==============================================================================
# Cleanup
# ==============================================================================

.PHONY: clean
## clean: Remove build artifacts and cache
clean:
	@echo "$(COLOR_GREEN)Cleaning...$(COLOR_RESET)"
	@rm -rf $(COVERAGE_DIR)
	@rm -f bench.txt
	@$(GOCMD) clean -cache -testcache
	@echo "$(COLOR_GREEN)✓ Cleaned$(COLOR_RESET)"

# ==============================================================================
# Documentation
# ==============================================================================

.PHONY: docs
## docs: Generate documentation (starts godoc server)
docs:
	@echo "$(COLOR_GREEN)Generating documentation...$(COLOR_RESET)"
	@if command -v godoc >/dev/null 2>&1; then \
		echo "Starting godoc server at http://localhost:6060"; \
		godoc -http=:6060; \
	else \
		echo "$(COLOR_YELLOW)Warning: godoc not found. Run 'go install golang.org/x/tools/cmd/godoc@latest'$(COLOR_RESET)"; \
	fi

# ==============================================================================
# Tools installation
# ==============================================================================

.PHONY: install-tools
## install-tools: Install development tools
install-tools:
	@echo "$(COLOR_GREEN)Installing development tools...$(COLOR_RESET)"
	@echo "  Installing golangci-lint..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@echo "  Installing gosec..."
	@go install github.com/securego/gosec/v2/cmd/gosec@latest
	@echo "  Installing govulncheck..."
	@go install golang.org/x/vuln/cmd/govulncheck@latest
	@echo "$(COLOR_GREEN)✓ All tools installed$(COLOR_RESET)"

.PHONY: check-tools
## check-tools: Check if required tools are installed
check-tools:
	@echo "$(COLOR_GREEN)Checking installed tools...$(COLOR_RESET)"
	@command -v golangci-lint >/dev/null 2>&1 && echo "  ✓ golangci-lint" || echo "  ✗ golangci-lint"
	@command -v gosec >/dev/null 2>&1 && echo "  ✓ gosec" || echo "  ✗ gosec"
	@command -v govulncheck >/dev/null 2>&1 && echo "  ✓ govulncheck" || echo "  ✗ govulncheck"

# ==============================================================================
# CI/CD helpers
# ==============================================================================

.PHONY: ci
## ci: Run all CI checks (verify, test)
ci: verify test
	@echo "$(COLOR_GREEN)✓ All CI checks passed$(COLOR_RESET)"

.PHONY: ci-comprehensive
## ci-comprehensive: Run comprehensive CI checks (verify, test-coverage, security)
ci-comprehensive: verify test-coverage security vuln-check
	@echo "$(COLOR_GREEN)✓ All comprehensive CI checks passed$(COLOR_RESET)"

.PHONY: pre-commit
## pre-commit: Run checks before commit (fmt, lint, test-short)
pre-commit: fmt lint test-short
	@echo "$(COLOR_GREEN)✓ Pre-commit checks passed$(COLOR_RESET)"

.PHONY: pre-push
## pre-push: Run checks before push (verify, test)
pre-push: verify test
	@echo "$(COLOR_GREEN)✓ Pre-push checks passed$(COLOR_RESET)"

# ==============================================================================
# Release and GitHub Workflows
# ==============================================================================

.PHONY: release-check
## release-check: Check if ready for release
release-check: verify test
	@echo "$(COLOR_GREEN)Running pre-release checks...$(COLOR_RESET)"
	@if ! git diff-index --quiet HEAD --; then \
		echo "$(COLOR_RED)Working directory is not clean. Commit changes first.$(COLOR_RESET)"; \
		git status --short; \
		exit 1; \
	fi
	@if [ -z "$$(git describe --tags --exact-match 2>/dev/null)" ]; then \
		echo "$(COLOR_GREEN)✓ No tag on current commit$(COLOR_RESET)"; \
	else \
		echo "$(COLOR_YELLOW)⚠ Current commit already has a tag: $$(git describe --tags --exact-match 2>/dev/null)$(COLOR_RESET)"; \
	fi
	@echo "$(COLOR_GREEN)✓ Ready for release$(COLOR_RESET)"

.PHONY: tag
## tag: Create and push a new version tag (usage: make tag VERSION=v1.2.3)
tag:
	@if [ -z "$(VERSION)" ]; then \
		echo "$(COLOR_RED)VERSION required. Usage: make tag VERSION=v1.2.3$(COLOR_RESET)"; \
		exit 1; \
	fi
	@echo "$(COLOR_GREEN)Creating tag $(VERSION)...$(COLOR_RESET)"
	@git tag -a $(VERSION) -m "Release $(VERSION)"
	@git push origin $(VERSION)
	@echo "$(COLOR_GREEN)✓ Tag $(VERSION) created and pushed$(COLOR_RESET)"
	@echo "$(COLOR_BLUE)GitHub will automatically create a release from this tag$(COLOR_RESET)"

.PHONY: tag-delete
## tag-delete: Delete a local and remote tag (usage: make tag-delete VERSION=v1.2.3)
tag-delete:
	@if [ -z "$(VERSION)" ]; then \
		echo "$(COLOR_RED)VERSION required. Usage: make tag-delete VERSION=v1.2.3$(COLOR_RESET)"; \
		exit 1; \
	fi
	@echo "$(COLOR_YELLOW)Deleting tag $(VERSION)...$(COLOR_RESET)"
	@git tag -d $(VERSION) || true
	@git push origin :refs/tags/$(VERSION) || true
	@echo "$(COLOR_GREEN)✓ Tag $(VERSION) deleted$(COLOR_RESET)"

.PHONY: release
## release: Full release process (checks, tag, and info)
release: release-check
	@echo ""
	@echo "$(COLOR_BLUE)Ready to create a new release!$(COLOR_RESET)"
	@echo ""
	@echo "$(COLOR_GREEN)Current version:$(COLOR_RESET) $(VERSION)"
	@echo "$(COLOR_GREEN)Latest tag:$(COLOR_RESET) $$(git describe --tags --abbrev=0 2>/dev/null || echo 'none')"
	@echo ""
	@echo "$(COLOR_YELLOW)To create a release:$(COLOR_RESET)"
	@echo "  1. Choose version (e.g., v1.2.3)"
	@echo "  2. Run: make auto-release VERSION=v1.2.3"
	@echo "  3. Or manually: make tag VERSION=v1.2.3"
	@echo ""
	@echo "$(COLOR_BLUE)GitHub Release Workflow:$(COLOR_RESET)"
	@echo "  - Triggers on new version tags (v*)"
	@echo "  - Runs tests and linters"
	@echo "  - Creates GitHub Release with changelog"
	@echo "  - Publishes package documentation"

.PHONY: auto-release
## auto-release: Trigger automated release workflow (usage: make auto-release VERSION=v1.2.3)
auto-release:
	@if [ -z "$(VERSION)" ]; then \
		echo "$(COLOR_RED)VERSION required. Usage: make auto-release VERSION=v1.2.3$(COLOR_RESET)"; \
		exit 1; \
	fi
	@echo "$(COLOR_GREEN)Triggering automated release for $(VERSION)...$(COLOR_RESET)"
	@if command -v gh >/dev/null 2>&1; then \
		gh workflow run auto-release.yml -f version=$(VERSION); \
		echo "$(COLOR_GREEN)✓ Release workflow triggered$(COLOR_RESET)"; \
		echo "$(COLOR_BLUE)View progress: https://github.com/$$(git config --get remote.origin.url | sed 's/.*github.com[:/]\(.*\)\.git/\1/')/actions$(COLOR_RESET)"; \
	else \
		echo "$(COLOR_RED)Error: GitHub CLI (gh) not found$(COLOR_RESET)"; \
		echo "$(COLOR_YELLOW)Install with: brew install gh$(COLOR_RESET)"; \
		echo "$(COLOR_YELLOW)Or use GitHub UI: Actions → Auto Release → Run workflow$(COLOR_RESET)"; \
		exit 1; \
	fi

.PHONY: releases
## releases: List recent releases
releases:
	@echo "$(COLOR_BLUE)Recent Releases:$(COLOR_RESET)"
	@echo ""
	@git tag -l "v*" --sort=-version:refname | head -10 | while read tag; do \
		date=$$(git log -1 --format=%ai $$tag | cut -d' ' -f1); \
		echo "  $$tag ($$date)"; \
	done

.PHONY: changelog
## changelog: Generate changelog since last tag
changelog:
	@echo "$(COLOR_BLUE)Changelog since last release:$(COLOR_RESET)"
	@echo ""
	@LAST_TAG=$$(git describe --tags --abbrev=0 2>/dev/null); \
	if [ -z "$$LAST_TAG" ]; then \
		echo "No previous tags found"; \
		git log --oneline --decorate; \
	else \
		echo "Changes since $$LAST_TAG:"; \
		echo ""; \
		git log $$LAST_TAG..HEAD --oneline --decorate; \
	fi

.PHONY: github-workflows
## github-workflows: Show information about GitHub workflows
github-workflows:
	@echo "$(COLOR_BLUE)GitHub Workflows:$(COLOR_RESET)"
	@echo ""
	@echo "$(COLOR_GREEN)Expected workflows for go-utils:$(COLOR_RESET)"
	@echo "  1. ci.yml - Run tests and linters on PRs and pushes"
	@echo "  2. release.yml - Automatically create releases on version tags"
	@echo "  3. codeql.yml - Security analysis (CodeQL)"
	@echo ""
	@if [ -d ".github/workflows" ]; then \
		echo "$(COLOR_GREEN)Existing workflows:$(COLOR_RESET)"; \
		for workflow in .github/workflows/*.yml .github/workflows/*.yaml; do \
			if [ -f "$$workflow" ]; then \
				NAME=$$(grep "^name:" $$workflow | head -1 | cut -d':' -f2 | xargs); \
				FILE=$$(basename $$workflow); \
				echo "  - $$NAME ($$FILE)"; \
			fi; \
		done; \
	else \
		echo "$(COLOR_YELLOW).github/workflows directory not found$(COLOR_RESET)"; \
		echo "Create workflows to automate releases and CI"; \
	fi

# ==============================================================================
# Quick shortcuts
# ==============================================================================

.PHONY: t
## t: Alias for 'test'
t: test

.PHONY: l
## l: Alias for 'lint'
l: lint

.PHONY: f
## f: Alias for 'fmt'
f: fmt

# ==============================================================================
# Info
# ==============================================================================

.PHONY: info
## info: Display project information
info:
	@echo "$(COLOR_BLUE)go-utils Project Information$(COLOR_RESET)"
	@echo ""
	@echo "$(COLOR_GREEN)Version:$(COLOR_RESET)    $(VERSION)"
	@echo "$(COLOR_GREEN)Commit:$(COLOR_RESET)     $(COMMIT)"
	@echo "$(COLOR_GREEN)Go Version:$(COLOR_RESET) $(GO_VERSION)"
	@echo ""
	@echo "$(COLOR_GREEN)Module:$(COLOR_RESET)     $$(head -1 go.mod | cut -d' ' -f2)"
	@echo "$(COLOR_GREEN)Packages:$(COLOR_RESET)   errs, log"
	@echo ""
	@echo "$(COLOR_GREEN)Latest Tag:$(COLOR_RESET) $$(git describe --tags --abbrev=0 2>/dev/null || echo 'none')"

# ==============================================================================
# Default target
# ==============================================================================

.DEFAULT_GOAL := help

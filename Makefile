# SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
#
# SPDX-License-Identifier: Apache-2.0

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.DEFAULT_GOAL := help

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Checks

.PHONY: check
check: reuse markdown-lint ## Run all repository checks.

.PHONY: reuse
reuse: ## Check REUSE compliance.
	reuse lint

.PHONY: markdown-lint
markdown-lint: ## Lint Markdown files.
	rumdl check .

##@ Formatting

.PHONY: markdown-fmt
markdown-fmt: ## Format Markdown files and fix lint issues where possible.
	rumdl fmt .

##@ Setup

.PHONY: hooks
hooks: ## Verify prerequisites and install the opt-in Git hooks.
	@missing=(); \
	for tool in lefthook reuse gitleaks committed rumdl; do \
		if ! command -v "$${tool}" >/dev/null 2>&1; then \
			missing+=("$${tool}"); \
		fi; \
	done; \
	if (( $${#missing[@]} > 0 )); then \
		printf 'Missing hook prerequisites: %s\nSee CONTRIBUTING.md for installation instructions.\n' "$${missing[*]}"; \
		exit 1; \
	fi
	lefthook install

# SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
#
# SPDX-License-Identifier: Apache-2.0

SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.DEFAULT_GOAL := help

CHART := chart

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Checks

.PHONY: check
check: lint test ## Run all checks of the management proxy.

.PHONY: lint
lint: ## Lint the chart and its rendered resources with each value profile, run the chart unit tests, and lint the scripts.
	@rendered=$$(mktemp -d); trap 'rm -rf "$$rendered"' EXIT; \
	for values in $(CHART)/ci/*-values.yaml; do \
		echo "helm lint -f $${values}"; \
		helm lint --quiet --strict $(CHART) -f "$${values}"; \
		helm template lint $(CHART) --namespace management -f "$${values}" >"$$rendered/$${values##*/}"; \
	done; \
	echo "kube-linter"; \
	kube-linter lint --config .kube-linter.yaml --fail-if-no-objects-found --format plain "$$rendered"; \
	echo "./test/validate-schemas.sh"; \
	./test/validate-schemas.sh "$$rendered"
	helm unittest $(CHART)
	shellcheck test/*.sh

.PHONY: test
test: ## Validate the Envoy configuration of each value profile, and test the sidecar with TLS and mutual TLS. Needs Docker.
	cd test && test -z "$$(gofmt -l .)" && go vet ./... && go test -count=1 ./...

---
status: proposed
decision-makers:
  - elmar.meuthen@telekom.de
  - michael.fedorenko@telekom.de
  - luis.pflamminger@telekom.de
  - mika.juznik@telekom.de
  - niklas.beilmann@telekom.de
tags: [testing, tooling]
---

# Test Components with helm-unittest and Go Testcontainers

## Context and Problem Statement

The first component, the [management proxy](../developer/management-proxy.md), needs tests for its Helm chart and for
the behaviour of its Envoy sidecar. The first version used bash scripts: one script checked the values that the chart
must refuse, and one script started the sidecar in Docker with openssl, jq and Python one-liners. These scripts were
long and hard to read and extend.

Which test frameworks do the components of this repository use?

## Decision Drivers

- Use frameworks that other projects use, so that contributors know them and the repository has no custom test harness.
- Keep the tests of a component fast and runnable on a laptop with Docker, without a Kubernetes cluster.
- Write test cases as data, for example a table of tokens and expected status codes.
- Use the same language for the tests as for the components that we plan to write.

## Considered Options

- Bash scripts for each check, as in the first version
- helm-unittest for the chart, and Go tests with testcontainers-go for the component behaviour
- End-to-end tests on a kind cluster for each component

## Decision Outcome

Chosen option: "helm-unittest for the chart, and Go tests with testcontainers-go for the component behaviour".

- [helm-unittest](https://github.com/helm-unittest/helm-unittest) checks the rendered resources and the values that the
  chart must refuse. The test cases use the value profiles in `chart/ci/`. Traefik and ingress-nginx test their charts
  in the same way.
- Go tests with [testcontainers-go](https://golang.testcontainers.org/) start the containers of a component in Docker
  and check its behaviour. Envoy Gateway and kgateway also test in Go.
- `helm lint`, kube-linter and kubeconform stay in `make lint`.

A kind cluster for each component tests more, for example RBAC and NetworkPolicies. But it is slow, it needs more
resources, and each change to the Envoy configuration then needs a cluster.

### Consequences

- Good, because the repository has no custom test harness, and the test output and reports are standard.
- Good, because the tests of a component run in about one minute with Docker only.
- Bad, because contributors need Go and the helm-unittest plugin.
- Bad, because the component tests do not cover the integration in a cluster, for example `kubectl proxy`, RBAC,
  NetworkPolicies and kgateway.

## More Information

An end-to-end and regression test suite for the whole repository can close the integration gap later. It would install
all components together on a kind cluster, with kgateway, and test the flows across them. It can reuse the Go helpers of
the component tests. We decide on it in a separate record when two components must work together.

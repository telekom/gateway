<!--
SPDX-FileCopyrightText: 2026 Deutsche Telekom AG

SPDX-License-Identifier: CC0-1.0
-->

# Management Proxy

The management proxy lets the distributed control plane configure the gateway resources of a data-plane cluster through
the Kubernetes API. An Envoy sidecar validates the Gateway Token that the gateway mints for the management route.
`kubectl proxy` forwards each accepted request with a local ServiceAccount. Both run from their upstream images.

- Design: [ADR 0004](../docs/decisions/0004-use-an-oauth-proxy-for-cross-cluster-configuration.md) and
  [ADR 0005](../docs/decisions/0005-reuse-an-existing-kubernetes-api-proxy.md)
- Architecture, implementation and configuration: [developer guide](../docs/developer/management-proxy.md)

## Layout

```text
management-proxy/
├── chart/                    Helm chart. values.yaml documents each value.
│   ├── ci/                   Value profiles for the lint and the tests
│   └── tests/                helm-unittest suites: rendered resources and refused values
└── test/
    ├── validate-schemas.sh   Validates the rendered resources against the Kubernetes and CRD schemas
    ├── echo.Dockerfile       Image of the echo server of the sidecar test, for Dependabot
    ├── envoy_test.go         Validates the Envoy configuration of each value profile with Envoy
    ├── kubectl_proxy_test.go Checks the paths that kubectl proxy accepts
    └── sidecar_test.go       Tests the sidecar in Docker with testcontainers-go
```

## Install

```bash
helm upgrade --install management-proxy ./chart --namespace gateway-management -f my-values.yaml
```

The chart needs Kubernetes 1.30 or newer. With the default values, it also needs cert-manager with an Issuer, and the
Gateway API and kgateway CRDs for the management route. See
[Configure the chart](../docs/developer/management-proxy.md#configure-the-chart) for the values that you must set.

## Checks

```bash
make check   # lint and test. Run make help to see the targets.
```

<!--
SPDX-FileCopyrightText: 2026 Deutsche Telekom AG

SPDX-License-Identifier: CC0-1.0
-->

# Gateway

An API gateway for the
[Open Telekom Integration Platform](https://github.com/telekom/Open-Telekom-Integration-Platform), based on
[kgateway](https://kgateway.dev/) and the Kubernetes [Gateway API](https://gateway-api.sigs.k8s.io/).

> [!WARNING]
> This project is in early development. Structure, interfaces and decisions can change at any time.

## Repository layout

This is a monorepo. It holds all components of the gateway and their documentation. Each component gets its own
top-level directory when we add it.

```text
.
└── docs/            Technical and operator documentation
    └── decisions/   Decision records
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Code of Conduct

This project has adopted the [Contributor Covenant](https://www.contributor-covenant.org/) in version 2.1 as our code of
conduct. Please see the details in our [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). All contributors must abide by the code
of conduct.

## Licensing

This project follows the [REUSE standard for software licensing](https://reuse.software/). Each file contains copyright
and license information, and license texts can be found in the [LICENSES](LICENSES) folder. For more information visit
<https://reuse.software/>.

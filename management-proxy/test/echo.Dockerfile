# SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
#
# SPDX-License-Identifier: Apache-2.0

# The echo server of sidecar_test.go, in place of kubectl proxy. The test reads the image of this line, so that
# Dependabot can update its tag and digest. Nothing builds this file.
FROM docker.io/traefik/whoami:v1.12.0@sha256:c4717a8d1f0134a7444e24f881160e033991f23027c6c5a9a3f8fd22e70d1d44

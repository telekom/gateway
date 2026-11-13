# SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
#
# SPDX-License-Identifier: Apache-2.0

# The echo server of sidecar_test.go, in place of kubectl proxy. The test reads the image of this line, so that
# Dependabot can update its tag and digest. Nothing builds this file.
FROM ghcr.io/telekom/pubsub-horizon-cosmoparrot:1.6.0@sha256:116830b4d73a5deb7fa3f5bea5f19317b0d620dd22f781fc09f1c92c3f7549cc

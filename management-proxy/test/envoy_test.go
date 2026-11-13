// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
//
// SPDX-License-Identifier: Apache-2.0

package test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestEnvoyAcceptsTheConfiguration validates the Envoy configuration of each value profile in chart/ci with the Envoy
// version of the chart.
func TestEnvoyAcceptsTheConfiguration(t *testing.T) {
	profiles, err := filepath.Glob(filepath.Join(chartDir, "ci", "*-values.yaml"))
	if err != nil || len(profiles) == 0 {
		t.Fatalf("no value profiles found: %v", err)
	}
	// Envoy loads the certificates of the SDS files, so they must exist.
	ca := newCA(t, "validate")
	cert, key := ca.issue(t, "validate.test")

	for _, profile := range profiles {
		t.Run(filepath.Base(profile), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			files := envoyFiles(renderEnvoyConfig(t, profile), cert, key, ca.PEM, ca.PEM)
			ctr, err := testcontainers.Run(ctx, readValues(t, profile).envoyImage(),
				testcontainers.WithFiles(files...),
				testcontainers.WithCmd("--mode", "validate", "--config-path", "/etc/envoy/envoy.yaml", "--log-level", "warn"),
				testcontainers.WithWaitStrategy(wait.ForExit()),
			)
			testcontainers.CleanupContainer(t, ctr)
			if err != nil {
				t.Fatal(err)
			}
			state, err := ctr.State(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if state.ExitCode != 0 {
				t.Fatalf("envoy --mode validate exited with %d:\n%s", state.ExitCode, containerLogs(ctr))
			}
		})
	}
}

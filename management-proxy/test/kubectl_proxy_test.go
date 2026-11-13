// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
//
// SPDX-License-Identifier: Apache-2.0

package test

import (
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestKubectlProxyAcceptPaths checks which paths the --accept-paths argument of kubectl proxy accepts. kubectl compiles
// it with the Go regexp package. kubectl proxy rejects the other paths with 403.
func TestKubectlProxyAcceptPaths(t *testing.T) {
	var deployment struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Name string   `yaml:"name"`
						Args []string `yaml:"args"`
					} `yaml:"containers"`
				} `yaml:"spec"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(renderTemplate(t, "templates/deployment.yaml", sidecarProfile), &deployment); err != nil {
		t.Fatal(err)
	}
	var acceptPaths *regexp.Regexp
	for _, c := range deployment.Spec.Template.Spec.Containers {
		for _, arg := range c.Args {
			if value, ok := strings.CutPrefix(arg, "--accept-paths="); ok && c.Name == "kubectl-proxy" {
				acceptPaths = regexp.MustCompile(value)
			}
		}
	}
	if acceptPaths == nil {
		t.Fatal("kubectl-proxy has no --accept-paths argument")
	}

	for path, want := range map[string]bool{
		// Discovery and OpenAPI
		"/version":    true,
		"/api":        true,
		"/api/v1":     true,
		"/apis":       true,
		"/openapi/v3": true,
		"/openapi/v3/apis/gateway.networking.k8s.io/v1": true,
		// The configured API groups
		"/apis/gateway.networking.k8s.io":                                       true,
		"/apis/gateway.networking.k8s.io/v1/namespaces/routes/httproutes":       true,
		"/apis/gateway.kgateway.dev/v1alpha1/namespaces/routes/trafficpolicies": true,
		// Other resources
		"/api/v1/namespaces/routes/secrets":               false,
		"/api/v1/pods":                                    false,
		"/apis/apps/v1/namespaces/routes/deployments":     false,
		"/apis/rbac.authorization.k8s.io/v1/clusterroles": false,
		"/version/x": false,
		// Names that only look like a configured group, and a group in the middle of the path
		"/apis/gateway.networking.k8s.iox/v1":             false,
		"/apis/gatewayxnetworking.k8s.io/v1":              false,
		"/apis/apps/v1/apis/gateway.networking.k8s.io/v1": false,
	} {
		if got := acceptPaths.MatchString(path); got != want {
			t.Errorf("accept %s: got %t, want %t", path, got, want)
		}
	}
}

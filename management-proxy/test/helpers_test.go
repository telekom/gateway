// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
//
// SPDX-License-Identifier: Apache-2.0

// Package test tests the Envoy sidecar of the management proxy chart in Docker, without a cluster.
package test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/testcontainers/testcontainers-go"
	"go.yaml.in/yaml/v3"
)

const (
	chartDir = "../chart"
	// The Envoy configuration expects the certificates at these paths.
	serverTLSDir = "/etc/management-proxy/tls"
	clientCADir  = "/etc/management-proxy/client-ca"
	jwksCADir    = "/etc/management-proxy/jwks-ca"
)

// chartValues holds the values that the tests need. Read it with readValues.
type chartValues struct {
	PathPrefix     string   `yaml:"pathPrefix"`
	AllowedClients []string `yaml:"allowedClients"`
	TLS            struct {
		Port              int `yaml:"port"`
		ClientCertificate struct {
			DNSName string `yaml:"dnsName"`
		} `yaml:"clientCertificate"`
	} `yaml:"tls"`
	Envoy struct {
		HealthPort int `yaml:"healthPort"`
		Image      struct {
			Repository string `yaml:"repository"`
			Tag        string `yaml:"tag"`
		} `yaml:"image"`
		GatewayToken struct {
			Issuer    string   `yaml:"issuer"`
			Audiences []string `yaml:"audiences"`
			JWKS      struct {
				URI string `yaml:"uri"`
			} `yaml:"jwks"`
		} `yaml:"gatewayToken"`
	} `yaml:"envoy"`
}

// readValues reads the default values of the chart, and then the values files in order. A later file overrides the keys
// that it sets.
func readValues(t *testing.T, files ...string) chartValues {
	t.Helper()
	var values chartValues
	for _, file := range append([]string{filepath.Join(chartDir, "values.yaml")}, files...) {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal(data, &values); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	return values
}

func (v chartValues) envoyImage() string {
	return v.Envoy.Image.Repository + ":" + v.Envoy.Image.Tag
}

// renderTemplate renders one template of the chart with the values file and the --set values.
func renderTemplate(t *testing.T, template, valuesFile string, set ...string) []byte {
	t.Helper()
	args := []string{"template", "test", chartDir, "--namespace", "test", "-f", valuesFile, "--show-only", template}
	for _, s := range set {
		args = append(args, "--set", s)
	}
	var stderr bytes.Buffer
	cmd := exec.Command("helm", args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm template: %v: %s", err, stderr.String())
	}
	return out
}

// renderEnvoyConfig returns the files of the rendered Envoy ConfigMap.
func renderEnvoyConfig(t *testing.T, valuesFile string, set ...string) map[string]string {
	t.Helper()
	var configMap struct {
		Data map[string]string `yaml:"data"`
	}
	if err := yaml.Unmarshal(renderTemplate(t, "templates/configmap-envoy.yaml", valuesFile, set...), &configMap); err != nil {
		t.Fatal(err)
	}
	return configMap.Data
}

// envoyFiles returns the Envoy configuration and the certificates as files for the sidecar container.
func envoyFiles(config map[string]string, serverCert, serverKey, clientCA, jwksCA []byte) []testcontainers.ContainerFile {
	files := []testcontainers.ContainerFile{
		file(serverTLSDir+"/tls.crt", serverCert),
		file(serverTLSDir+"/tls.key", serverKey),
		file(clientCADir+"/ca.crt", clientCA),
		file(jwksCADir+"/ca.crt", jwksCA),
	}
	for name, content := range config {
		files = append(files, file("/etc/envoy/"+name, []byte(content)))
	}
	return files
}

func file(path string, content []byte) testcontainers.ContainerFile {
	return testcontainers.ContainerFile{Reader: bytes.NewReader(content), ContainerFilePath: path, FileMode: 0o644}
}

// containerLogs returns the logs of a container, for the error message of a failed test.
func containerLogs(ctr testcontainers.Container) string {
	logs, err := ctr.Logs(context.Background())
	if err != nil {
		return err.Error()
	}
	defer logs.Close()
	out, _ := io.ReadAll(logs)
	return string(out)
}

// certificateAuthority issues certificates for the tests.
type certificateAuthority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	// PEM is the CA certificate in PEM format.
	PEM []byte
}

func newCA(t *testing.T, name string) *certificateAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &certificateAuthority{cert: cert, key: key, PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue returns a certificate for the DNS name and its key, both in PEM format. The certificate is valid for server
// and client authentication.
func (ca *certificateAuthority) issue(t *testing.T, dnsName string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// tlsCertificate returns a certificate for the DNS name for a Go TLS client or server.
func (ca *certificateAuthority) tlsCertificate(t *testing.T, dnsName string) tls.Certificate {
	t.Helper()
	cert, err := tls.X509KeyPair(ca.issue(t, dnsName))
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// signingKey signs Gateway Tokens with RS256.
type signingKey struct {
	key *rsa.PrivateKey
}

func newSigningKey(t *testing.T) signingKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return signingKey{key: key}
}

// jwks returns the public key as a JSON Web Key Set.
func (k signingKey) jwks(t *testing.T) []byte {
	t.Helper()
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
		{Key: &k.key.PublicKey, KeyID: "test", Algorithm: string(jose.RS256), Use: "sig"},
	}}
	data, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// sign returns a signed JWT with the claims.
func (k signingKey) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: k.key, KeyID: "test"}},
		(&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// unsignedToken returns a JWT with the algorithm "none" and no signature.
func unsignedToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + enc.EncodeToString(payload) + "."
}

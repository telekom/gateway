// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
//
// SPDX-License-Identifier: Apache-2.0

package test

import (
	"bufio"
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// kubectl proxy listens on this port of the loopback address, see templates/deployment.yaml.
	kubectlProxyPort = "8001"
	// The host name of the sidecar in its server certificate.
	sidecarHost = "management-proxy.test"
	routesPath  = "/apis/gateway.networking.k8s.io/v1/namespaces/routes/httproutes"
	waitTimeout = 30 * time.Second
)

var sidecarProfile = filepath.Join(chartDir, "ci", "sidecar-values.yaml")

// sidecarSetup selects the values that change between the runs of TestSidecar.
type sidecarSetup struct {
	mtls           bool
	requestBinding bool
	// Empty = the chart default
	jwksCacheDuration string
}

// TestSidecar runs the sidecar with the rendered Envoy configuration in Docker: with TLS, with mutual TLS, and without
// request binding.
//
// An echo server in the network namespace of the sidecar stands in for kubectl proxy, so the test can check the
// forwarded request. The JWKS endpoint is an HTTPS server in the test process. Its certificate can change while the
// test runs. The test signs its own Gateway Tokens.
func TestSidecar(t *testing.T) {
	setups := map[string]sidecarSetup{
		"TLS":                {mtls: false, requestBinding: true},
		"mutual TLS":         {mtls: true, requestBinding: true},
		"no request binding": {mtls: false, requestBinding: false},
	}
	for name, setup := range setups {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			testSidecar(t, setup)
		})
	}
}

// TestSidecarReloadsJWKSCA changes the certificate of the JWKS endpoint to one of a new CA, and then changes the CA
// ConfigMap. The sidecar must use the new CA without a restart.
func TestSidecarReloadsJWKSCA(t *testing.T) {
	t.Parallel()
	s := startSidecar(t, sidecarSetup{requestBinding: true, jwksCacheDuration: "2s"})
	s.expectJWKSRecovery(t)

	s.jwks.setCertificate(s.otherCA.tlsCertificate(t, s.jwks.host))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := s.do(request{token: s.token(t, nil)})
		if assert.NoError(c, err) {
			assert.Equal(c, http.StatusUnauthorized, resp.status)
		}
	}, waitTimeout, time.Second, "the sidecar rejects a valid token when the cached keys expire and the new CA is unknown")

	s.jwksCA.update(t, s.otherCA.PEM)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := s.do(request{token: s.token(t, nil)})
		if assert.NoError(c, err) {
			assert.Equal(c, http.StatusOK, resp.status)
		}
	}, waitTimeout, time.Second, "the sidecar reloads the CA and fetches the keys again")
}

func testSidecar(t *testing.T, setup sidecarSetup) {
	s := startSidecar(t, setup)
	s.expectJWKSRecovery(t)

	t.Run("forwarded request", s.testForwardedRequest)
	t.Run("Gateway Token", func(t *testing.T) {
		s.testGatewayToken(t, setup)
	})
	t.Run("TLS", func(t *testing.T) {
		s.testTLS(t, setup)
	})
	t.Run("health port", s.testHealthPort)
}

func (s *sidecar) testForwardedRequest(t *testing.T) {
	t.Helper()
	resp, err := s.do(request{path: routesPath + "?limit=1", token: s.token(t, nil), headers: map[string]string{
		"X-Request-Id":            "test-request-1",
		"Audit-Id":                "forged",
		"Impersonate-User":        "system:admin",
		"Impersonate-Group":       "system:masters",
		"Impersonate-Extra-Scope": "all",
		"Impersonate-Uid":         "1",
		"Proxy-Authorization":     "Basic eDp4",
	}})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.status)
	forwarded := parseEcho(t, resp.body)

	assert.Equal(t, routesPath+"?limit=1", forwarded.RequestURI, "the path and the query reach kubectl proxy unchanged")
	assert.Equal(t, "localhost", forwarded.Header.Get("Host"))
	assert.Empty(t, forwarded.Header.Get("Authorization"), "the token is removed")
	assert.Empty(t, forwarded.Header.Get("Proxy-Authorization"))
	for name := range forwarded.Header {
		assert.False(t, strings.HasPrefix(name, "Impersonate-"), "the header %s is removed", name)
	}
	assert.Equal(t, "test-request-1", forwarded.Header.Get("Audit-Id"), "Audit-Id is the request ID of the caller")
}

func (s *sidecar) testGatewayToken(t *testing.T, setup sidecarSetup) {
	t.Helper()
	prefix := s.values.PathPrefix
	tests := []struct {
		name   string
		method string
		path   string
		token  string
		want   int
		// The expected status without request binding, if it differs from want
		wantWithoutBinding int
	}{
		{name: "no token", want: 401},
		{name: "unsigned token", token: unsignedToken(t, s.claims(nil)), want: 401},
		{name: "token of another key", token: newSigningKey(t).sign(t, s.claims(nil)), want: 401},
		{name: "token of another issuer", token: s.token(t, claims{"iss": "https://other.test"}), want: 401},
		{name: "expired token", token: s.token(t, claims{"exp": time.Now().Add(-5 * time.Minute).Unix()}), want: 401},
		{name: "token without expiry", token: s.token(t, claims{"exp": nil}), want: 401},
		{name: "token that expires after the maximum lifetime",
			token: s.token(t, claims{"exp": time.Now().Add(11 * time.Minute).Unix()}), want: 401},
		// Envoy rejects an audience that is not allowed with 403.
		{name: "token without audience", token: s.token(t, claims{"aud": nil}), want: 403},
		{name: "token of another audience", token: s.token(t, claims{"aud": "other"}), want: 403},
		{name: "token in the query instead of the header", path: routesPath + "?access_token=" + s.token(t, nil), want: 401},
		{name: "token of the second allowed client", token: s.token(t, claims{"clientId": s.values.AllowedClients[1]}), want: 200},
		{name: "token of another client", token: s.token(t, claims{"clientId": "other-client"}), want: 403},
		{name: "token without client", token: s.token(t, claims{"clientId": nil}), want: 403},
		{name: "token of another route prefix", token: s.token(t, claims{"requestPath": "/other" + routesPath}), want: 403},
		{name: "token of a prefix that only starts like the route prefix",
			token: s.token(t, claims{"requestPath": prefix + "x" + routesPath}), want: 403},
		{name: "token without requestPath", token: s.token(t, claims{"requestPath": nil}), want: 403},
		{name: "token for another path", path: "/api", token: s.token(t, nil), want: 403, wantWithoutBinding: 200},
		{name: "token for another method", token: s.token(t, claims{"operation": "DELETE"}), want: 403,
			wantWithoutBinding: 200},
		{name: "token without operation", token: s.token(t, claims{"operation": nil}), want: 403, wantWithoutBinding: 200},
		{name: "DELETE with a token for DELETE", method: http.MethodDelete, path: routesPath + "/x",
			token: s.token(t, claims{"operation": "DELETE", "requestPath": prefix + routesPath + "/x"}), want: 200},
		{name: "path with escaped slashes", path: routesPath + "%2Fx",
			token: s.token(t, claims{"requestPath": prefix + routesPath + "%2Fx"}), want: 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.want
			if !setup.requestBinding {
				want = cmp.Or(tt.wantWithoutBinding, tt.want)
			}
			resp, err := s.do(request{method: tt.method, path: tt.path, token: tt.token})
			require.NoError(t, err)
			assert.Equal(t, want, resp.status)
		})
	}
}

func (s *sidecar) testTLS(t *testing.T, setup sidecarSetup) {
	t.Helper()
	token := s.token(t, nil)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + s.address + routesPath)
	if err == nil {
		resp.Body.Close()
	}
	assert.Error(t, err, "plain HTTP is rejected")

	ok, err := s.do(request{token: token, maxVersion: tls.VersionTLS12})
	if assert.NoError(t, err, "TLS 1.2 is accepted") {
		assert.Equal(t, http.StatusOK, ok.status)
	}
	_, err = s.do(request{token: token, minVersion: tls.VersionTLS10, maxVersion: tls.VersionTLS11})
	assert.ErrorContains(t, err, "tls:", "TLS 1.1 is rejected")

	if !setup.mtls {
		ok, err := s.do(request{token: token, noClientCert: true})
		if assert.NoError(t, err, "no client certificate is needed without mutual TLS") {
			assert.Equal(t, http.StatusOK, ok.status)
		}
		return
	}
	otherSAN := s.ca.tlsCertificate(t, "other.test")
	otherCA := s.otherCA.tlsCertificate(t, s.values.TLS.ClientCertificate.DNSName)
	_, err = s.do(request{token: token, noClientCert: true})
	assert.ErrorContains(t, err, "tls:", "no client certificate")
	_, err = s.do(request{token: token, clientCert: &otherSAN})
	assert.ErrorContains(t, err, "tls:", "client certificate with another SAN")
	_, err = s.do(request{token: token, clientCert: &otherCA})
	assert.ErrorContains(t, err, "tls:", "client certificate of another CA")
}

func (s *sidecar) testHealthPort(t *testing.T) {
	t.Helper()
	for _, tt := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/ready", http.StatusOK},
		{http.MethodGet, "/stats/prometheus", http.StatusOK},
		// No Envoy admin API
		{http.MethodGet, "/config_dump", http.StatusNotFound},
		{http.MethodPost, "/quitquitquit", http.StatusNotFound},
	} {
		status, err := s.health(tt.method, tt.path)
		require.NoError(t, err)
		assert.Equal(t, tt.want, status, "%s %s", tt.method, tt.path)
	}
}

// sidecar is a running sidecar with its echo server and JWKS endpoint.
type sidecar struct {
	values     chartValues
	ca         *certificateAuthority
	otherCA    *certificateAuthority
	signingKey signingKey
	jwks       *jwksServer
	jwksCA     *configMapVolume
	clientCert *tls.Certificate // nil without mutual TLS
	// Addresses of the TLS port and the health port on the host
	address       string
	healthAddress string
}

func startSidecar(t *testing.T, setup sidecarSetup) *sidecar {
	ctx := context.Background()
	values := readValues(t, sidecarProfile)
	s := &sidecar{
		values:     values,
		ca:         newCA(t, "test-ca"),
		otherCA:    newCA(t, "other-ca"),
		signingKey: newSigningKey(t),
	}
	s.jwksCA = newConfigMapVolume(t, "ca.crt", s.ca.PEM)
	jwksURI, err := url.Parse(values.Envoy.GatewayToken.JWKS.URI)
	require.NoError(t, err)
	s.jwks = startJWKSServer(t, jwksURI.Hostname(), s.signingKey.jwks(t), s.ca.tlsCertificate(t, "other.test"))
	if setup.mtls {
		cert := s.ca.tlsCertificate(t, values.TLS.ClientCertificate.DNSName)
		s.clientCert = &cert
	}

	// The sidecar reaches the JWKS server in the test process through the host name of testcontainers.
	set := []string{
		"tls.clientCertificate.enabled=" + strconv.FormatBool(setup.mtls),
		"envoy.gatewayToken.requestBinding=" + strconv.FormatBool(setup.requestBinding),
		"envoy.gatewayToken.jwks.address=" + testcontainers.HostInternal,
		"envoy.gatewayToken.jwks.port=" + strconv.Itoa(s.jwks.port),
	}
	if setup.jwksCacheDuration != "" {
		set = append(set, "envoy.gatewayToken.jwks.cacheDuration="+setup.jwksCacheDuration)
	}
	config := renderEnvoyConfig(t, sidecarProfile, set...)
	serverCert, serverKey := s.ca.issue(t, sidecarHost)
	tlsPort := strconv.Itoa(values.TLS.Port) + "/tcp"
	healthPort := strconv.Itoa(values.Envoy.HealthPort) + "/tcp"
	ctr, err := testcontainers.Run(ctx, values.envoyImage(),
		testcontainers.WithFiles(envoyFiles(config, serverCert, serverKey, s.ca.PEM)...),
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.Binds = append(hc.Binds, s.jwksCA.dir+":"+jwksCADir+":ro")
		}),
		testcontainers.WithCmd("--config-path", "/etc/envoy/envoy.yaml", "--log-level", "warn", "--disable-hot-restart"),
		testcontainers.WithExposedPorts(tlsPort, healthPort),
		testcontainers.WithHostPortAccess(s.jwks.port),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/stats/prometheus").WithPort(healthPort)),
	)
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("sidecar logs:\n%s", containerLogs(ctr))
		}
	})
	s.address, err = ctr.PortEndpoint(ctx, tlsPort, "")
	require.NoError(t, err)
	s.healthAddress, err = ctr.PortEndpoint(ctx, healthPort, "")
	require.NoError(t, err)

	status, err := s.health(http.MethodGet, "/ready")
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, status, "the sidecar is not ready while kubectl proxy is down")

	echo, err := testcontainers.Run(ctx, echoImage(t),
		testcontainers.WithEnv(map[string]string{"COSMOPARROT_PORT": kubectlProxyPort}),
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.NetworkMode = container.NetworkMode("container:" + ctr.GetContainerID())
		}),
	)
	testcontainers.CleanupContainer(t, echo)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		status, err := s.health(http.MethodGet, "/ready")
		if assert.NoError(c, err) {
			assert.Equal(c, http.StatusOK, status)
		}
	}, waitTimeout, time.Second, "the sidecar is ready when kubectl proxy accepts connections")
	return s
}

// claims changes the default claims of a valid token. A nil value removes the claim.
type claims map[string]any

func (s *sidecar) claims(changes claims) map[string]any {
	now := time.Now()
	c := map[string]any{
		"iss":         s.values.Envoy.GatewayToken.Issuer,
		"aud":         s.values.Envoy.GatewayToken.Audiences[0],
		"clientId":    s.values.AllowedClients[0],
		"operation":   http.MethodGet,
		"requestPath": s.values.PathPrefix + routesPath,
		"iat":         now.Unix(),
		"exp":         now.Add(time.Minute).Unix(),
	}
	maps.Copy(c, changes)
	maps.DeleteFunc(c, func(_ string, v any) bool { return v == nil })
	return c
}

// token returns a valid Gateway Token for GET on routesPath, with the changes.
func (s *sidecar) token(t *testing.T, changes claims) string {
	return s.signingKey.sign(t, s.claims(changes))
}

// request is a request to the TLS port of the sidecar. Empty fields use the defaults: GET on routesPath, no token, and
// the client certificate of the test with mutual TLS.
type request struct {
	method, path, token    string
	headers                map[string]string
	clientCert             *tls.Certificate
	noClientCert           bool
	minVersion, maxVersion uint16
}

type response struct {
	status int
	body   string
}

// do sends the request on a new connection, so that each request makes its own TLS handshake.
func (s *sidecar) do(r request) (*response, error) {
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(s.ca.PEM)
	config := &tls.Config{RootCAs: roots, ServerName: sidecarHost, MinVersion: r.minVersion, MaxVersion: r.maxVersion}
	if cert := cmp.Or(r.clientCert, s.clientCert); cert != nil && !r.noClientCert {
		config.Certificates = []tls.Certificate{*cert}
	}
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: config, DisableKeepAlives: true},
	}
	req, err := http.NewRequest(cmp.Or(r.method, http.MethodGet), "https://"+s.address+cmp.Or(r.path, routesPath), nil)
	if err != nil {
		return nil, err
	}
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	for name, value := range r.headers {
		req.Header.Set(name, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &response{status: resp.StatusCode, body: string(body)}, nil
}

// health returns the status code of a request to the health port.
func (s *sidecar) health(method, path string) (int, error) {
	req, err := http.NewRequest(method, "http://"+s.healthAddress+path, nil)
	if err != nil {
		return 0, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// expectJWKSRejected waits until Envoy counts a TLS error of the kind on the JWKS cluster, for example
// fail_verify_san. Then it checks that the sidecar rejects a valid token, because it has no keys.
func (s *sidecar) expectJWKSRejected(t *testing.T, counter string) {
	t.Helper()
	metric := fmt.Sprintf(`envoy_cluster_ssl_%s{envoy_cluster_name="jwks"} `, counter)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + s.healthAddress + "/stats/prometheus")
		if !assert.NoError(c, err) {
			return
		}
		defer resp.Body.Close()
		count := 0
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if value, ok := strings.CutPrefix(scanner.Text(), metric); ok {
				count, _ = strconv.Atoi(value)
			}
		}
		if !assert.NoError(c, scanner.Err(), "scan Prometheus metrics") {
			return
		}
		assert.Positive(c, count, metric)
	}, waitTimeout, time.Second, "the sidecar rejects the JWKS certificate (%s)", counter)

	resp, err := s.do(request{token: s.token(t, nil)})
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.status,
		"fail closed: a valid token is rejected while the sidecar has no keys")
}

// expectJWKSRecovery checks that the sidecar rejects invalid JWKS certificates before accepting a trusted one.
func (s *sidecar) expectJWKSRecovery(t *testing.T) {
	t.Helper()

	// Run before the subtests because Envoy caches the keys it fetches.
	s.expectJWKSRejected(t, "fail_verify_san")
	s.jwks.setCertificate(s.otherCA.tlsCertificate(t, s.jwks.host))
	s.expectJWKSRejected(t, "fail_verify_error")

	s.jwks.setCertificate(s.ca.tlsCertificate(t, s.jwks.host))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := s.do(request{token: s.token(t, nil)})
		if assert.NoError(c, err) {
			assert.Equal(c, http.StatusOK, resp.status)
		}
	}, waitTimeout, time.Second, "the sidecar fetches the keys again and accepts a valid token")
}

// echoImage returns the image of the echo server in echo.Dockerfile. The echo server stands in for kubectl proxy. It
// answers each request with the request that it received, as JSON.
func echoImage(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("echo.Dockerfile")
	require.NoError(t, err)
	for line := range strings.Lines(string(data)) {
		if image, ok := strings.CutPrefix(strings.TrimSpace(line), "FROM "); ok {
			return image
		}
	}
	t.Fatal("echo.Dockerfile has no FROM line")
	return ""
}

// forwardedRequest is the request that the echo server received.
type forwardedRequest struct {
	RequestURI string
	Header     http.Header
}

// parseEcho reads the request that the echo server received from its JSON response.
func parseEcho(t *testing.T, body string) forwardedRequest {
	t.Helper()
	var echo struct {
		RequestURI string              `json:"requestUri"`
		Headers    map[string][]string `json:"headers"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &echo), "the response of the echo server:\n%s", body)
	header := http.Header{}
	for name, values := range echo.Headers {
		for _, value := range values {
			header.Add(name, value)
		}
	}
	return forwardedRequest{RequestURI: echo.RequestURI, Header: header}
}

// jwksServer is the HTTPS JWKS endpoint of the sidecar. Its certificate can change while it runs.
type jwksServer struct {
	host string
	port int
	cert atomic.Pointer[tls.Certificate]
}

func startJWKSServer(t *testing.T, host string, jwks []byte, cert tls.Certificate) *jwksServer {
	s := &jwksServer{host: host}
	s.setCertificate(cert)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwks)
	}))
	// Envoy fails the TLS handshake on purpose in some tests. Do not log it.
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	// Each fetch makes a new connection with a full handshake, so Envoy verifies the current certificate. Keep-alive
	// would reuse a connection, and a session ticket would skip the certificate check.
	server.Config.SetKeepAlivesEnabled(false)
	server.TLS = &tls.Config{
		SessionTicketsDisabled: true,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return s.cert.Load(), nil
		},
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	s.port = server.Listener.Addr().(*net.TCPAddr).Port
	return s
}

func (s *jwksServer) setCertificate(cert tls.Certificate) {
	s.cert.Store(&cert)
}

# Management Proxy

The management proxy lets the distributed control plane (dCP) reach the Kubernetes API of a data-plane cluster. An Envoy
sidecar authenticates each request. `kubectl proxy` forwards it to the Kubernetes API with a local ServiceAccount. This
guide describes how we package, configure, operate and test it, and what a proof of concept (PoC) and the minimum viable
product (MVP) showed.

| | |
| --- | --- |
| Status | MVP: the Helm chart in [`management-proxy/chart`](../../management-proxy/chart). Tested with the current Kong gateway in place of kgateway. Not tested with kgateway and the Gateway Token ExtProc. |
| Decisions | [ADR 0004](../decisions/0004-use-an-oauth-proxy-for-cross-cluster-configuration.md): the dCP configures data-plane clusters through an OAuth-protected Kubernetes API proxy. [ADR 0005](../decisions/0005-reuse-an-existing-kubernetes-api-proxy.md): reuse an Envoy sidecar with `kubectl proxy`. |
| Terms | [ADR 0004](../decisions/0004-use-an-oauth-proxy-for-cross-cluster-configuration.md#context-and-problem-statement) |

## Overview

The diagram shows where the management proxy sits on the path from `roverctl` to the gateway proxies.
[ADR 0004](../decisions/0004-use-an-oauth-proxy-for-cross-cluster-configuration.md#write-and-clean-up-target-resources-safely)
shows the sequence of one configuration change.

```mermaid
flowchart LR
  rover["roverctl"]

  subgraph cp["Control-plane cluster"]
    api["Rover API"]
    op["dCP Gateway operator<br/>kgateway FeatureBuilder<br/>kgateway client and watch cache"]
  end

  iris["Iris"]

  subgraph dp["Data-plane cluster"]
    route["kgateway management route<br/>JWT check, LMS Gateway Token"]
    subgraph mp["Management proxy pod"]
      envoy["envoy<br/>validates the LMS token"]
      kp["kubectl-proxy<br/>ServiceAccount token"]
    end
    kube["kube-apiserver<br/>RBAC, validation, admission"]
    ctl["kgateway controller"]
    gw["Gateway proxies<br/>serve the API traffic"]
  end

  rover -->|"rover.yaml"| api --> op
  op -->|"client credentials"| iris
  op -->|"Kubernetes API calls<br/>Iris access token"| route
  route -->|"TLS"| envoy
  envoy -->|"loopback"| kp --> kube
  kube -->|"watch"| ctl
  ctl -->|"xDS"| gw
```

## Packaging

Run Envoy and `kubectl proxy` as two containers with their upstream images. Build no image of our own. The PoC first
used one combined image in two containers. We changed it to the upstream images.

| Option | Good | Bad |
| --- | --- | --- |
| One container, combined image | One process list and one image. | Needs a process supervisor in the container. Kubernetes cannot restart one process alone, and probes cannot check each process. The exposed process shares the ServiceAccount token. We must build, scan and update the image. |
| Two containers, combined image | Kubernetes restarts each process alone. | Each container has the binary of the other. We must still build, scan and update the image. |
| Two containers, upstream images (chosen) | Nothing to build. Each image gets security fixes from its upstream project. Each container has only its own binary. Only the forwarder gets the ServiceAccount token. | Two image versions to track. |

Containers in one pod share the network namespace. So Envoy reaches `kubectl proxy` on the loopback address, and no
other pod can reach `kubectl proxy`.

The Helm chart in [`management-proxy/chart`](../../management-proxy/chart) packages the Deployment, the Envoy
configuration, the ServiceAccount and its RBAC, the certificates, a NetworkPolicy, a PodDisruptionBudget and the
management route. The values file documents each setting. The gateway chart can include it as a subchart.

## Pod layout

Run the management proxy as a separate Deployment with its own ServiceAccount. kgateway routes the management traffic to
the Service of this Deployment. Do not put `kubectl proxy` into the gateway pod. It would share the identity of the
gateway pod and would need supported deployment customization.

| Container | Image | Task |
| --- | --- | --- |
| `envoy` | `envoyproxy/envoy`, distroless variant | Listens on the Service port. Validates the LMS Gateway Token, authorizes the client and removes the token. Forwards to `127.0.0.1:8001` with the Host `localhost`. |
| `kubectl-proxy` | `registry.k8s.io/kubectl` | Runs `kubectl proxy --address=127.0.0.1 --port=8001 --accept-hosts=^localhost$ --accept-paths=…`. Adds the ServiceAccount token. |

Mount the ServiceAccount token only into `kubectl-proxy`. Set `automountServiceAccountToken: false` on the pod. Add the
volume that Kubernetes mounts automatically as an explicit projected volume. The kubelet rotates the token, and
client-go reads the [token file again][token-file]. The
[Deployment template](../../management-proxy/chart/templates/deployment.yaml) shows the volume.

Run both containers as non-root, with a read-only root file system and without capabilities. `kubectl proxy` needs a
writable `HOME`, for example an `emptyDir` on `/tmp`. The `kubectl` image runs as root by default, so the chart sets the
user ID 65532 for the pod. Pin both images by digest.

`--accept-paths` allows only discovery, OpenAPI and the API groups in `kubectlProxy.apiGroups`. By default, these are
the Gateway API and kgateway groups. `kubectl proxy` rejects other paths with `403`. RBAC stays the authority.

On shutdown, a `preStop` sleep keeps Envoy running for `deployment.shutdownDelaySeconds`, so that endpoints and gateways
stop to send new requests. Then Envoy stops and ends the open watches. `kubectl-proxy` stops 5 seconds after Envoy. The
chart runs 2 replicas with a PodDisruptionBudget, and it spreads them over nodes if possible.

### Probes

Only Envoy has a probe: a readiness probe on `/ready` of the health port, see
[Configure the Envoy sidecar](#configure-the-envoy-sidecar). `/ready` fails while `kubectl proxy` does not accept TCP
connections. A pod is ready only when all its containers are ready, so this probe covers both containers. If a process
stops, the kubelet restarts its container without a probe.

| Probe | Decision | Reason |
| --- | --- | --- |
| Liveness probe for Envoy | Not used | It only finds an Envoy that runs but does not answer, and we have not seen this. A restart ends all open watches of the replica, so a false failure has a high cost. If we add it, use a separate path, for example `/healthz` with a `direct_response` of `200`. Do not use `/ready`: it fails when `kubectl proxy` fails and would restart the wrong container. |
| Startup probe for Envoy | Not used | It is only necessary together with a liveness probe. Envoy opens its listeners only after the [initialization][envoy-init] of all clusters: the DNS lookup of the JWKS endpoint and one round of health checks. A startup probe then prevents restarts while DNS is slow. |
| Probes for `kubectl proxy` | Not possible without a cost | The kubelet probes the pod IP, but `kubectl proxy` listens only on `127.0.0.1`. Do not listen on all addresses: the proxy has no authentication and sends the ServiceAccount token. The image has no shell and no HTTP client. An `exec` probe with `kubectl get --raw` starts a full `kubectl` process for each probe, and it fails when the API server is not available. |

A `kubectl proxy` that runs but does not answer makes `/ready` fail, so the pod leaves the endpoints of the Service. No
probe restarts the container in this case.

`envoy.maxDownstreamConnections` limits the connections of all listeners together, also of the health listener. When the
TLS listener has all connections, for example many open watches, Envoy refuses new connections on the health port. The
readiness probe and the metrics scrape then fail. The pod leaves the endpoints, and new connections go to the other
replicas. A liveness probe on the health port would restart this Envoy and end all its watches. To prevent this, set
`ignore_global_conn_limit: true` on the health listener before you add a liveness probe. Readiness then does not show a
full TLS listener.

## Configure the Envoy sidecar

The sidecar uses a static configuration with one route to `kubectl proxy`. It does not route. The
[Envoy configuration template](../../management-proxy/chart/templates/configmap-envoy.yaml) is the reference. In this
order:

1. The TLS listener presents the server certificate. It accepts only TLS 1.2 or 1.3 with forward secrecy, and no plain
   HTTP. File-based SDS reloads the certificates when the kubelet updates the Secret volumes. With mutual TLS
   (`tls.clientCertificate.enabled`), it also requires a client certificate of the configured CA with the DNS SAN
   `tls.clientCertificate.dnsName`.
2. The `jwt_authn` filter validates the LMS Gateway Token: signature, issuer, expiry and, if configured, the audience.
   It reads the token only from the `Authorization` header. It gets the keys from the JWKS endpoint of the gateway over
   HTTPS, and verifies the certificate and the host name of the endpoint. It removes the validated token from the
   request, because `forward` is `false` by default. This step is required: the client-go token transport
   [keeps an existing `Authorization` header][bearer-keep]. A forwarded caller token would reach the API server instead
   of the ServiceAccount token.
3. The `rbac` filter allows only the configured clients. It compares the client claim, and the `requestPath` claim with
   the prefix of the management route. It uses `sourced_metadata` for these principals. Envoy v1.40
   [deprecates the `metadata` principal][envoy-sourced-metadata]. A CEL condition binds the token to the request, see
   below.
4. The `header_mutation` filter removes all `Impersonate-*` headers. The ServiceAccount has no `impersonate` permission,
   so this is a second line of defense.
5. The router forwards the request. It rewrites the Host to `localhost`, because `kubectl proxy` has a default host
   filter. It removes the `Authorization` and `Proxy-Authorization` headers, and sets the `Audit-ID` header to the
   request ID. It has no retry policy, a route timeout of `0s` and a maximum stream duration.

The listener normalizes the path, merges slashes and rejects escaped slashes with `400`. Envoy rejects a token without
an allowed audience with `403`, not with `401`. The access log is JSON on stdout. It contains the request ID, the client
ID, the method, the path with the query, the status, the response flags and the duration. It contains no token.

The sidecar keeps the request ID of the gateway (`X-Request-Id`) and sets it as the `Audit-ID` header. The API server
uses this header as the `auditID` of its audit event and returns it in the response. So one ID connects the gateway log,
the sidecar log with the client ID, and the Kubernetes audit event. This is a provisional answer to the audit question
on the internal wiki page [Cross-cluster Configuration][security-wiki]. The caller can choose the request ID. Only
callers with a valid Gateway Token reach the API server.

A second listener on the health port serves only `/ready` and `/stats/prometheus`. `/ready` fails while `kubectl proxy`
does not accept TCP connections. Other paths of the Envoy admin API return `404`. The Service does not expose this port.

The LMS Gateway Token carries these claims:

- `clientId`: the client of the caller.
- `operation`: the HTTP method.
- `requestPath`: the path on the gateway, with the prefix of the route and without the query.
- `exp`: the same expiry as the caller token.

With `envoy.gatewayToken.requestBinding` (default: on), the sidecar binds each token to one request. The RBAC policy has
this CEL condition. A missing claim fails the condition, so the sidecar rejects the request with `403`:

```cel
request.method == metadata.filter_metadata['envoy.filters.http.jwt_authn']['gateway_token']['operation'] &&
'<pathPrefix>' + request.url_path == metadata.filter_metadata['envoy.filters.http.jwt_authn']['gateway_token']['requestPath']
```

Envoy v1.40 accepts only parsed CEL expressions in the RBAC filter. It rejects `cel_expr_string`. So the chart renders
the expression as a syntax tree. `request.url_path` is the path without the query, after the gateway removed the route
prefix. jumper sets the claims in this format. The Gateway Token ExtProc must set them in the same format.

The sidecar depends on the JWKS endpoint. While it has no valid keys in its cache, it rejects every request. After a
failed fetch, it fetches the keys again after 10 seconds. Test key rotation with the key rotator of the gateway.

## Keep watches working

A watch is one long HTTP response. The proxies check the token only when the request starts. The
[client-go reflector][reflector-timeout] ends each watch after a random time of 5 to 10 minutes. It
[resumes from the last `resourceVersion`][reflector-resume] and requests bookmarks. The API server sends a bookmark
about [once a minute][bookmark-frequency]. After `410 Gone`, the reflector [lists again][reflector-relist]. So a watch
survives the loss of a hop, if the client sees the loss.

A hop that reopens a watch without telling the client breaks this. With the same `resourceVersion`, the API server sends
events again that the client already has. Without a `resourceVersion`, the API server sends synthetic `ADDED` events for
all objects. In both cases, the client does not know about the restart. Follow these rules:

| Rule | Reason |
| --- | --- |
| No hop retries or reopens a request. Configure no retry on the management HTTPRoute, on its TrafficPolicy or on the sidecar route. | Envoy retries only with a `retry_policy`. A retry after a timeout hides the end of the stream from the client. |
| No hop buffers. Forward the response headers and each event at once. | Buffering delays events. Some proxies retry only while no response byte reached the client. |
| The idle timeout of each hop is longer than the bookmark interval, for example 15 minutes. | Bookmarks keep a quiet watch active. |
| The maximum stream duration is longer than the 10-minute maximum watch of client-go. | The client ends its own watches. The limit only ends watches of clients that do not. |
| A failing or stopping hop ends the client stream. Drain Envoy on shutdown. | The client reconnects to another replica and resumes. |
| Run at least 2 replicas with a PodDisruptionBudget and a readiness probe. | New watches go to a live replica during a rollout or a node drain. |
| The dCP uses informers, not raw watches. It reconciles to the desired state and applies with server-side apply. | A duplicate or replayed event causes at most an apply without changes. |

Configure each hop for streaming:

| Hop | Setting |
| --- | --- |
| kgateway management route | The [Envoy default route timeout][envoy-timeouts] is 15 seconds and stops watches. Set [`timeouts.request`][kgateway-timeouts] to `0s` in the TrafficPolicy of the route. Set `timeouts.streamIdle` longer than the bookmark interval. Envoy closes idle streams after 5 minutes by default. kgateway v2.3.0 has no field for a maximum stream duration. |
| Envoy sidecar | Set the route timeout to `0s` and the stream idle timeout as above. Do not add a buffering filter. Set `max_stream_duration` as an upper limit for clients that do not end their watches. |
| `kubectl proxy` | It [flushes streamed responses][kubectl-flush] every 200 milliseconds and sets no server timeouts. No setting is necessary. |
| kube-apiserver | It uses the `timeoutSeconds` value of the watch request. |

On kgateway, the TrafficPolicy of the management route sets the route timeouts: `request: 0s` and `streamIdle: 15m`.
`timeouts.streamIdle` overrides the idle timeout of the listener for this route only. The
[route template](../../management-proxy/chart/templates/route.yaml) of the chart shows the TrafficPolicy.

## Configure the management route on kgateway

Require valid JWTs in the `Authorization` header. Configure an explicit issuer and a rule for authorized clients. Check
the audience if the issuer sets one. An Iris client-credentials token that we inspected had no `aud` claim. The
[OAuth2/OIDC extension][kgateway-oidc] for browser login is a different flow. A tagged
[translator fixture][kgateway-jwt-rbac] shows CEL authorization based on JWT claims. The
[JWT translator][kgateway-jwt-translation] translates `forwardToken: false` into the removal of the validated token
header. Forward native Kubernetes requests without changes. Opaque tokens need an external-auth path.

The chart renders the management route in the release namespace:

- An HTTPRoute with a `PathPrefix` match on `pathPrefix` and a `URLRewrite` that removes the prefix.
- A TrafficPolicy with the timeouts, the JWT provider and the ExtProc of the gateway, and a CEL rule that checks the
  issuer and the client claim of the caller token. The gateway chart owns the two GatewayExtensions. The chart only
  references them.
- A BackendConfigPolicy for TLS to the sidecar, with the CA of the sidecar certificate, SNI and a SAN check. Without
  mutual TLS, it sets `simpleTLS`, so kgateway sends no client certificate. With mutual TLS, it references the client
  certificate of kgateway.

The gateway signs the LMS Gateway Token of the management route with its usual signing key. A separate key gives no
additional protection, see
[ADR 0005](../decisions/0005-reuse-an-existing-kubernetes-api-proxy.md#protect-the-management-proxy-from-in-cluster-access).

Set `route.enabled: false` only for a test gateway that is not kgateway, for example Kong in the MVP. The API server
accepts these resources with the kgateway v2.3.0 CRDs. We have not tested them with a running kgateway. Open: the
per-route data that the ExtProc needs to mint the Gateway Token.

### Accept tokens from more than one identity provider

The dCP gets its token from Iris. If a dCP must use another identity provider or another Iris realm, change only the
management route. The sidecar does not change. The gateway mints the LMS Gateway Token with the issuer of the route
realm, whichever issuer signed the caller token. The LMS Gateway Token does not keep the issuer of the caller token.

The [Custom Ingress IDP][custom-ingress-idp] concept sets the trust rules for issuers on API exposures. Each base path
trusts exactly one issuer. A zone operator approves each trusted issuer, and a second machine-to-machine identity
provider can need a security exception. Use the same rules for the management route.

Prefer one management route for each issuer, each with its own path prefix. The sidecar RBAC then binds each client to
the `requestPath` prefix of its route, and so to one issuer. With the chart, install one release for each issuer.

If one route must accept more than one issuer, add a provider for each issuer to the JWT GatewayExtension, and an entry
for each issuer to `route.issuers`. kgateway accepts a token that matches any provider, and writes the claims of all
providers to the same metadata key, `payload`. kgateway allows a request when any entry in `matchExpressions` is true.
So the chart renders one expression for each issuer, which checks the issuer and the client together:

```cel
metadata.filter_metadata['envoy.filters.http.jwt_authn']['payload']['iss'] == 'https://idp.example.com/realms/dcp' &&
metadata.filter_metadata['envoy.filters.http.jwt_authn']['payload']['clientId'] in ['dcp-client-id']
```

Each entry sets the name of its client claim, because it can differ between identity providers, for example `clientId`,
`azp` or `client_id`. `clients` limits the clients of each issuer to a subset of `allowedClients`. The LMS Gateway Token
does not keep the issuer, so the sidecar checks only `allowedClients`. Use client IDs that are unique across the trusted
issuers.

## Bootstrap the management route with the platform deployment

Bootstrap the management route with declarative Helm resources. An Argo CD Application manages these resources through
GitOps, independently of the dCP. For the initial composition, prefer one Application. An umbrella chart can package the
pinned upstream charts and a small subchart for the management bootstrap. As an alternative, keep the bootstrap
templates in the parent chart. Subcharts organize resources, but they do not set the order in which Argo CD applies
them. Argo CD renders the Helm templates and owns the deployment lifecycle.

Use [sync waves][argo-waves] on the rendered resources:

| Wave | Resources | Gate before the next wave |
| --- | --- | --- |
| `0` | Gateway API and kgateway CRDs. Namespaces, RBAC, Services and other prerequisites for the controller and the management proxy. | The required CRDs are `Established=True`. The supporting resources pass their health checks. |
| `1` | kgateway controller and management proxy Deployments | The Deployments are healthy. |
| `10` | The linked management resources: Gateway, GatewayParameters, JWT provider, TrafficPolicy and HTTPRoute | Observe the convergence of these resources together. The Gateway causes kgateway to provision Envoy. |

The upstream kgateway v2.3.0 chart supports [Deployment annotations][kgateway-deployment]. Set this value in the values
scope of that chart:

```yaml
deploymentAnnotations:
  argocd.argoproj.io/sync-wave: "1"
```

Annotate the bootstrap custom resources with wave `10`. The upstream CRD templates can keep the default wave `0`. Argo
CD v3.5.1 includes an [Established-aware CRD health check][argo-crd-health]. Verify that the installed version has an
equivalent check. Put policies and their target routes in the same wave. Otherwise, the health gate of an earlier policy
can wait for a route in a later wave. [Skipping the dry run for missing types][argo-dry-run] does not install CRDs or
make them ready. This proposal comes from source inspection. We have not rendered or deployed the combined chart. Open:
the management proxy chart has no values for annotations on its Deployment and its route resources yet, so it cannot set
these waves.

A separate foundation Application of the platform can already own the CRDs. In that case, keep that ownership. Make the
bootstrap wait until that Application is synced and healthy. Waves in a parent Application wait for child Applications
only with a suitable [health assessment for Applications][argo-app-health]. Put the management resources outside the
write scope of the proxy ServiceAccount. Let the management listener accept routes only from the management namespace.
If the dCP cannot reach its management endpoint, recovery stays available through the Kubernetes deployment path of the
platform.

## Configure the chart

[`values.yaml`](../../management-proxy/chart/values.yaml) documents each value with a `# --` comment in the
[helm-docs](https://github.com/norwoodj/helm-docs) format. The chart fails to render when a required value is missing.
These values need a decision for each data-plane cluster:

| Value | Content |
| --- | --- |
| `pathPrefix`, `allowedClients` | The prefix of the management route and the client IDs of the dCP. The sidecar and the route use both. |
| `envoy.gatewayToken` | Issuer, JWKS endpoint, client claim, audiences and request binding of the Gateway Token. `envoy.gatewayToken.jwks.address` connects to the in-cluster Service of the gateway, while the request keeps the host of the URI. |
| `deployment` | Replicas, pod metadata, scheduling, image pull Secrets and shutdown timing. |
| `tls.port`, `tls.certManager` | The TLS listener port and certificate issuance. Certificates come from cert-manager (default) or from existing Secrets. `values.yaml` lists the prerequisites of the issuer. |
| `tls.serverCertificate` | The server certificate of the sidecar. `tls.serverCertificate.secretName` selects the certificate Secret, and `tls.serverCertificate.caSecretName` selects the CA Secret. |
| `tls.clientCertificate` | The client certificate of kgateway. `tls.clientCertificate.enabled` turns on mutual TLS. |
| `rbac` | The namespaces and the rules of the proxy ServiceAccount. The chart creates a Role in each namespace. It refuses the namespaces of the management proxy, the management route, its Gateway and its GatewayExtensions. |
| `route` | The Gateway, the two GatewayExtensions of the gateway and the trusted issuers of the caller token, see [Accept tokens from more than one identity provider](#accept-tokens-from-more-than-one-identity-provider). |
| `networkPolicy` | Ingress only from the kgateway proxy pods. Egress only to the API server, DNS and the JWKS endpoint. |

Provisional decisions, open for the security review on the internal wiki page
[Cross-cluster Configuration][security-wiki]:

- The sidecar checks the audience only if `envoy.gatewayToken.audiences` is set. The audience of the Gateway Token for
  the management route is not defined yet.
- The MVP postpones mutual TLS, see
  [ADR 0005](../decisions/0005-reuse-an-existing-kubernetes-api-proxy.md#protect-the-management-proxy-from-in-cluster-access).
  So every pod that the NetworkPolicy allows reaches the token check of the sidecar.
  `tls.clientCertificate.enabled: true` turns on mutual TLS. The sidecar test covers it. We have not tested it with
  kgateway.

## Test the chart

Run all checks with `make -C management-proxy check` or, from the repository root, `make check-all`. The root
`make check` runs only `lint`. CI runs the same targets. [CONTRIBUTING.md](../../CONTRIBUTING.md#checks) lists the
tools. [ADR 0011](../decisions/0011-test-components-with-helm-unittest-and-testcontainers.md) explains the choice of the
test frameworks.

| Target | Checks |
| --- | --- |
| `lint` | For each value profile in `chart/ci`: `helm lint --strict` and `kube-linter` for best practices. [`validate-schemas.sh`](../../management-proxy/test/validate-schemas.sh) runs `kubeconform` against the Kubernetes schemas and the CRDs of the Gateway API, kgateway and cert-manager. It checks each environment in its `ENVIRONMENTS` list: the minimum versions of the chart, the versions in use and the next planned versions. It does not check that the CRDs install on the Kubernetes version. [helm-unittest](https://github.com/helm-unittest/helm-unittest) runs the suites in [`chart/tests`](../../management-proxy/chart/tests): they check the rendered resources of the profiles, and that the chart refuses unsafe or incomplete values. Also `shellcheck`. |
| `test` | Go tests with [testcontainers-go](https://golang.testcontainers.org/). Needs Docker. [`envoy_test.go`](../../management-proxy/test/envoy_test.go) runs `envoy --mode validate` for the Envoy configuration of each profile. [`sidecar_test.go`](../../management-proxy/test/sidecar_test.go) runs the sidecar with an echo server in place of `kubectl proxy`. It signs its own tokens and checks the token matrix, the request binding, TLS, the forwarded headers and the health port. It checks that the sidecar rejects JWKS certificates of another CA or another host name, and fails closed without keys. It runs with TLS, with mutual TLS, and without request binding. [`kubectl_proxy_test.go`](../../management-proxy/test/kubectl_proxy_test.go) checks which paths the `--accept-paths` argument of `kubectl proxy` accepts. |

The tests do not run `kubectl proxy`, and do not cover RBAC, NetworkPolicies and watches. The manual MVP test in a
cluster covered `kubectl proxy`, RBAC and the resume of watches, see [Results of the MVP](#results-of-the-mvp). Enforced
NetworkPolicies are not tested.

## Connect the dCP through the proxy

The dCP uses the same Kubernetes client as for its local resources. A sketch, adapted from the
[Kong client factory][client-factory]:

```go
cfg := &rest.Config{
    Host:            gw.Spec.Admin.Url, // management route, can contain a path prefix
    TLSClientConfig: rest.TLSClientConfig{CAData: trustedCA},
    WrapTransport:   transport.TokenSourceWrapTransport(ccConfig.TokenSource(ctx)), // Iris client credentials
}
c, err := client.New(cfg, client.Options{Scheme: scheme}) // Gateway API v1 and kgateway v1alpha1 types
```

- The [token source][oauth2-expiry] caches the token and gets a new one 10 seconds before it expires. It does not get a
  token for each request.
- Gateway API v1.5.1 supplies typed clients with apply configurations. kgateway v2.3.0 supplies Go types and a typed
  clientset, but no apply configurations, informers or listers. Use the controller-runtime client with server-side apply
  for both API groups.
- The field `gw.Spec.Admin.Url` is an example. The Gateway resource of the dCP needs a field for the management
  endpoint.

## Results of the proof of concept

The PoC ran in a virtual test cluster with the current Kong gateway in place of kgateway. Kong validated the Iris token
of the caller, and jumper minted the LMS Gateway Token. The sidecar used Envoy v1.40.2 and kubectl v1.36.0, with an
early version of the sidecar configuration and a listener without TLS. ConfigMaps stood in for the Gateway API
resources.

| Check | Result |
| --- | --- |
| Upstream images through the trusted Artifactory mirrors, without a pull secret | Works. |
| The sidecar gets the JWKS through the gateway Service in the cluster, and validates the LMS token | Works. |
| Discovery, list, server-side apply with `dryRun=All`, apply, update and delete through the gateway | Works. A dry run persists nothing. An invalid object returns `422` from Kubernetes. The field manager of the caller is kept. |
| The token is removed before `kubectl proxy` | Works. The authenticated requests succeed as the ServiceAccount. |
| Other namespaces and other resource types | `403` from Kubernetes for the ServiceAccount. |
| Another client with a valid LMS token | `403` from the sidecar RBAC. |
| An Iris token sent directly to the sidecar | `401`. The sidecar accepts only LMS tokens. |
| Calls from pods in another namespace and in the proxy namespace, without a token and with a forged unsigned token | `401` from the sidecar. `kubectl proxy` on the pod IP is unreachable. |
| ServiceAccount token in the `envoy` container | Not present. |
| Delete the only proxy pod during a watch, and change an object during the outage | The client sees the end of the stream. The gateway returns `504` during the outage. The client resumes from its last `resourceVersion` without a new list. It receives the change once and no event twice. |
| A watch without events for 90 seconds through Kong | Fails in 4 of 5 runs. After 60 seconds, Kong ends the request to the sidecar and sends it again. The client connection stays open and the client does not notice. The likely cause is the [Kong service defaults][kong-service-defaults]: a 60-second read timeout and 5 retries. |
| The same watch with bookmarks | The bookmark reaches the client before the timeout, so the later end of the stream is visible to the client. |

The Kong result shows why no hop may retry. Repeat the watch checks on the kgateway management route in the integration
tests. Not tested in the PoC: TLS and mTLS to the sidecar, kgateway, a graceful drain, 2 replicas and the rotation of
the ServiceAccount token.

## Results of the MVP

We installed the chart in the same virtual test cluster (Kubernetes v1.36), with the Gateway API v1.5.1 and kgateway
v2.3.0 CRDs, cert-manager certificates, 2 replicas and request binding. Kong and jumper again stood in for kgateway and
the ExtProc. Kong sent the requests over TLS without a client certificate, as in the default of the chart. The sidecar
fetched the JWKS over HTTPS from the public URL of the gateway. The test used real Iris tokens of two clients and real
Gateway Tokens of jumper.

| Check | Result |
| --- | --- |
| Request binding with the `operation` and `requestPath` claims of jumper | Works, also with queries, watches and server-side apply. |
| Discovery, list, server-side apply with `dryRun=All`, apply and update of HTTPRoutes, apply of TrafficPolicies, delete | Works. A dry run persists nothing. An invalid HTTPRoute returns `422`. The field manager of the caller is kept. |
| Another namespace, the release namespace | `403` from Kubernetes for the ServiceAccount. |
| Secrets and ConfigMaps in the target namespace | `403` from `kubectl proxy` (`--accept-paths`). |
| `kubectl --as=system:admin --as-group=system:masters` | The request runs as the ServiceAccount. The sidecar removes the impersonation headers. |
| Audit ID | The API server returns the request ID as `Audit-Id`. The sidecar log has the same ID and the client ID, and no token. |
| Another client with a valid Gateway Token, an Iris token sent directly to the sidecar | `403` from the sidecar RBAC, `401` from the sidecar. |
| ServiceAccount token | Only in `kubectl-proxy`. `kubectl proxy` on the pod IP is unreachable. |
| ServiceAccount token rotation, with the minimum lifetime of 600 seconds | All 32 requests in 16 minutes succeed, also after the first token expired. `kubectl proxy` reads the rotated token. |
| Delete all proxy pods during a watch, and change an object meanwhile | The client sees the end of the stream. Kong returns `504` until a new pod is ready. The client resumes from its last `resourceVersion` without a new list, and receives each object once. |
| NetworkPolicy | Not enforced in the virtual cluster. Pods in other namespaces reach the sidecar and get `401`. The health port serves the metrics, but not the Envoy admin API. |
| kgateway v2.3.0 CRDs | Install on Kubernetes v1.36. Kubernetes v1.37 rejects several of them, because their CEL rules exceed the cost budget. |

Not tested in the MVP: kgateway, mutual TLS in a cluster, enforced NetworkPolicies, idle watches through kgateway, and
the rotation of the certificates and of the JWKS keys.

## What we maintain

- We maintain no image. We track the versions and digests of two upstream images in the chart values.
- We maintain the chart: the Envoy configuration, the RBAC of the ServiceAccount, the NetworkPolicy and the route
  resources.
- The sidecar trusts only the LMS issuer of its gateway. It depends on the JWKS endpoint of that gateway.
- The correctness of watches depends on the configuration of every hop. The integration tests must check for hidden
  retries.

### Keep the images up to date

Dependabot updates the tag and the digest of three images, see [`.github/dependabot.yml`](../../.github/dependabot.yml):

- The Envoy and `kubectl` images of the chart, in `envoy.image` and `kubectlProxy.image` of `values.yaml`.
- The echo image of the sidecar test, in [`test/echo.Dockerfile`](../../management-proxy/test/echo.Dockerfile). The test
  reads the image of its `FROM` line. Nothing builds this file.

The `docker` ecosystem of Dependabot reads Dockerfiles and Helm-style `image:` maps with `repository` and `tag`. The
[Dependabot parser][dependabot-docker-parser] reads a digest only as part of the tag, so the values write it there:
`tag: distroless-v1.40.2@sha256:…`. Dependabot ignores a separate `digest` key. It would update only the tag, and the
container runtime would still pull the old image by its digest. Dependabot searches only the configured directory, not
its subdirectories.

Dependabot proposes only patch versions of `kubectl`. `kubectl` must stay within one minor version of the API servers,
as the [version skew policy][kubectl-skew] requires. Change the minor version with the cluster upgrades.

We have not seen a Dependabot pull request for these images yet. Check the first one: it must change the tag and the
digest together.

## More information

- The internal wiki page [Cross-cluster Configuration][security-wiki] lists the risk measures and the hardening of the
  management proxy.
- The internal wiki page [Custom Ingress IDP][custom-ingress-idp] describes the trust rules for identity providers other
  than Iris.

[token-file]: https://github.com/kubernetes/kubernetes/blob/d9e352346bb8aaf71fc86f956236fcd9806e8c5f/staging/src/k8s.io/client-go/transport/round_trippers.go
[bearer-keep]: https://github.com/kubernetes/client-go/blob/v0.37.0/transport/round_trippers.go#L310-L313
[envoy-sourced-metadata]: https://github.com/envoyproxy/envoy/blob/v1.40.2/api/envoy/config/rbac/v3/rbac.proto#L421-L438
[reflector-timeout]: https://github.com/kubernetes/client-go/blob/v0.37.0/tools/cache/reflector.go#L56-L58
[reflector-resume]: https://github.com/kubernetes/client-go/blob/v0.37.0/tools/cache/reflector.go#L590-L597
[reflector-relist]: https://github.com/kubernetes/client-go/blob/v0.37.0/tools/cache/reflector.go#L640-L650
[bookmark-frequency]: https://github.com/kubernetes/kubernetes/blob/v1.36.0/staging/src/k8s.io/apiserver/pkg/storage/cacher/cacher.go#L75-L77
[envoy-timeouts]: https://github.com/envoyproxy/envoy/blob/v1.35.0/docs/root/faq/configuration/timeouts.rst#L54-L110
[kgateway-timeouts]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/api/v1alpha1/shared/timeouts.go#L5-L21
[kubectl-flush]: https://github.com/kubernetes/kubernetes/blob/v1.35.0/staging/src/k8s.io/apimachinery/pkg/util/proxy/upgradeaware.go#L80-L91
[kgateway-oidc]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/docs/guides/oidc.md
[kgateway-jwt-rbac]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/pkg/kgateway/translator/gateway/testutils/inputs/jwt/rbac.yaml#L64-L96
[kgateway-jwt-translation]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/pkg/kgateway/extensions2/plugins/trafficpolicy/jwt.go#L199-L238
[argo-waves]: https://argo-cd.readthedocs.io/en/v3.5.1/user-guide/sync-waves/
[argo-crd-health]: https://github.com/argoproj/argo-cd/blob/v3.5.1/resource_customizations/apiextensions.k8s.io/CustomResourceDefinition/health.lua
[argo-dry-run]: https://argo-cd.readthedocs.io/en/v3.5.1/user-guide/sync-options/#skip-dry-run-for-new-custom-resources-types
[argo-app-health]: https://argo-cd.readthedocs.io/en/v3.5.1/operator-manual/upgrading/1.7-1.8/#health-assessment-of-argoprojioapplication-crd-has-been-removed
[kgateway-deployment]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/install/helm/kgateway/templates/deployment.yaml#L26-L36
[client-factory]: https://github.com/telekom/controlplane/blob/6c33e47adefa88ae4da0b7d5e701752f96020d65/gateway/pkg/kongutil/client_factory.go#L83-L99
[oauth2-expiry]: https://github.com/golang/oauth2/blob/v0.36.0/token.go#L19-L22
[kong-service-defaults]: https://github.com/Kong/kong/blob/3.9.1/kong/db/schema/entities/services.lua#L33-L42
[security-wiki]: https://wiki.telekom.de/spaces/DHEI/pages/4600044528/Cross-cluster+Configuration
[custom-ingress-idp]: https://wiki.telekom.de/pages/viewpage.action?pageId=4047176972
[dependabot-docker-parser]: https://github.com/dependabot/dependabot-core/blob/bbaa42a223acbd059d11a3475ae0c5aded0ec1a3/docker/lib/dependabot/docker/file_parser.rb#L141-L167
[kubectl-skew]: https://kubernetes.io/releases/version-skew-policy/#kubectl
[envoy-init]: https://www.envoyproxy.io/docs/envoy/v1.40.2/intro/arch_overview/operations/init

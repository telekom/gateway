---
status: proposed
decision-makers: []
tags: [configuration, build-vs-buy]
---

# Reuse an Existing Kubernetes API Proxy before Building One

## Context and Problem Statement

[ADR 0004](0004-use-an-oauth-proxy-for-cross-cluster-configuration.md) proposes a transparent Kubernetes API proxy with
OAuth authentication for machine clients. The dCP owns route semantics and reconciliation. Kubernetes owns resource
validation and persistence. We need a component for authentication and forwarding, not another route-management service.

Should we configure existing open-source software or build a dedicated service? In this record, "buy" means that we
reuse and operate an existing component. We will not use a commercial product.

## Decision Drivers

- Reuse Kubernetes API semantics and existing clients, including list/watch and server-side apply.
- Accept OAuth2 client-credentials access tokens from the required issuer. Authorize the calling client.
- Keep a Kubernetes credential with a narrow scope and regular rotation inside the target cluster.
- Keep custom code, dependencies and long-term maintenance to a minimum.

## Considered Options

- Configure an existing reverse proxy that can authenticate requests. If necessary, add a local Kubernetes credential
  adapter.
- Adopt a dedicated Kubernetes identity proxy or a broader cluster-management platform.
- Build a small Go forwarding service.
- Build a route-specific operation API, or use a workflow or GitOps controller.
- Add mTLS client certificates to the OAuth access token on the path from the dCP. The internal wiki page
  [Cross-cluster Configuration][security-wiki] assesses this option.

## Decision Outcome

Proposed option: **reuse an existing proxy or a small composition of proxies**. Select a product after an integration
spike. The spike must show the required behavior for the issuer, the Kubernetes client and credential rotation. This
record does not select a product yet.

A custom route API adds validation and lifecycle responsibilities. ADR 0004 gives these responsibilities to other
layers. Workflow and GitOps controllers add execution or desired-state machinery, but they do not solve a requirement of
this proxy. A broader management platform is a good choice only if we already operate it and it supplies the required
boundary at low cost.

### Candidates for the spike

| Candidate | Fit according to the sources, and remaining work |
| --- | --- |
| Existing kgateway with `kubectl proxy` | Reuses the existing Envoy front end. kgateway v2.3.0 exposes [JWT providers][kgateway-extensions] and [JWT and CEL-RBAC policies][kgateway-policies]. Bootstrap the management route independently of the dCP. Test the actual issuer, authorization, forwarding and the shared failure domain. |
| Envoy sidecar with local `kubectl proxy` (_preferred_) | Adds an authentication check in the adapter pod, behind the kgateway route. Envoy provides [JWT verification][envoy-jwt] and [HTTP RBAC][envoy-rbac] for the verified client claim. `kubectl proxy` supplies [Kubernetes forwarding][kubectl-proxy], and client-go supplies [token-file refresh][token-file]. Test the composition and watch streaming. |
| Ory Oathkeeper with local `kubectl proxy` | Alternative with [JWT][oathkeeper-jwt] or [OAuth2 introspection][oathkeeper-introspection]. Add an explicit rule for authorized clients, because authentication alone is not sufficient. Test the removal of credential headers and the watch behavior. |
| Envoy alone | [Credential injection][envoy-injector] can replace the upstream bearer header. The extension is [alpha][envoy-extensions]. The [SDS file-rotation documentation][envoy-sds] does not show that a filename-backed GenericSecret refreshes a projected token automatically. Before selection, demonstrate a refresh mechanism. |
| kube-oidc-proxy | The [Jetstack original is archived][kube-oidc-proxy]. A fork from Tremolo Security exists. Its OIDC and impersonation model would change the identity model of ADR 0004. It also needs scoped impersonation permissions. It is an alternative to the current model, not a matching candidate. |

In all candidates, kgateway routes the management traffic to the adapter. An authentication component in the adapter
pod, for example Envoy or Oathkeeper, only checks the request and forwards it to its one local upstream. It does not
route.

For the Envoy sidecar, use JWT authentication and then client authorization. Then forward to `kubectl proxy` on its
loopback bind address. Rewrite the forwarded Host to `localhost`, because the local proxy has a default host filter.
Remove the external `Authorization` header before that hop. The client-go token transport keeps an existing header and
does not replace it with the configured ServiceAccount token. The adapter reads the projected token file and uses the
Kubernetes CA. Opaque access tokens need introspection or another verification path that the issuer supports.

The pinned sources show the building blocks. A candidate stays unverified until it passes the checks in
[Evidence required for product selection](#evidence-required-for-product-selection).

### Bootstrap the management route with the platform deployment

Each component of the composition ships in the gateway chart. A candidate that needs a cluster operator, a DaemonSet
with host access or other cluster-wide tooling does not qualify. The gateway chart itself already needs cluster-wide
resources, for example the CRDs, the GatewayClass and the ClusterRoles of kgateway. Agree on this footprint with each
cluster operator. Prerequisites that the gateway runtime already has, for example the certificate issuer behind the key
rotator, stay allowed.

Bootstrap the management route with declarative Helm resources. An Argo CD Application manages these resources through
GitOps, independently of the dCP. For the initial composition, prefer one Application. An umbrella chart can package the
pinned upstream charts and a small subchart for the management bootstrap. As an alternative, keep the bootstrap
templates in the parent chart. Subcharts organize resources, but they do not set the order in which Argo CD applies
them. Argo CD renders the Helm templates and owns the deployment lifecycle.

Use [sync waves][argo-waves] on the rendered resources:

| Wave | Resources | Gate before the next wave |
| --- | --- | --- |
| `0` | Gateway API and kgateway CRDs. Namespaces, RBAC, Services and other prerequisites for the controller and the adapter. | The required CRDs are `Established=True`. The supporting resources pass their health checks. |
| `1` | kgateway controller and credential-adapter Deployments | The Deployments are healthy. |
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
make them ready. This proposal comes from source inspection. We have not rendered or deployed the combined chart.

A separate foundation Application of the platform can already own the CRDs. In that case, keep that ownership. Make the
bootstrap wait until that Application is synced and healthy. Waves in a parent Application wait for child Applications
only with a suitable [health assessment for Applications][argo-app-health]. Put the management resources outside the
write scope of the proxy ServiceAccount. Let the management listener accept routes only from the management namespace.
If the dCP cannot reach its management endpoint, recovery stays available through the Kubernetes deployment path of the
platform.

Require valid JWTs in the `Authorization` header. Configure an explicit issuer, an audience and a rule for authorized
clients. The [OAuth2/OIDC extension][kgateway-oidc] for browser login is a different flow. A tagged
[translator fixture][kgateway-jwt-rbac] shows CEL authorization based on JWT claims. The
[JWT translator][kgateway-jwt-translation] translates `forwardToken: false` into the removal of the validated token
header. Forward native Kubernetes requests without changes. Configure timeouts that are compatible with watches. Opaque
tokens need an external-auth path. We have not yet tested the compatibility with the actual issuer or the end-to-end
forwarding.

Prefer a separate credential-adapter Deployment with its own ServiceAccount. kgateway routes the management traffic to
the Service of this adapter. An authentication sidecar in the adapter pod checks each request, and `kubectl proxy` binds
only to loopback. See [Protect the adapter from in-cluster access](#protect-the-adapter-from-in-cluster-access). Do not
put `kubectl proxy` into the gateway pod. It would share the identity of the gateway pod and would need supported
deployment customization. With the shared Envoy option, we keep our trust in the routing that the dCP generates. This
option also shares rollout and configuration failures with the gateway. If management access fails, the platform needs
an independent recovery path.

### Protect the adapter from in-cluster access

`kubectl proxy` has no authentication. The [Kubernetes documentation][kubectl-proxy-warning] warns about this. Policies
on the management HTTPRoute run only in the kgateway proxy. A pod that calls the adapter Service directly skips them. So
the adapter pod authenticates each request itself.

Proposed: **the Envoy sidecar checks the LMS Gateway Token** that kgateway mints for the management route. kgateway
authenticates to the sidecar with a client certificate from a [BackendConfigPolicy][kgateway-backend-tls]. cert-manager
issues and renews the TLS certificate of the sidecar and the client certificate of kgateway. The gateway runtime already
uses cert-manager for the source certificates of the key rotator. Verify that each target cluster provides it.

The internal wiki page [Cross-cluster Configuration][security-wiki] compares the token options. It also lists the risk
measures and the hardening of the adapter.

### Connect the dCP through the proxy

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

A watch is one long HTTP response. The proxies check the token only when the request starts. The
[client-go reflector][reflector-timeout] ends each watch after a random time of 5 to 10 minutes and opens a new watch
with the current token. The components support streaming responses, but no source confirms the complete path. Configure
each hop and test the path in the spike:

| Hop | Setting |
| --- | --- |
| kgateway management route | The [Envoy default route timeout][envoy-timeouts] is 15 seconds and stops watches. Set [`timeouts.request`][kgateway-timeouts] to `0s` in the TrafficPolicy of the route. Set `timeouts.streamIdle` to `0s` or longer than the quiet time of a watch. Envoy closes idle streams after 5 minutes by default. kgateway v2.3.0 has no field for a maximum stream duration. |
| Envoy sidecar | Set the route timeout to `0s` and the stream idle timeout as above. Do not add a buffering filter. Set `max_stream_duration` as an upper limit for clients that do not end their watches. |
| `kubectl proxy` | It [flushes streamed responses][kubectl-flush] every 200 milliseconds and sets no server timeouts. No setting is necessary. |
| kube-apiserver | It uses the `timeoutSeconds` value of the watch request. |

On kgateway, the TrafficPolicy of the management route sets the route timeouts. `timeouts.streamIdle` overrides the idle
timeout of the listener for this route only.

```yaml
apiVersion: gateway.kgateway.dev/v1alpha1
kind: TrafficPolicy
metadata:
  name: kubernetes-management
  namespace: gateway-management
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: kubernetes-management
  timeouts:
    request: 0s     # The Envoy default of 15 seconds ends every watch.
    streamIdle: 15m # Longer than the 10-minute maximum watch of client-go.
  # jwtAuth, rbac and extProc are not shown here.
```

The Envoy sidecar uses a static configuration with one route to `kubectl proxy`. The `jwt_authn` filter removes the
validated token from the request, because `forward` is `false` by default. We validated this excerpt with
`envoy --mode validate` (Envoy v1.35):

```yaml
static_resources:
  listeners:
    - name: adapter
      address:
        socket_address: { address: 0.0.0.0, port_value: 8443 }
      filter_chains:
        - filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                stat_prefix: adapter
                stream_idle_timeout: 900s # Longer than the 10-minute maximum watch of client-go.
                route_config:
                  virtual_hosts:
                    - name: kubernetes-api
                      domains: ["*"]
                      routes:
                        - match: { prefix: "/" }
                          route:
                            cluster: kubectl-proxy
                            timeout: 0s # The default of 15 seconds ends every watch.
                            host_rewrite_literal: localhost
                            max_stream_duration:
                              max_stream_duration: 900s # Upper limit for a watch after its token expires.
                http_filters:
                  # The jwt_authn and rbac filters come first. They are not shown here.
                  - name: envoy.filters.http.router
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
  clusters:
    - name: kubectl-proxy
      type: STATIC
      load_assignment:
        cluster_name: kubectl-proxy
        endpoints:
          - lb_endpoints:
              - endpoint:
                  address:
                    socket_address: { address: 127.0.0.1, port_value: 8001 }
```

The TLS settings of the sidecar listener are not shown. For a Unix socket, replace the `socket_address` of the cluster
with a `pipe` address.

### Identity-model change for kube-oidc-proxy

In the current proposal, Kubernetes authenticates and authorizes every forwarded request as the local ServiceAccount.
The OAuth front end decides which external client can use that execution identity.

In [Tremolo kube-oidc-proxy v1.0.12][oidc-impersonation], the proxy validates the JWT. It maps the username claim and
the optional group claims to Kubernetes subjects. It authenticates upstream with its local credentials and sends
impersonation headers. Kubernetes first authorizes the proxy to impersonate those subjects. Then Kubernetes authorizes
the requested operation as the mapped user or group. For this normal proxy flow, the API server does not need to trust
the external issuer.

This changes the [RBAC ownership and impersonation permissions][kubernetes-impersonation]:

- Give the proxy ServiceAccount cluster-scoped `impersonate` permissions. Limit them with an allowlist of the mapped
  users and groups. Kubernetes has no namespace-scoped permission for ordinary user or group impersonation.
- Bind namespace-scoped resource permissions to the mapped user or group, not to the proxy ServiceAccount. You do not
  create a Kubernetes User object. A RoleBinding refers to the mapped subject name.
- Kubernetes audit records can keep both the authenticated proxy identity and the impersonated caller identity.

A machine-client JWT can work if it has a suitable stable claim and an accepted issuer and audience. Browser login is
not necessary. Verify this with an actual token. [Token passthrough][oidc-passthrough] is different. It forwards the
original token, and the API server must accept that token. If you disable impersonation, you do not get the model with a
fixed ServiceAccount. If we select kube-oidc-proxy, explicit impersonation allowlists and RBAC for the effective user
replace the no-impersonation rule below. The broad default permissions of its chart are not the proposed permission
scope.

### Evidence required for product selection

1. Authenticate an actual access token of a machine client. Reject invalid tokens. Reject clients that are authenticated
   but not authorized.
2. Keep the local credentials private. Refresh them when the projected ServiceAccount token rotates. Verify upstream
   TLS. Remove caller credentials and impersonation headers before the proxy forwards the request with the local
   identity. Do not give impersonation permissions to the ServiceAccount.
3. Use a Kubernetes client for permitted discovery, list/watch, apply with `dryRun=All`, real apply and delete. Verify
   the handling of the forwarded host. Keep Kubernetes status and error bodies, query options and patch content types
   unchanged. Make sure that RBAC denies resources, namespaces and verbs that are not allowed.
4. Reconnect watches after a connection loss and after expired resource versions. Verify that new requests use the
   refreshed token. Verify that the dCP repairs missed drift. The proxy validates the JWT when a request starts. An
   existing watch does not have to close when the token expires. Select and test a maximum stream duration and timeouts
   that are compatible with streaming. Make sure that the proxy does not buffer streams and does not retry mutations.
5. Expose metrics for requests, errors and latency. Verify that the logs are useful and do not contain tokens,
   authorization headers or secrets from payloads.
6. For the option on kgateway, bootstrap while the dCP is unavailable. Recover a broken management route through the
   platform deployment path. Verify that a partial bootstrap never exposes an unauthenticated Kubernetes proxy. Verify
   that the adapter has no direct exposure. Verify that the bootstrap resources are outside the write scope of the
   caller.
7. From a pod in another namespace and from a pod in the adapter namespace, call the adapter Service and the pod IP
   without a token and with a valid token of a client that is not authorized. Make sure that no request reaches
   `kubectl proxy`.

Gateway readiness and safe intermediate traffic configuration stay integration topics for the dCP and kgateway under ADR
0004.

### When building is justified

Build only if the spike finds a specific gap. Building must cost less than configuring or extending existing software.
Include the maintenance in this cost. If we need a fallback, make it a Go forwarding service with standard Kubernetes
client machinery. Document an OpenAPI contract for all endpoints that the service adds. Do not add route bundles,
translation or reconciliation only because a custom process exists.

### Consequences

We still own the deployment configuration, RBAC, the issuer integration and the dCP gateway adapter. Reuse of a proxy
removes custom API behavior, but not this integration work. We can change the product without a change to the split of
responsibilities in ADR 0004.

## More Information

- [Kubernetes dry run and watch semantics](https://kubernetes.io/docs/reference/using-api/api-concepts/)
- [Tremolo Security kube-oidc-proxy fork](https://github.com/TremoloSecurity/kube-oidc-proxy)

[envoy-jwt]: https://github.com/envoyproxy/envoy/blob/6bf4e1821bb9f27e30617ea83709a148ccc025e6/docs/root/configuration/http/http_filters/jwt_authn_filter.rst
[envoy-rbac]: https://github.com/envoyproxy/envoy/blob/6bf4e1821bb9f27e30617ea83709a148ccc025e6/docs/root/configuration/http/http_filters/rbac_filter.rst
[kubectl-proxy]: https://github.com/kubernetes/kubectl/blob/62644851425759e5f7b4916b5bff65e829cfbc25/pkg/proxy/proxy_server.go
[kubectl-proxy-warning]: https://kubernetes.io/docs/reference/kubectl/conventions/#kubectl-proxy
[security-wiki]: https://wiki.telekom.de/spaces/DHEI/pages/4600044528/Cross-cluster+Configuration
[kgateway-backend-tls]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/api/v1alpha1/kgateway/backend_config_policy_types.go#L288-L339
[client-factory]: https://github.com/telekom/controlplane/blob/6c33e47adefa88ae4da0b7d5e701752f96020d65/gateway/pkg/kongutil/client_factory.go#L83-L99
[oauth2-expiry]: https://github.com/golang/oauth2/blob/v0.36.0/token.go#L19-L22
[reflector-timeout]: https://github.com/kubernetes/client-go/blob/v0.37.0/tools/cache/reflector.go#L56-L58
[envoy-timeouts]: https://github.com/envoyproxy/envoy/blob/v1.35.0/docs/root/faq/configuration/timeouts.rst#L54-L110
[kgateway-timeouts]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/api/v1alpha1/shared/timeouts.go#L5-L21
[kubectl-flush]: https://github.com/kubernetes/kubernetes/blob/v1.35.0/staging/src/k8s.io/apimachinery/pkg/util/proxy/upgradeaware.go#L80-L91
[token-file]: https://github.com/kubernetes/kubernetes/blob/d9e352346bb8aaf71fc86f956236fcd9806e8c5f/staging/src/k8s.io/client-go/transport/round_trippers.go
[oathkeeper-jwt]: https://github.com/ory/oathkeeper/blob/ab46001dc7d772710066c544aeb4e1a4f78e5f6f/pipeline/authn/authenticator_jwt.go
[oathkeeper-introspection]: https://github.com/ory/oathkeeper/blob/ab46001dc7d772710066c544aeb4e1a4f78e5f6f/pipeline/authn/authenticator_oauth2_introspection.go
[envoy-injector]: https://github.com/envoyproxy/envoy/blob/6bf4e1821bb9f27e30617ea83709a148ccc025e6/api/envoy/extensions/filters/http/credential_injector/v3/credential_injector.proto
[envoy-extensions]: https://github.com/envoyproxy/envoy/blob/6bf4e1821bb9f27e30617ea83709a148ccc025e6/source/extensions/extensions_metadata.yaml
[envoy-sds]: https://github.com/envoyproxy/envoy/blob/6bf4e1821bb9f27e30617ea83709a148ccc025e6/docs/root/configuration/security/secret.rst
[kube-oidc-proxy]: https://github.com/jetstack/kube-oidc-proxy/blob/6ed44b437a43ec17b4ce9eb56aebd264029bab44/README.md
[kgateway-extensions]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/install/helm/kgateway-crds/templates/gateway.kgateway.dev_gatewayextensions.yaml
[kgateway-policies]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/install/helm/kgateway-crds/templates/gateway.kgateway.dev_trafficpolicies.yaml
[kgateway-oidc]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/docs/guides/oidc.md
[kgateway-jwt-rbac]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/pkg/kgateway/translator/gateway/testutils/inputs/jwt/rbac.yaml#L64-L96
[kgateway-jwt-translation]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/pkg/kgateway/extensions2/plugins/trafficpolicy/jwt.go#L199-L238
[oidc-impersonation]: https://github.com/TremoloSecurity/kube-oidc-proxy/blob/63bfef0eeeb0d71ba55c597972a3c93d3f606c8a/pkg/proxy/proxy.go#L218-L239
[oidc-passthrough]: https://github.com/TremoloSecurity/kube-oidc-proxy/blob/63bfef0eeeb0d71ba55c597972a3c93d3f606c8a/docs/tasks/token-passthrough.md
[kubernetes-impersonation]: https://github.com/kubernetes/website/blob/3793b2f6cd0256224839b064ede8dcee9fd5ee20/content/en/docs/reference/access-authn-authz/user-impersonation.md
[argo-waves]: https://argo-cd.readthedocs.io/en/v3.5.1/user-guide/sync-waves/
[argo-crd-health]: https://github.com/argoproj/argo-cd/blob/v3.5.1/resource_customizations/apiextensions.k8s.io/CustomResourceDefinition/health.lua
[argo-dry-run]: https://argo-cd.readthedocs.io/en/v3.5.1/user-guide/sync-options/#skip-dry-run-for-new-custom-resources-types
[argo-app-health]: https://argo-cd.readthedocs.io/en/v3.5.1/operator-manual/upgrading/1.7-1.8/#health-assessment-of-argoprojioapplication-crd-has-been-removed
[kgateway-deployment]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/install/helm/kgateway/templates/deployment.yaml#L26-L36

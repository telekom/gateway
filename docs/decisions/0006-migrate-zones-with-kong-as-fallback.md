---
status: proposed
decision-makers:
  - niklas.beilmann@telekom.de
  - christian.ingenhaag@telekom.de
  - mika.juznik@telekom.de
  - luis.pflamminger@telekom.de
  - michael.fedorenko@telekom.de
consulted:
  - ron.gummich@telekom.de
tags: [migration, chart, control-plane]
---

# Migrate Zones to kgateway with Kong as Fallback

## Context and Problem Statement

The new gateway on kgateway replaces the Kong-based gateway. Each zone has a gateway on its own data plane. A zone
operator operates it. Today, an ingress controller receives the traffic of a zone and sends it to Kong. Kong sends it to
the providers. The [control plane](https://github.com/telekom/controlplane) configures Kong through the Kong admin API.

At first, kgateway will not support all features of the current gateway. A route can only move to kgateway when kgateway
supports all features that the route uses.

How do we move the traffic of a zone from Kong to kgateway?

## Decision Drivers

- **No change for consumers and providers.** Hostnames, paths and tokens stay the same.
- **Move routes one by one.** A route moves when kgateway supports all its features.
- **Fast rollback.** We can roll back one route or a whole zone without a DNS change.
- **Test before go-live.** We can test a kgateway route on the real hostname before consumer traffic uses it.
- **Zone operators can do it.** The procedure works on each data plane, with steps that a zone operator can follow.
- **Short transition.** Keep the time with two gateways and two configurations short.

A big-bang migration per zone and a new hostname for kgateway do not meet the first two drivers. We rejected them early.

## Considered Options

- kgateway in front, Kong as fallback for `/`
- Kong in front, kgateway behind
- Weighted traffic split between the two gateways at the load balancer or in DNS

## Decision Outcome

Chosen option: "kgateway in front, Kong as fallback for `/`", because it moves routes one by one and makes kgateway the
entry point before any route moves.

After the move of the entry point, kgateway receives all traffic of the zone. A catch-all `HTTPRoute` for
`PathPrefix /`, the fallback route, sends all requests to Kong. The control plane configures both gateways. For each
migrated route, it creates an `HTTPRoute` with a more specific path in kgateway. In Gateway API, the more specific path
has precedence. So kgateway sends these requests directly to the provider. All other requests go through Kong as before.

### Consequences

- Good, because consumers and providers see no change.
- Good, because the entry point moves before any route moves. At that time, kgateway only forwards to Kong. The risk is
  limited to the extra hop.
- Good, because a route goes live or rolls back with a change in the control plane only.
- Bad, because requests on routes that did not move pass two proxies. This adds latency and one more point of failure.
- Bad, because the control plane configures two gateways at the same time. It must show the status of each gateway.
- Bad, because the client IP setup of Kong expects the ingress controller in front. Zones must change it, see
  [Client IP in Kong](#client-ip-in-kong).
- Bad, because the ingress controller must stay for the admin API of Kong until we remove Kong.

## Implementation Sketch

### Rollout per zone

The steps use the test header, see [Per-route switch](#per-route-switch).

1. Deploy kgateway on the data plane of the zone. Check connectivity and observability. The ingress controller still
   sends all traffic to Kong.
2. Give the control plane access to the data plane. The control plane runs in another cluster, see
   [pull request #3](https://github.com/telekom/gateway/pull/3) ("Configure Data-Plane Resources through an
   OAuth-Protected Kubernetes API Proxy" and "Reuse an Existing Kubernetes API Proxy before Building One").
3. Turn on the fallback route in the chart. It points to the cluster-internal Service of the Kong proxy.
4. Add kgateway to the zone configuration in the control plane. The control plane creates kgateway routes in test mode.
   Kong stays fully configured.
5. Test the fallback route. Call the kgateway Service directly with the `Host` header of the zone and a valid consumer
   token.
6. Move the ingress of the Kong proxy to kgateway, see [Open Questions](#open-questions). Before that, change the client
   IP setup of Kong, see [Client IP in Kong](#client-ip-in-kong).
7. Smoke-test one route on the real hostname with the test header.
8. Make the routes go live one by one. To roll back a route, put it back into test mode.
9. When all routes are live, check that the fallback route gets no traffic. Then turn it off.
10. Remove Kong from the zone configuration. The control plane stops configuring Kong. Remove the Kong deployment.

### Fallback route

The chart creates the fallback route from these values:

```yaml
kongFallback:
  enabled: false
  service:
    name: my-kong-service
    namespace: kong # empty: namespace of the release
    port: 8000
  hostnames: # the hostnames of the zone, the same as on the routes of the control plane
    - api.example.com
  timeouts:
    request: 0s # 0s turns the timeout off. Kong applies its own timeouts.
```

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: kong-fallback
spec:
  parentRefs:
    - name: my-gateway
  hostnames:
    - api.example.com
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: my-kong-service
          namespace: kong
          port: 8000
      timeouts:
        request: 0s
```

- **Hostnames.** The fallback route uses the same hostnames as the routes of the control plane. Gateway API compares
  hostnames before paths. kgateway creates one Envoy virtual host per hostname, and Envoy selects one virtual host per
  request. A fallback route without hostnames is only in the virtual host `*`. Requests to `api.example.com` on a path
  without a kgateway route then get a 404.
- **Timeout.** Without a timeout, Envoy uses 15 seconds. Kong uses 60 seconds by default. The fallback route turns the
  timeout off, so that Kong applies its own timeouts.
- **Namespace.** If Kong runs in another namespace, the reference needs a `ReferenceGrant` in the namespace of Kong.
- **Policies.** The fallback route has no policies. Policies that target the whole `Gateway` also apply to it. They must
  not change requests that go to Kong.
- **Nested paths.** The control plane blocks only API base paths that are exactly the same. If `/a/v1` is live on
  kgateway, it also catches requests to a nested base path like `/a/v1/b/v1`, which belong to Kong. Such nested base
  paths do not occur in practice, so they are not a risk.

### Per-route switch

The control plane needs a switch per route that moves the route from Kong to kgateway. It creates kgateway resources
only for routes whose features kgateway supports. We have three ideas:

- **Test header (preferred).** The control plane always creates the kgateway resources of supported routes. While the
  switch is off, the route is in test mode: its `HTTPRoute` rule has an extra header match, for example
  `x-kgateway-test: "true"`. Requests without the header do not match and go to Kong. When the switch is on, the control
  plane removes the header match.
  - Good, because we can test a route on the real hostname before go-live.
  - Good, because kgateway accepts all resources of the route before go-live. Go-live changes one match only.
  - Bad, because each client that sends the header reaches the kgateway route before go-live. Only gateway operators and
    developers know the header, and kgateway already checks authentication and access. Policies that kgateway does not
    support yet, for example rate limiting, do not apply. We accept this risk.
- **Selected test routes.** A CRD or an annotation in the control plane selects specific routes for tests. The control
  plane creates kgateway resources in test mode only for these routes. Other routes have no kgateway resources until
  their switch is on.
  - Good, because we can test the selected routes before go-live.
  - Good, because fewer routes are reachable with the test header.
  - Bad, because zone operators have no access to the control plane. We must select the test routes for them centrally.
  - Bad, because kgateway does not accept the resources of the other routes before their go-live.
- **Create on switch.** The control plane creates the kgateway resources only when the switch is on.
  - Good, because it is simple and there are no hidden routes.
  - Bad, because we cannot test a route before go-live.

For a rollback of the whole zone, the control plane puts all routes into test mode (test header) or removes them
(selected test routes, create on switch).

### Zone configuration

This sketch is only an idea. We will change the control plane to support the new gateway and decide the API there.

```yaml
spec:
  presets:
    - name: default
      type: API
      default: true
      gatewayRef: standard
      identityProviderRef: primary
      # The gateway implementations that the control plane configures for this preset.
      # Migration: [Kong] -> [Kong, Kgateway] -> [Kgateway]
      implementations: [Kong, Kgateway]
      urls:
        - hostname: api.example.com
          basePath: /
      features:
        # Zone-wide flag. Disabled: all kgateway routes are in test mode.
        # We could also name it KgatewayLive.
        - name: KgatewayNative
          enabled: true
```

Another idea: a mode per implementation instead of a feature. Features describe what a preset can do, and the control
plane selects presets by them. A mode describes the migration state:

```yaml
implementations:
  - name: Kong
  - name: Kgateway
    mode: Test # Test: all kgateway routes are in test mode. Live: the per-route switch decides.
```

## Pros and Cons of the Options

### kgateway in front, Kong as fallback for `/`

- Good, because routes move one by one.
- Good, because kgateway becomes the entry point early, with no change in behavior.
- Good, because migrated routes pass one proxy only.
- Bad, because routes that did not move pass two proxies.

### Kong in front, kgateway behind

The control plane changes the upstream of a migrated Kong route to kgateway.

- Good, because routes move one by one.
- Good, because the entry point does not change during the migration.
- Bad, because migrated routes also pass two proxies, until Kong is removed.
- Bad, because the entry point moves to kgateway at the end, for all routes at once.
- Bad, because the Kong route of a migrated route must forward without its own policies. This adds a third kind of Kong
  configuration.

### Weighted traffic split between the two gateways at the load balancer or in DNS

- Good, because a percentage of the traffic tests kgateway first.
- Bad, because kgateway must support all routes of the zone before it gets any traffic.
- Bad, because there is no control per route.
- Bad, because DNS caches make a rollback slow.

## Open Questions

- How does kgateway take over the ingress in step 6: a DNS change, a takeover of the existing load balancer, or a
  weighted split between the ingress controller and kgateway? Together with the fallback route, a weighted split makes
  step 6 gradual.
- Test header, selected test routes or create on switch? Where is the switch (field, annotation, label or CRD), and what
  is the name of the header?
- What does the control plane do when one gateway accepts a change and the other rejects it?

### Client IP in Kong

Today, Kong is already behind a proxy, the ingress controller. If the chart value `trustedIps` is set, Kong reads the
client IP from the header `x-original-forwarded-for` (chart value `realIpHeader`). The ingress controller sets this
header. Envoy does not set it, it sets `X-Forwarded-For`.

Behind kgateway, zones that set `trustedIps` must:

- set `realIpHeader` to `X-Forwarded-For`
- include the addresses of the kgateway pods in `trustedIps`

Otherwise, logs and IP-based features of Kong see kgateway as the client. Zones without `trustedIps` already see the
ingress controller as the client, so nothing changes for them.

Open: how does kgateway fill `X-Forwarded-For`, for example does it append to an existing value? This decides whether
`realIpRecursive` must change too. We test this together with the fallback route in step 5.

## More Information

- [kgateway Transition Diagram](https://wiki.telekom.de/pages/viewpage.action?pageId=4545609925) (internal): the
  transition model and the rollout per zone that this record is based on
- Gateway API: [hostname precedence](https://gateway-api.sigs.k8s.io/reference/api-spec/1.6/spec/#httproutespec),
  [match precedence](https://gateway-api.sigs.k8s.io/reference/api-spec/1.6/spec/#httprouterule),
  [timeouts](https://gateway-api.sigs.k8s.io/reference/api-types/httproute/#timeouts-optional),
  [ReferenceGrant](https://gateway-api.sigs.k8s.io/reference/api-types/referencegrant/)
- Envoy:
  [route timeout](https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/route/v3/route_components.proto#envoy-v3-api-field-config-route-v3-routeaction-timeout),
  [virtual host selection](https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/route/v3/route_components.proto#envoy-v3-api-field-config-route-v3-virtualhost-domains)
- kgateway (`main` at `430fc96`):
  [virtual hosts per hostname](https://github.com/kgateway-dev/kgateway/blob/430fc96/pkg/kgateway/translator/listener/gateway_listener_translator.go#L1175-L1229),
  [`HTTPRoute` timeouts are supported](https://github.com/kgateway-dev/kgateway/blob/430fc96/pkg/kgateway/translator/gateway/testutils/inputs/httproute-timeout-retry/builtin.yaml)
- Kong: [default Service timeouts](https://developer.konghq.com/gateway/entities/service/),
  [`trusted_ips` and `real_ip_header`](https://developer.konghq.com/gateway/configuration/),
  [client IP values of the Kong chart](https://github.com/telekom/gateway-kong-charts/blob/43716eb/templates/_kong.tpl#L391-L398)
- Control plane: exact base path check in `api/internal/handler/api/handler.go`, zone and preset API in
  [`admin/api/v1/zone_types.go`](https://github.com/telekom/controlplane/blob/main/admin/api/v1/zone_types.go)

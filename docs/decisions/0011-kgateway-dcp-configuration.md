---
status: proposed
decision-makers:
  - niklas.beilmann@telekom.de
  - christian.ingenhaag@telekom.de
  - mika.juznik@telekom.de
  - luis.pflamminger@telekom.de
  - michael.fedorenko@telekom.de
tags: [control-plane]
---

# Model kgateway in the Zone and Gateway Resources

## Context and Problem Statement

[ADR 0008](https://github.com/telekom/gateway/blob/main/docs/decisions/0008-dcp-architecture.md) decides that domain
operators create one shared Route and that a kgateway controller in the gateway operator decides whether and how
kgateway serves it. The kgateway controller reads only resources of the gateway domain. So the `Gateway` resource
(`gateway.cp.ei.telekom.de`) must carry everything that the decision needs, and the `Zone` resource
(`admin.cp.ei.telekom.de`), from which the admin operator creates the `Gateway`, must provide it.

### What is a preset?

A **preset** is a named routing profile of a zone. It has a traffic type (`API`, `AI` or `Event`), an optional default
flag, a gateway entry, an identity provider, up to five URLs (hostname and base path) and zone feature flags (for
example `BasicAuth`, `RateLimiting`, `ConsumerFailover`). Domain operators select one preset per traffic type and
requested features. They take the Route's `gatewayRef`, hostnames and paths from it, and publish its links (gateway URL,
issuers, token URL) in the zone status.

A zone separates two things:

| Concept | What it describes | Where it is defined |
| --- | --- | --- |
| **Gateway entry** (`spec.gateways[]`) | A physical gateway that the control plane configures: its admin URL, credentials, implementation and Redis. The admin operator creates **one `Gateway` CR per entry**. | `zone_types.go` |
| **Preset** (`spec.presets[]`) | A **public entry point** into the zone: the hostnames and base paths that consumers call, which gateway serves them, which token issuer or token URL applies, and which kind of traffic uses it. | `zone_types.go` |

A preset answers this question for the domain operators: "I have traffic of type X, maybe with feature Y. Which gateway
do I configure, under which hostnames, and which URLs and issuers do I publish?"

[ADR 0006](https://github.com/telekom/gateway/blob/main/docs/decisions/0006-migrate-zones-with-kong-as-fallback.md) is
the relevant constraint. kgateway replaces the ingress controller of the zone, receives all API traffic on the existing
hostnames and sends unmatched requests to Kong through a catch-all fallback route. Consumers see no change. So a
kgateway Route must match the same hostnames and paths as the Kong Route.

## Decision Drivers

- The gateway operator decides from gateway-domain resources only, without reading the Zone.
- Domain operators and preset selection stay unaware of kgateway while Kong is the fallback.
- kgateway serves the same hostnames and paths as the Kong gateway behind it.
- The model also covers the end state of ADR 0006, in which Kong is removed.
- Data-plane settings can differ per zone.
- Configuration that only the migration needs is easy to remove after the migration is complete, without changing the
  Zone API or migrating existing Zones.

## Considered Options

- **A:** Keep the kgateway preset.
- **B:** Put the routing settings on the kgateway gateway entry, without a kgateway preset.
- **C:** Put the routing settings into a separate kgateway CRD, without a kgateway preset.

## Decision Outcome

Proposed option: **C: put the routing settings into a separate kgateway CRD, without a kgateway preset**. The settings
exist only for the migration, so they get a resource that the migration can delete. The Zone and the `Gateway` keep only
fields that are still valid after the migration.

The Zone does not state a connection between kgateway and Kong. The kgateway controller derives it: the kgateway
`Gateway` of a Route is the `Gateway` with `implementation: Kgateway` in the namespace of the Route's `gatewayRef`, the
zone namespace. The `KgatewayRouting` resource with the name of that `Gateway` decides for each Route whether kgateway
serves it.

### Routing settings

The routing settings are the same in every option. Only their place differs. `mode` selects how kgateway serves the
Routes of the Kong gateway. The modes follow the rollout steps of ADR 0006:

| Mode | Behavior | ADR 0006 rollout |
| --- | --- | --- |
| `Header` | Every API that is not excluded is routed directly only for requests with `matchHeader`. | Test mode, steps 4 to 7 |
| `Selective` | An API is routed directly for all requests if kgateway supports all its features. Otherwise, only for requests with `matchHeader`. | Partly live, step 8 |
| `All` | Every API that is not excluded is routed directly for all requests, without a feature check. | Fully live, steps 8 and 9 |

- `matchHeader` is required in the modes `Header` and `Selective`.
- `supportedFeatures` is required in mode `Selective`. The other modes do not use it.
- `exclude` lists API base paths that kgateway never serves, in every mode.

`Header` is new. Today, only `Selective` with `supportedFeatures: []` comes close: it routes every API with a header,
because practically every Route uses at least one feature. That depends on the feature detection and does not state the
test mode. A per-API switch back to test mode (ADR 0006, step 8) is not part of this decision.

### Zone

No changes to presets. Also the `gatewayRef` stays as it is, because:

- A preset is an entry point, and kgateway is not a new one. Under ADR 0006, kgateway takes over the ingress for the
  existing hostnames. Consumers call the same URLs, so the preset's URLs, links and issuers stay correct.
- `gatewayRef` names the gateway that answers every request. While Kong is the fallback, every request that kgateway
  does not match still goes to Kong. Kong must therefore keep serving all Routes, and `gatewayRef` must keep pointing to
  it.
- One shared Route needs one `gatewayRef`. Domain operators copy `Status.Presets[].GatewayRef` into
  `Route.spec.gatewayRef`. Under ADR 0008, the Kong controller serves that Route unchanged, and the kgateway controller
  finds the kgateway `Gateway` in the same zone namespace.
- Domain operators stay unaware of kgateway. That is what ADR 0008 decided. API, event, agentic, file, application,
  organization, rover-server, discovery-server and projector all read presets, and none of them needs a kgateway special
  case.
- Preset selection stays unchanged. `SelectPreset`, `default`, `ConsumerFailover` and `MatchingGateways` work as before.
  The special rules for the kgateway preset (never selected, never default, no failover, at most one) go away.
- Consumers keep working. The Application operator creates Kong Consumers on the gateways that the presets reference.
  kgateway has no Consumer entity, so it must not show up there.
- No changes to the Zone API for presets. Existing zone configurations stay valid, and no preset fields need migrating,
  except removing the kgateway preset.
- kgateway is configured in one place. The routing policy (`mode`, `supportedFeatures`, `matchHeader`, `exclude`) is in
  one `KgatewayRouting` resource for each zone.
- The gateway operator never has to read Zones. It decides from the Route, the `Gateway` CRs and `KgatewayRouting`,
  which keeps the domain boundaries.
- Rollout and rollback are easy. Creating or changing the `KgatewayRouting` resource switches kgateway on or off for a
  zone without touching any Route producer. Deleting it sends all traffic back to Kong.
- The end state is an ordinary change. When Kong is removed, the presets' `gatewayRef` switches from `standard` to
  `kgateway`. kgateway is then a normal gateway behind a normal preset, with no new concept and no new field.
- Fewer combinations to validate. A list such as `gatewayRefs: [standard, kgateway]` would need rules for order,
  duplicates and implementations. A single `gatewayRef` needs none of these rules.

The kgateway gateway entry keeps only the fields that every gateway entry has. `supportedFeatures`, `mode`,
`matchHeader` and `exclude` move to `KgatewayRouting`.

```yaml
spec:
  gateways:
    - name: standard                 # Kong, unchanged
      admin: { url: "https://kong-admin.example", identityProviderRef: default }
    - name: kgateway
      implementation: Kgateway       # immutable, as today; at most one per zone
      admin: { url: "https://dataplane-proxy.example", identityProviderRef: default }
  presets:
    - name: default
      type: API
      gatewayRef: standard           # unchanged; no kgateway preset
```

Webhook rule: a zone has at most one gateway entry with `implementation: Kgateway`.

### Gateway

The admin operator creates the kgateway `Gateway` as today, without `supportedFeatures`.

```yaml
apiVersion: gateway.cp.ei.telekom.de/v1
kind: Gateway
metadata: { name: zone-a--kgateway, namespace: env--zone-a }
spec:
  implementation: Kgateway
  admin: { url: ..., clientId: ..., clientSecret: ..., issuerUrl: ... }
```

CEL rules on the CRD: `implementation` is immutable. `redis` and `features` stay Kong-only.

The data-plane target (namespace, Gateway API `Gateway`, listener) stays operator configuration. It is not specific to
the migration. Add a per-zone field to the gateway entry only when a zone needs other values.

### KgatewayRouting

The platform team creates one `KgatewayRouting` resource for each zone, next to the `Gateway` resources in the zone
namespace. It has the name of the kgateway `Gateway` it belongs to.

```yaml
apiVersion: gateway.cp.ei.telekom.de/v1alpha1
kind: KgatewayRouting
metadata:
  name: zone-a--kgateway             # name of the kgateway Gateway
  namespace: env--zone-a
spec:
  mode: Selective                    # Header | Selective | All
  supportedFeatures: [AccessControl, LastMileSecurity]   # required in mode Selective
  matchHeader: { name: X-Gateway-Direct, value: test }   # required in modes Header and Selective
  exclude: { basePaths: ["/eni/payments/*"] }
status:
  conditions: []                     # for example Ready=False if the kgateway Gateway does not exist
```

CEL rules on the CRD check the fields: the required fields of each mode, the header name and the `exclude` patterns. The
controller checks that a `Gateway` with the same name and `implementation: Kgateway` exists, and reports the result in
the status.

### Controller

The kgateway controller finds the kgateway `Gateway` in the namespace of the Route's `gatewayRef`. If the zone has none,
it ignores the Route. Otherwise, it serves:

- Routes whose `gatewayRef` is the kgateway `Gateway` (end state). It serves them fully, without `KgatewayRouting`. A
  Route that needs an unsupported feature is not ready.
- Routes whose `gatewayRef` is another `Gateway` of the zone (migration), if the zone has a `KgatewayRouting`. It uses
  the Route's hostnames and paths, evaluates `exclude`, `mode`, `supportedFeatures` and `matchHeader`, and keeps the
  Route on Kong if it is not eligible. Without `KgatewayRouting`, it removes previously applied objects of these Routes.

A change of the kgateway `Gateway` or of `KgatewayRouting` requeues the Routes of the namespace.

Routes carry no kgateway label or annotation. The domain operators do not mark them. The kgateway controller reports its
result in its own status condition and properties on the Route.

The Kong controller keeps ignoring Routes whose `gatewayRef` is a kgateway `Gateway`.

### Consequences

- Good, because the kgateway controller decides from gateway-domain resources only, for Routes of all domains.
- Good, because preset selection, links and domain operators have no kgateway special cases.
- Good, because kgateway matches exactly the hostnames and paths that Kong serves, as ADR 0006 requires.
- Good, because the Zone API and the `Gateway` CRD get no migration-only fields. After the migration, deleting the
  `KgatewayRouting` resources and the CRD removes the settings.
- Good, because the end state of ADR 0006 is an ordinary preset change, not a new model.
- Good, because the Zone needs no field for the connection between kgateway and Kong.
- Good, because the rollout switches have one place, with their own RBAC and status.
- Bad, because a zone is configured in two places during the migration: the Zone and `KgatewayRouting`.
- Bad, because the `KgatewayRouting` resource cannot be validated against the Zone. Problems show up in its status.
- Bad, because no owner reference removes `KgatewayRouting` with the Zone.
- Bad, because a zone can have only one kgateway gateway.
- Bad, because the data-plane target is the same for all zones until a zone needs a per-zone field.
- Bad, because the Routes of every Kong gateway of the zone are candidates, also those whose hostnames do not pass
  kgateway, for example Event or AI hostnames. Their HTTPRoutes receive no traffic. `exclude` keeps them off kgateway.
- Bad, because zones with a kgateway preset need a configuration change, and `supportedFeatures`, `mode`, `matchHeader`
  and `exclude` are removed from the Zone API.
- Bad, because kgateway cannot use hostnames other than those of Kong while Kong is the fallback. Today's test setups
  with separate kgateway hostnames must call kgateway with the `Host` header of the zone, as ADR 0006 describes.

### Migration

1. Add the `KgatewayRouting` CRD and the `Header` mode.
2. For each zone, create a `KgatewayRouting` resource from the routing settings of the kgateway gateway entry, and
   remove the kgateway preset.
3. Ship the kgateway controller of ADR 0008. Remove the kgateway Route creation from the API operator.
4. Remove `supportedFeatures`, `mode`, `matchHeader` and `exclude` from the Zone gateway entry and `supportedFeatures`
   from the `Gateway`. Remove the kgateway preset rules.
5. When Kong is removed, point the presets to the kgateway entry, remove the Kong entry and delete the `KgatewayRouting`
   resource. When no zone uses it, delete the CRD.

## Pros and Cons of the Options

### A: Keep the kgateway preset

The kgateway preset keeps its own URLs. The admin operator copies its hostnames and paths and the routing settings to
the kgateway `Gateway`, and the kgateway controller rewrites each Route to them.

```yaml
spec:
  gateways:
    - name: standard                 # Kong
      admin: { url: "https://kong-admin.example", identityProviderRef: default }
    - name: kgateway
      implementation: Kgateway
      admin: { url: "https://dataplane-proxy.example", identityProviderRef: default }
      supportedFeatures: [AccessControl, LastMileSecurity]
      mode: Selective                # routing settings on the gateway entry, as today
      matchHeader: { name: X-Gateway-Direct, value: test }
      exclude: { basePaths: ["/eni/payments/*"] }
  presets:
    - name: default
      type: API
      default: true
      gatewayRef: standard
      identityProviderRef: default
      urls: [{ hostname: api.example.com, basePath: / }]
    - name: kgateway                 # never selected, never default, at most one
      type: API
      gatewayRef: kgateway
      identityProviderRef: default
      urls: [{ hostname: kgateway.example.com, basePath: / }]   # can differ from the Kong URLs
```

- Good, because existing zone configurations do not change.
- Bad, because a preset becomes something that is never selected. Selection, links, `ConsumerFailover` and defaults need
  special cases, as today.
- Bad, because the kgateway URLs can differ from the Kong URLs, although kgateway receives the same traffic. The
  controller must map Kong paths to kgateway paths for every Route.

### B: Put the routing settings on the kgateway gateway entry, without a kgateway preset

The kgateway gateway entry gets a `kgateway` block with the data-plane target and the routing settings. The admin
operator copies the block to the kgateway `Gateway`, as it copies `implementation` and `supportedFeatures` today. The
kgateway controller finds the kgateway `Gateway` in the namespace of the Route's `gatewayRef`, as in C.

```yaml
spec:
  gateways:
    - name: standard                 # Kong, unchanged
      admin: { url: "https://kong-admin.example", identityProviderRef: default }
    - name: kgateway                 # at most one per zone; found through the zone namespace
      implementation: Kgateway
      admin: { url: "https://dataplane-proxy.example", identityProviderRef: default }
      supportedFeatures: [AccessControl, LastMileSecurity]
      kgateway:
        dataPlane: { namespace: gateway, gatewayName: gateway, sectionName: proxy }
        mode: Selective
        matchHeader: { name: X-Gateway-Direct, value: test }
        exclude: { basePaths: ["/eni/payments/*"] }
  presets:
    - name: default
      type: API
      default: true
      gatewayRef: standard           # unchanged; no kgateway preset
      identityProviderRef: default
      urls: [{ hostname: api.example.com, basePath: / }]
```

- Good, because presets and domain operators stay unchanged.
- Good, because the connection between kgateway and Kong follows from the zone namespace, without a field.
- Bad, because the routing settings are migration-only fields in the Zone API.

### C: Put the routing settings into a separate kgateway CRD, without a kgateway preset

See [Decision Outcome](#decision-outcome).

The Zone keeps only the kgateway gateway entry with its `implementation`. The admin operator creates the kgateway
`Gateway` from it, as for every gateway entry. The routing settings move into a new CRD `KgatewayRouting` of the gateway
domain. The platform team creates one resource for each zone, next to the `Gateway` resources in the zone namespace. It
has the name of the kgateway `Gateway` it belongs to. The kgateway controller reads it directly.

As in B, presets do not change, and the kgateway controller finds the kgateway `Gateway` in the namespace of the Route's
`gatewayRef`. If the zone has no `KgatewayRouting`, the kgateway controller serves no Routes of other gateways. Deleting
the resource sends all traffic back to Kong. In the end state, the presets point to the kgateway entry, and the resource
and later the CRD are deleted.

```yaml
# Zone
spec:
  gateways:
    - name: standard                 # Kong, unchanged
      admin: { url: "https://kong-admin.example", identityProviderRef: default }
    - name: kgateway                 # at most one per zone; no kgateway-specific fields
      implementation: Kgateway
      admin: { url: "https://dataplane-proxy.example", identityProviderRef: default }
  presets:
    - name: default
      type: API
      default: true
      gatewayRef: standard           # unchanged; no kgateway preset
      identityProviderRef: default
      urls: [{ hostname: api.example.com, basePath: / }]
---
# Created by the platform team in the zone namespace
apiVersion: gateway.cp.ei.telekom.de/v1alpha1
kind: KgatewayRouting
metadata:
  name: zone-a--kgateway             # name of the kgateway Gateway
  namespace: env--zone-a
spec:
  mode: Selective                    # Header | Selective | All
  supportedFeatures: [AccessControl, LastMileSecurity]   # required in mode Selective
  matchHeader: { name: X-Gateway-Direct, value: test }   # required in modes Header and Selective
  exclude: { basePaths: ["/eni/payments/*"] }
status:
  conditions: []                     # for example Ready=False if the kgateway Gateway does not exist
```

- Good, because the Zone API and the `Gateway` CRD get no migration-only fields. After the migration, deleting the
  resources and the CRD removes the settings.
- Good, because the admin operator does not copy routing settings, and the gateway operator reads its own resource.
- Good, because the rollout switches of ADR 0006 have one place. RBAC on the CRD can separate who switches live traffic
  from who changes the Zone.
- Good, because the resource reports its own status, for example a missing or not ready kgateway `Gateway`.
- Bad, because a zone is configured in two places during the migration.
- Bad, because the webhook of the CRD cannot validate against the Zone. It can check the fields and that the kgateway
  `Gateway` exists. The controller reports other problems in the status.
- Bad, because no owner reference removes the resource with the Zone. It is removed with the zone namespace or by the
  platform team.

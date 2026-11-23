---
status: proposed
decision-makers:
  - elmar.meuthen@telekom.de
  - luis.pflamminger@telekom.de
  - michael.fedorenko@telekom.de
  - mika.juznik@telekom.de
  - niklas.beilmann@telekom.de
tags: [extproc, last-mile-security, control-plane]
---

# Carry Subscription Scopes to the Token Minter

## Context and Problem Statement

[0009](0009-define-the-inputs-and-claims-of-the-token-minter.md) recommends that the control plane sends per-route data
to the token minter as dynamic metadata, written by a `TrafficPolicy` transformation on the route. The token minter then
writes the claims of gateway-jumper, without `scope`.

gateway-jumper writes `scope` into the Gateway Token
([`TokenGeneratorService.java`](https://github.com/telekom/gateway-jumper/blob/main/src/main/java/jumper/service/TokenGeneratorService.java)).
The control plane fills the `oauth` map of the `jumper_config` header
([`custom_scopes.go`](https://github.com/telekom/controlplane/blob/79aab2dba3d9c7b9a50690b4855ec475164768de/gateway/internal/features/feature/custom_scopes.go#L46-L78)):

- a `default` entry with the scopes of the route
- one entry per consumer name with the scopes of this consumer

Each entry holds the scopes as one space-separated string. gateway-jumper looks up the entry by the `clientId` claim of
the consumer token
([`JumperConfig.java`](https://github.com/telekom/gateway-jumper/blob/main/src/main/java/jumper/model/config/JumperConfig.java)).
It takes the entry of the consumer, else `default`, else it writes no `scope`. The entry of the consumer replaces
`default`, it is not merged. `scope` is the only claim for which the control plane sends a value per consumer.

Providers that authorize by `scope` would treat a Gateway Token from the token minter differently. Until this is
decided, routes with scopes stay on the current gateway.

How do the scopes of the consumer reach the token minter?

## Decision Drivers

- **Compatibility.** The Gateway Token has the same `scope` as from gateway-jumper.
- **Trusted inputs.** A client must not be able to set the scopes or the identity that selects them.
- **Configurable with kgateway resources.** The control plane writes the scopes with the same `TrafficPolicy` as the
  other route data.
- **Limits of kgateway v2.5.0.** A transformation sets at most 16 `dynamicMetadata` entries, and each value is a string
  ([`traffic_policy_types.go`](https://github.com/kgateway-dev/kgateway/blob/v2.5.0/api/v1alpha1/kgateway/traffic_policy_types.go)).
  The template cannot read dynamic metadata: the function `dynamic_metadata` is commented out
  ([`jinja.rs`](https://github.com/kgateway-dev/kgateway/blob/v2.5.0/internal/envoy_modules/lib/transformation/src/jinja.rs)).
- **Fail closed.** Invalid scope data stops the request.

## Considered Options

- Two route-data keys, the token minter picks the scopes per request
- One route-data key per consumer
- The transformation template picks the scopes
- One route rule per consumer
- The identity provider writes the scopes into the consumer token
- No `scope`, routes with scopes stay on the current gateway

Reading the scopes from the Kubernetes API is not an option. 0009 rejects this for all route data.

This decision covers the routes on which the token minter runs. Failover routes belong to mesh, which stays on the
current gateway, see
[Known differences from gateway-jumper](../developer/token-minter.md#known-differences-from-gateway-jumper).

## Decision Outcome

Open. Recommended option: "Two route-data keys, the token minter picks the scopes per request", because it builds the
same `scope` as gateway-jumper with the transport of 0009, and the token minter selects the scopes by the verified
`clientId`.

The control plane writes two more keys into the dynamic metadata namespace `token-minter`:

| Key | Content | Example |
| --- | --- | --- |
| `scopes` | Scopes of the route, like the `default` entry of gateway-jumper | `read` |
| `consumerScopes` | JSON object from client ID to scopes, only for consumers with their own scopes | `{"client-a":"read write"}` |

The token minter takes `consumerScopes[clientId]`, else `scopes`, else it writes no `scope`. Without a `clientId` claim,
it takes `scopes`. Invalid JSON or a value that is not a string rejects the request with `invalid_route_data` (500).

Not implemented yet.

### Consequences of the Recommended Option

- Good, because the token minter builds the same `scope` as gateway-jumper.
- Good, because the token minter selects the entry by the `clientId` that `jwt` verified, not by a header.
- Good, because the scopes use the same transport and the same `TrafficPolicy` as the other route data.
- Good, because the control plane already builds the scopes (`custom_scopes.go`). Its kgateway writer, planned in
  DHEI-21833, can write them as two more keys instead of into `jumper_config`. DHEI-21833 does not cover scopes yet: the
  epic DHEI-21338 leaves them out of the MVP.
- Bad, because `consumerScopes` grows with the consumers of the route, like the list of client IDs in `rbac`.
- Bad, because the values are templates. OAuth scope names can contain `{`, `%` and `#`
  ([RFC 6749, section 3.3](https://www.rfc-editor.org/rfc/rfc6749.html#section-3.3)), so a scope name can contain `{{`,
  `{%` or `{#`. The control plane must reject scope names with `{`, or write `consumerScopes` base64-encoded. The JSON
  itself is safe: each `{` is followed by `"` or `}`, and scope names cannot contain `"` or `\`, so the JSON needs no
  escaping.

## Pros and Cons of the Options

### Two route-data keys

- Good, because it meets all decision drivers.
- Bad, because the map grows with the consumers and must not contain template syntax.

### One route-data key per consumer

- Good, because each value is a plain string.
- Bad, because the limit of 16 entries per transformation allows only a few consumers per route.

### The transformation template picks the scopes

- Bad, because the template cannot read the payload of `jwt` in the dynamic metadata.
- Bad, because the template can read the request headers, but the token minter does not trust identity headers, and
  `jwt` has already removed the consumer token from the request.

### One route rule per consumer

- Good, because each rule has plain route data.
- Bad, because the route must match on a claim of the consumer token.
- Bad, because the rules grow with the consumers.

### The identity provider writes the scopes

- Good, because the token minter would copy `scope` like the other consumer claims.
- Bad, because the scopes belong to a subscription to one API, and the identity provider does not know the
  subscriptions.

### No `scope`

- Good, because nothing changes in the token minter and the control plane.
- Bad, because routes with scopes cannot move to the new gateway.

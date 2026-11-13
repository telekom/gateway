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

# Define the Inputs and Claims of the Token Minter

## Context and Problem Statement

[0003](0003-mint-the-gateway-token-in-a-custom-extproc.md) decides that a custom ExtProc, the token minter, mints the
Gateway Token. [0007](0007-choose-where-the-token-minter-runs.md) recommends running it as its own Deployment. Envoy
calls the token minter after the kgateway filters `jwt` and `rbac`.

The token minter needs two kinds of input:

- the claims of the verified consumer token
- per-route data from the [control plane](https://github.com/telekom/controlplane): the issuer of the realm, the
  environment and the API base path

Today, Kong sends the per-route data to [gateway-jumper](https://github.com/telekom/gateway-jumper) as request headers
(`realm`, `environment`, `api_base_path`). gateway-jumper builds the issuer from a static base URL and the `realm`
header. On kgateway, the control plane writes the per-route resources (`HTTPRoute`, `TrafficPolicy`) through the
configuration API (DHEI-21833).

How do the inputs reach the token minter, which claims does it write, and how does it fail?

## Decision Drivers

- **Trusted inputs.** A client must not be able to set the consumer identity or the route data.
- **Nothing leaks to the provider.** Route data and the consumer token do not reach the provider.
- **Fail closed.** A missing token minter, missing route data or a partial configuration stops the request. The consumer
  token never reaches the provider.
- **Compatibility.** Providers validate the Gateway Token as before.
- **Configurable with kgateway resources.** The control plane writes everything with the kgateway CRDs, per route.
- **No realm configuration in the token minter.** A new realm needs no change to the token minter.

## Considered Options

For the transport of the per-route data:

- Dynamic metadata, written by a `TrafficPolicy` transformation on the route
- Request headers, set by the route
- The token minter reads the route configuration from the Kubernetes API

The kgateway API does not expose per-route ExtProc metadata (`grpc_initial_metadata`) or writable route metadata. We did
not consider these.

## Decision Outcome

Open. Recommended option: "Dynamic metadata, written by a `TrafficPolicy` transformation on the route", because clients
cannot set it, it does not reach the provider, and the control plane can write it with the same `TrafficPolicy` that
turns on `jwt`, `rbac` and the token minter.

Together with the transport, we recommend these rules for the contract:

- **Consumer claims only from `jwt`.** The token minter reads the verified payload from the dynamic metadata of `jwt`.
  It does not read the `Authorization` header and does not trust identity headers.
- **Issuer per route.** The control plane sends the full issuer URL of the realm for each route. It already computes
  this value (`LmsIssuer` in the zone status). The token minter has no realm, zone or route configuration.
- **Claims of gateway-jumper.** Claims that gateway-jumper omits when their source is missing, the token minter omits
  too. This includes `iat` and `exp`. Rejecting a consumer token without `exp` would most likely make more sense,
  because a Gateway Token without `exp` does not expire. We recommend keeping the gateway-jumper behaviour for
  compatibility. kgateway `jwt` rejects expired tokens, and our identity provider always sets both claims, so the case
  does not occur in practice.
- **No `scope` for now.** The MVP leaves out `scope` (DHEI-21830). gateway-jumper writes `scope` from the scopes of the
  route and of the consumer's subscription. They depend on the consumer, so they do not fit into route data with one
  value per route. [0010](0010-carry-subscription-scopes-to-the-token-minter.md) proposes how they reach the token
  minter. Until then, routes with scopes stay on the current gateway.
- **Fail closed.** Missing consumer claims, missing route data, a missing key or a signing error stop the request with
  an error response that contains no token data. `failOpen` stays `false`.
- **`jwt` removes the consumer token.** `forwardToken` stays `false`. A route without the token minter then forwards no
  token, and the provider rejects the request.
- **Only on LMS routes.** The token minter is attached per route, never to the whole `Gateway`.

The [guide of the token minter](../developer/token-minter.md) states the contract in detail: the
[route data](../developer/token-minter.md#route-data), the
[claims](../developer/token-minter.md#claims-of-the-gateway-token), the
[failure behaviour](../developer/token-minter.md#failure-behaviour) and the
[route configuration](../developer/token-minter.md#route-configuration).

We tested the recommended transport on a test cluster with kgateway v2.5.0, see the
[open checks](../developer/token-minter.md#open-checks):

- The ExtProc at `filterStage` `Route`/`Before` sees the dynamic metadata of the transformation, and runs after `jwt`
  and `rbac`.
- A request that `jwt` (401) or `rbac` (403) rejects does not reach the token minter.
- The ExtProc sees the original `:path`, before a `URLRewrite` of the route.

### Consequences of the Recommended Option

- Good, because the token minter has no realm, zone or route configuration. It only reads its inputs per request.
- Good, because the issuer comes from the control plane, which already computes it. It does not depend on a second
  static value.
- Good, because a route without the token minter forwards no token.
- Good, because at `Route`/`Before` the token minter also runs after the rate limit filters. A rate-limited request gets
  a 429 and costs no signing, as with the current gateway.
- Bad, because the transformation runs late in the filter chain. The token minter must run at `Route`/`Before`, not at
  the default `AuthZ`/`After`.
- Bad, because kgateway renders the transformation values as MiniJinja templates. The control plane must not write
  template syntax into them.
- Bad, because the issuer comes from the control plane, while gateway-jumper builds it from a Helm value. While both
  gateways run, the two values must be equal. On the test cluster they differ: the issuer in the zone status of the
  control plane contains the default port (`https://gateway-a.example.com:443/auth/realms/default`), the issuer of
  gateway-jumper does not. The control plane must leave out the default port.

## Pros and Cons of the Options

### Dynamic metadata from a transformation

- Good, because clients cannot set it.
- Good, because it does not reach the provider.
- Good, because the control plane writes it in the same `TrafficPolicy` as `jwt`, `rbac` and the token minter.
- Bad, because the token minter must run at `Route`/`Before`.
- Bad, because the values are templates.

### Request headers

- Good, because it is the same model as Kong and gateway-jumper today.
- Bad, because the route applies its request header changes in the router, after the ExtProc. The token minter probably
  never sees them.
- Bad, because clients can send the same headers. The route must overwrite them, and the token minter must remove them.

### Read the route configuration from the Kubernetes API

- Good, because the request carries no route data.
- Bad, because a component in the request path then depends on the Kubernetes API, needs RBAC and a cache, and must map
  each request to its route.

## More Information

- Issuer per realm in the control plane:
  [`links.go`](https://github.com/telekom/controlplane/blob/main/admin/internal/handler/zone/links.go)
- kgateway v2.5.0 CRDs: `TrafficPolicy` (`transformation.request.dynamicMetadata`, `jwtAuth`, `rbac`, `extProc`) and
  `GatewayExtension` (`jwt.providers[].forwardToken`, `jwt.validationMode`, `extProc`)

---
status: accepted
decision-makers:
  - niklas.beilmann@telekom.de
  - mika.juznik@telekom.de
  - luis.pflamminger@telekom.de
  - michael.fedorenko@telekom.de
consulted:
  - christian.ingenhaag@telekom.de
tags: [extproc, last-mile-security]
---

# Mint the Gateway Token in a Custom ExtProc

## Context and Problem Statement

With Last Mile Security (LMS), the gateway replaces the consumer token with a Gateway Token that the gateway signs.
Providers validate the Gateway Token with the public keys of the gateway. In the current gateway, the
[gateway-jumper](https://github.com/telekom/gateway-jumper) sidecar mints this token. The new gateway runs on kgateway
(Envoy) and needs a new component for this task.

Requirements from DHEI-21830 and the epic DHEI-21338:

- Sign with the keys from [gateway-rotator](https://github.com/telekom/gateway-rotator). The `kid` header must match the
  key ID that the Issuer Service publishes in its JWKS. We can change how the rotator derives the key ID (`tls.kid`).
  Providers do not change.
- Use rotated keys without a restart.
- Replace the consumer token in the `Authorization` header. Fail closed.
- Never log tokens or keys.
- Validate the consumer token and check consumer access before minting. We prefer the native kgateway features (`jwt`
  and `rbac` in a `TrafficPolicy`). An existing component is also acceptable if the control plane can configure it per
  route with Kubernetes resources in a similar way.
- Prefer existing solutions, and document each evaluation.

The Gateway Token contains claims about the consumer and the route, for example `requestPath`, `env`, `scope` and `aud`.
Most of these values are per-route data from the control plane. The control plane sends them to gateway-jumper as
request headers on each route.

**Realms.** One gateway deployment serves several realms. A realm is an environment. The client selects it with a path
prefix, for example `/realm-a/<apiBasePath>` or `/realm-b/<apiBasePath>`. The realm sets the issuer (`iss`) of the
Gateway Token. All realms of a zone use the same signing key. Today, the control plane sends the realm name per route as
a `realm` header. gateway-jumper does not read the realm from the path.

Do we use an existing open-source component to mint the Gateway Token, or do we write a custom ExtProc?

## Decision Drivers

- **Order and configuration.** JWT validation and the consumer access check run before minting. The control plane
  configures JWT validation, the access check and minting per route with Kubernetes resources.
- **Customizability.** We need full control over all claims, including the standard claims. The token must stay
  compatible with the providers that validate it today.
- **Realm handling.** The issuer must come from per-route control plane data. Adding a realm must not need a change to
  the static configuration of the minter.
- **Key rotation.** Use the rotator keys, and reload keys without a restart. Kong and kgateway run in parallel, so
  tokens from gateway-jumper and from the new minter must validate against the same JWKS.
- **Fail closed.** Missing keys or missing route data must stop the request.
- **Open-source posture.** The component is in the request path of every LMS route. We must be able to rely on it for
  years, or fork and maintain it.
- **Operating cost.** Each extra deployment, configuration format and CRD adds work.
- **Effort.** Cost to build and maintain the solution.

## Considered Options

- Custom ExtProc in Go
- [Heimdall](https://github.com/dadrus/heimdall): Heimdall validates the consumer token, checks consumer access and
  mints the token
- [Authorino](https://github.com/Kuadrant/authorino)

## Decision Outcome

Chosen option: "Custom ExtProc in Go", because it is the only option that meets all decision drivers.

Heimdall is the best existing alternative. It meets the functional requirements, but it is weak on customizability,
realm handling and open-source posture. See
[Pros and Cons of the Options](#pros-and-cons-of-the-options).

### Consequences

- Good, because JWT validation and the consumer access check stay native kgateway features. The ExtProc uses their
  result.
- Good, because the control plane keeps the realm decision per route, as today.
- Good, because the ExtProc can read the rotator Secret as it is.
- Bad, because we own security-relevant code. We keep it small and use only well-known libraries
  ([go-control-plane](https://github.com/envoyproxy/go-control-plane) and
  [go-jose](https://github.com/go-jose/go-jose)).

## Implementation Sketch

### Custom ExtProc

```text
client -> jwt (AuthN) -> rbac (AuthZ) -> ext_proc (Gateway Token) -> router
```

1. The ExtProc is a gRPC server that implements the Envoy `ext_proc` v3 API. It processes request headers only and skips
   bodies and responses.
2. A kgateway `GatewayExtension` registers the ExtProc. A `TrafficPolicy` turns it on for LMS routes. `failureModeAllow`
   stays `false`.
3. The ExtProc needs per-route data from the control plane (realm or issuer, `env`, audience, scopes) and the claims of
   the verified consumer token. A separate decision covers how this data reaches the ExtProc.
4. The ExtProc builds the claims and signs the token with RS256. The `kid` header is `tls.kid`.
5. The ExtProc returns a header mutation. It sets `Authorization: Bearer <gateway token>`.
6. If a step fails, the ExtProc returns an immediate error response. It does not forward the consumer token.
7. The ExtProc mounts the rotator Secret as a volume, watches the files and swaps the key atomically. If `tls.key` or
   `tls.kid` is empty, for example right after the rotator creates the Secret, the ExtProc is not ready and rejects
   requests.
8. Logs contain the `kid` and the claim names, never tokens or keys.

A separate decision covers how we deploy the ExtProc (sidecar or own deployment).

### Heimdall

Heimdall runs in decision mode. Envoy calls it through the gRPC `ext_authz` API. A kgateway `GatewayExtension` of type
ExtAuth registers it, and a `TrafficPolicy` turns it on per route.

Heimdall validates the consumer token, checks consumer access with a CEL authorizer on the client ID and mints the
token. It writes the `Authorization` header directly. LMS routes do not use the kgateway `jwt` and `rbac` filters. The
control plane writes the access rules into the RuleSet of each route, instead of a `TrafficPolicy`.

```text
client -> ext_authz (Heimdall: validate, check access, mint) -> router
```

The configuration has two parts:

- **Static** (`heimdall.yaml`, from the Helm chart; a change needs a restart):
  - `jwt` authenticators for the consumer token. Only the path of the JWKS or metadata URL can contain the token issuer,
    so we need one authenticator per issuer host.
  - a `map` contextualizer that gives the HTTP method and path to the claims template.
  - one `jwt` finalizer per realm. The finalizer's signer `name` is the issuer URL of the realm, and the signer `secret`
    points to the signing key.
  - a default rule that denies all requests without a matching rule.
- **Dynamic** (one `RuleSet` CRD per route, generated by the control plane, reloaded without a restart): matches the
  route and selects the steps. A rule can override the claims, the `ttl` and the template `values` of the finalizer and
  the assertions (for example trusted issuers) of the authenticator. It cannot override the signer, the output header or
  the JWKS endpoint.

The control plane selects the realm per route: the RuleSet of a route refers to the finalizer of its realm. The values
for the claims (`env`, audience and so on) go into the `values` of the rule.

```yaml
rules:
  - id: route-a
    match:
      routes:
        - path: /realm-a/some/api/v1/**
    execute:
      - authenticator: consumer_jwt
        config:
          assertions:
            issuers: [<trusted consumer token issuer>]
      - authorizer: consumer_access # CEL check on the client ID
      - contextualizer: request_info
      - finalizer: lms_realm_a
        config:
          values:
            env: realm-a
```

**Keys.** Heimdall reads the signing key from a PEM file and watches the file for changes. If the PEM block has an
`X-Key-ID` header, Heimdall uses it as the key ID. Otherwise the key ID is the hex SHA-1 hash of the public key. Neither
of the two ways to match the Issuer Service JWKS needs a change to Heimdall:

- The rotator also writes the key as a PEM entry with the header `X-Key-ID: <tls.kid>`. The key IDs stay as they are, so
  gateway-jumper and the Issuer Service do not change. This is a rotator change of about 50 to 80 lines of code plus
  tests. We prefer this way.
- The rotator derives `tls.kid` as the SHA-1 hash of the public key, and Heimdall reads `tls.key` directly. The rotator
  calculates the key ID only for a new key. The keys that are already in the Secret keep their old key IDs, so Heimdall
  signs with a wrong key ID for up to two rotations. If the rotator recalculates all key IDs at once instead, the new
  key IDs are not published in advance, and validation fails for a short time.

## Pros and Cons of the Options

### Custom ExtProc in Go

- Good, because it meets all decision drivers.
- Good, because ExtProc is the only extension point with a configurable `filterStage` in kgateway.
- Good, because it uses the rotator Secret without changes.
- Bad, because we build and maintain security-relevant code.

### Heimdall

- Good, because it is an existing component (Apache-2.0) with frequent releases.
- Good, because it reloads the signing key and the RuleSets without a restart.
- Good, because the control plane can configure it per route with a CRD.
- Good, because one filter does validation, access check and minting, in the correct order.
- Good, because the default rule denies routes without a RuleSet.
- Bad, because customizability is limited:
  - The signer always sets `iss`, `exp`, `iat`, `nbf`, `jti` and `sub` after it renders the custom claims. `iss` is the
    signer name.
  - The token cannot keep `iat` and `exp` of the consumer token, as gateway-jumper does today. This changes the token
    for providers.
  - Rules cannot override the signer, the output header or the JWKS endpoint of an authenticator.
- Bad, because realm handling is difficult:
  - Each realm needs its own finalizer in the static configuration, because `iss` is fixed per signer.
  - A new realm needs a Helm change and a restart of Heimdall.
  - The issuer URLs live in the Helm values, not in the control plane. The control plane must know the finalizer names
    from the Helm values. The two can drift.
- Bad, because the open-source posture is weak (as of 2026-10-29):
  - One maintainer. `MAINTAINERS.md` lists one person. On GitHub, that person has 1343 commits, and the next human
    contributor has 4.
  - About 260 stars and 34 forks. One organization is listed in `ADOPTERS.md`.
  - The version is still 0.x. The RuleSet CRD is `v1alpha4`, and the migration to `v1beta1` needs a conversion
    controller. The control plane would write this alpha API for every route.
  - Heimdall would be in the request path of every LMS route, with a bus factor of one.
- Bad, because it is one more deployment, configuration format and CRD to operate.
- Bad, because all authentication and authorization of LMS routes depends on Heimdall, not only the minting.
- Bad, because the consumer access check moves from kgateway `rbac` to CEL expressions in Heimdall rules.

### Authorino

- Bad, because the key ID is the name of the Kubernetes Secret. The rotator puts each new key into the same Secret, so
  the key ID does not change when the key changes.
- Bad, because the key must be in `key.pem`. This does not match the rotator Secret.
- Bad, because it does not reload the signing Secret when it changes.

## More Information

Versions used for the evaluation: kgateway v2.4.7 (`main` at `643f50b`), Heimdall v0.17.27 (`main` at `d397dd5`),
Authorino `main` at `448d295`.

- kgateway:
  [ExtProc `filterStage`](https://github.com/kgateway-dev/kgateway/blob/643f50b3d0084097a2f4d5e3ec3add78cde702e1/pkg/kgateway/extensions2/plugins/trafficpolicy/extproc_policy.go#L222-L258)
- Heimdall:
  [standard claims set by the signer](https://github.com/dadrus/heimdall/blob/d397dd5d20f868fd95cbed5b507afd97e45475c2/internal/rules/mechanisms/finalizers/jwt_signer.go#L106-L111),
  [finalizer fields that rules cannot override](https://github.com/dadrus/heimdall/blob/d397dd5d20f868fd95cbed5b507afd97e45475c2/internal/rules/mechanisms/finalizers/jwt_finalizer.go#L195-L196),
  [authenticator fields that rules cannot override](https://github.com/dadrus/heimdall/blob/d397dd5d20f868fd95cbed5b507afd97e45475c2/internal/rules/mechanisms/authenticators/jwt_authenticator.go#L289-L299),
  [templated issuer only in the URL path](https://github.com/dadrus/heimdall/blob/d397dd5d20f868fd95cbed5b507afd97e45475c2/docs/content/docs/mechanisms/authenticators.adoc#L374),
  [key ID from the PEM file](https://github.com/dadrus/heimdall/blob/d397dd5d20f868fd95cbed5b507afd97e45475c2/internal/secrets/provider/pem/key_store.go#L85),
  [generated key ID](https://github.com/dadrus/heimdall/blob/d397dd5d20f868fd95cbed5b507afd97e45475c2/internal/secrets/provider/pem/key_store.go#L176-L192),
  [PEM file watch](https://github.com/dadrus/heimdall/blob/d397dd5d20f868fd95cbed5b507afd97e45475c2/internal/secrets/provider/pem/provider.go#L64-L99),
  [static and dynamic configuration](https://github.com/dadrus/heimdall/blob/d397dd5d20f868fd95cbed5b507afd97e45475c2/docs/content/docs/operations/configuration.adoc),
  [MAINTAINERS.md](https://github.com/dadrus/heimdall/blob/d397dd5d20f868fd95cbed5b507afd97e45475c2/MAINTAINERS.md),
  [ADOPTERS.md](https://github.com/dadrus/heimdall/blob/d397dd5d20f868fd95cbed5b507afd97e45475c2/ADOPTERS.md)
- Realm and per-route data in the current gateway: `gateway/internal/features/feature/last_mile_security.go` in the
  [control plane](https://github.com/telekom/controlplane) sets the `realm` and `api_base_path` headers per route.

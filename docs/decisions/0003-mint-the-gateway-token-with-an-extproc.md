---
status: proposed
decision-makers:
  - niklas.beilmann@telekom.de
  - christian.ingenhaag@telekom.de
  - mika.juznik@telekom.de
  - luis.pflamminger@telekom.de
  - michael.fedorenko@telekom.de
tags: [extproc, lms]
---

# Mint the Gateway Token with an ExtProc Instead of Authorino

## Context and Problem Statement

With Last Mile Security (LMS), the gateway replaces the consumer token with a short-lived Gateway Token before it
forwards a request to the provider. The provider validates this token against the JWKS of the
[Issuer Service](https://github.com/telekom/issuer-service-go). In the current gateway,
[Jumper](https://github.com/telekom/gateway-jumper) mints the token. On kgateway, Jumper is no longer in the request
path.

The gateway must:

- mint the token only after the consumer token is validated and the ACL check passed,
- sign with the keys that `gateway-rotator` rotates every 7 days, with a `kid` that matches the Issuer Service JWKS,
- use a rotated key without a restart,
- keep the Issuer Service and the providers unchanged,
- reject the request if minting fails.

[Authorino](https://github.com/Kuadrant/authorino) can issue signed JWTs ("Festival Wristbands") and is an existing
option. Do we use Authorino or our own ExtProc to mint the Gateway Token?

## Decision Drivers

- Prefer existing solutions to custom components, but only if they fit.
- The key format, key ID and rotation of `gateway-rotator` and the Issuer Service stay as they are.
- We control the release of the component in the request path.

## Considered Options

- Authorino as ext_authz service
- Own ExtProc that mints the token

## Decision Outcome

Chosen option: "own ExtProc that mints the token", because Authorino only fits after upstream changes that are too large
for us to depend on.

To use Authorino, we would need these changes in Authorino:

1. Accept PKCS#8 keys. All rotator keys use PKCS#8, and Authorino only loads PKCS#1 and SEC1 keys.
2. Read the key and the `kid` from configurable fields of a Secret. Authorino reads `key.pem` and uses the Secret name
   as `kid`.
3. Reload a signing key when its Secret changes. Authorino reads keys only when it reconciles an AuthConfig.
4. Reject the request when a response item fails. Authorino allows the request when signing fails.

None of these has an issue or pull request upstream. Changes 2 and 4 change the AuthConfig API and need agreement with
the maintainers and a release of authorino-operator. The timing is out of our control.

Even with these changes, Authorino would have to replace the `jwt` and `rbac` filters of kgateway, because kgateway runs
ext_authz before `jwt`, and the control plane would have to generate one AuthConfig per route.

### Consequences

- Good, because the ExtProc reads `tls.key` and `tls.kid` from the rotator Secret directly and can reload them without a
  restart.
- Good, because kgateway validates the consumer token and enforces the ACL before the ExtProc runs.
- Good, because the Issuer Service and the providers stay unchanged.
- Bad, because we build and maintain one more component in the request path.

## More Information

- [kgateway discussion #13808](https://github.com/kgateway-dev/kgateway/discussions/13808): only the ExtProc filter
  stage is configurable, ext_authz stays in the AuthN stage
- [Authorino Festival Wristband tokens](https://github.com/Kuadrant/authorino/blob/main/docs/features.md#festival-wristband-tokens-responsesuccessheadersdynamicmetadatawristband)

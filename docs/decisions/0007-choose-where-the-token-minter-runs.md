---
status: proposed
decision-makers:
  - elmar.meuthen@telekom.de
  - luis.pflamminger@telekom.de
  - michael.fedorenko@telekom.de
  - mika.juznik@telekom.de
  - niklas.beilmann@telekom.de
tags: [extproc, deployment]
---

# Choose Where the Token Minter Runs

## Context and Problem Statement

[0003](0003-mint-the-gateway-token-in-a-custom-extproc.md) decides that a custom ExtProc mints the Gateway Token. We
call it the token minter. Envoy calls it for each request on a route with Last Mile Security (LMS), after `jwt` and
`rbac`. The token minter signs with the key that [gateway-rotator](https://github.com/telekom/gateway-rotator) writes
into a Kubernetes Secret.

DHEI-21830 asks us to evaluate the packaging and to check whether Envoy can reach the token minter over a Unix domain
socket. A hackathon prototype ran the token minter as a sidecar in the Envoy pod.

Where does the token minter run?

## Decision Drivers

- **Availability of the gateway.** The proposed zone migration puts kgateway in front of all traffic of a zone and sends
  routes that did not move to Kong through a fallback route. A failure of the token minter must only affect LMS routes,
  not the fallback route or other routes.
- **Scaling.** RSA signing costs CPU on every LMS request. Envoy and the token minter have different load profiles.
- **Key access.** A pod can mount a Secret only from its own namespace. The rotator writes the Secret in its own
  namespace, and Kong uses the same Secret.
- **Latency and failure points** in the request path.
- **Independence from kgateway releases.** An upgrade of kgateway or Envoy must not need a new token minter build.
- **Operating cost.**

## Considered Options

- Own Deployment behind a Service
- Sidecar in the Envoy pod, added through `GatewayParameters.spec.kube.deploymentOverlay`
- Custom Envoy proxy image that contains the token minter

A Unix domain socket is not possible with the current kgateway API (v2.5.0). `GatewayExtension.spec.extProc.grpcService`
needs a `backendRef`, and a static `Backend` has only a host and a port. The sidecar option therefore uses `127.0.0.1`.

### Signing Key in Another Namespace

The sidecar and the custom image run in the Envoy pod, and kgateway creates the Envoy pod in the namespace of the
`Gateway`. If this is not the namespace of the rotator Secret, the token minter cannot mount the Secret. Two ways solve
this. Neither solves the readiness and scaling problems of the two options.

**A copy of the Secret**, for example with [Reflector](https://github.com/emberstack/kubernetes-reflector). Costs:

- The rotator creates its Secret without the Reflector annotations. A small change in the rotator can set them, for
  example by copying them from the cert-manager Secret.
- If the copy stops, the token minter keeps signing with an old key and stays ready. The JWKS publishes the previous,
  active and next key. At the next rotation, the old key becomes the previous key and stays in the JWKS. After the
  second rotation, it is no longer in the JWKS, and providers reject the Gateway Token.
- The signing key exists twice, and Reflector needs write access to Secrets in all namespaces.

**Access through the Kubernetes API.** A Role in the namespace of the rotator allows `list` and `watch` on this Secret,
restricted with `resourceNames`, and a RoleBinding grants it to the ServiceAccount of the token minter. The token minter
watches the Secret through the API instead of a mounted volume. With `resourceNames`, Kubernetes allows the list and
watch requests only with the field selector `metadata.name=<name of the Secret>`, see
[Referring to resources](https://kubernetes.io/docs/reference/access-authn-authz/rbac/#referring-to-resources).
Costs:

- The token minter needs a second way to load the key, from the API instead of the mounted files.
- In the Envoy pod, the permission belongs to the ServiceAccount that kgateway creates for the `Gateway`, and the token
  minter container needs its own projected token for the API server. kgateway turns off the automatic token mount, and
  the xDS token of the Envoy container has the audience `kgateway`.

## Decision Outcome

Open. Recommended option: "Own Deployment behind a Service", because it scales independently of Envoy, and a token
minter that is not ready does not take Envoy pods out of service.

A test cluster ran the token minter this way with kgateway v2.5.0: Envoy balanced the requests across all token minter
pods, and with no token minter pod running, the gateway rejected the requests of LMS routes (fail closed).

### Consequences of the Recommended Option

- Good, because the token minter scales on its own load, for example with a HorizontalPodAutoscaler on CPU.
- Good, because the readiness of the token minter does not affect Envoy. Without a token minter, LMS routes fail closed
  and all other routes keep working.
- Good, because the Deployment runs in the namespace of the rotator Secret and mounts it as it is. The rotator does not
  change, even if kgateway runs in another namespace.
- Good, because kgateway and the token minter have separate releases and rollouts.
- Bad, because each LMS request needs one more network hop to the token minter. The request sends only headers and
  metadata, in one gRPC round trip on an open HTTP/2 connection. With `trafficDistribution: PreferClose` on the Service,
  Envoy prefers token minter pods in its own availability zone, so the hop crosses a zone only on failover. Each zone
  then needs its own token minter capacity. See [Locality](../developer/token-minter.md#locality) in the guide. The cost
  of the hop is an [open check](../developer/token-minter.md#open-checks).
- Bad, because verified consumer claims and the Gateway Token cross the network between Envoy and the token minter. The
  connection needs protection, see the
  [deployment in the guide of the token minter](../developer/token-minter.md#deployment).
- Bad, because there are more resources to operate: Deployment, Service, PodDisruptionBudget, autoscaler and network
  policy.

We do not prefer the same node. `trafficDistribution: PreferSameNode` and pod affinity to the Envoy pods would also
remove the hop between nodes. But each Envoy pod would send all its LMS requests to the one token minter pod on its
node, and pods that the autoscaler adds would get traffic only on failover. The token minter would have to scale with
the Envoy pods, like a sidecar.

The [guide of the token minter](../developer/token-minter.md#deployment) describes the Deployment.

## Pros and Cons of the Options

### Own Deployment behind a Service

- Good, because it scales and rolls out independently of Envoy.
- Good, because its readiness does not affect the Envoy pods.
- Good, because it can run in the namespace of the rotator Secret.
- Bad, because it adds a network hop and needs protection of the connection.
- Bad, because it adds resources to operate.

### Sidecar in the Envoy pod

- Good, because Envoy reaches it on `127.0.0.1`, without a network hop and without TLS.
- Bad, because a sidecar that is not ready takes the whole Envoy pod out of service. With kgateway in front of all
  traffic, a missing key would stop the whole zone. To avoid this, the sidecar would have to report ready without a key.
- Bad, because it scales with the Envoy replicas, not with the LMS load.
- Bad, because the `Gateway` must be in the namespace of the rotator Secret. Otherwise the token minter needs a copy of
  the Secret or access through the Kubernetes API, see
  [Signing Key in Another Namespace](#signing-key-in-another-namespace).
- Bad, because kgateway does not validate the overlay when the `GatewayParameters` are created. It applies the overlay
  as a strategic merge patch when it renders the Deployment, so errors show up only then.

### Custom Envoy proxy image

- Good, because it is one container.
- Bad, because we must rebuild the image for each kgateway release.
- Bad, because two processes share one container, without separate probes, resources or restarts. The probe of the
  container checks Envoy. If the token minter process stops, the pod stays ready, and nothing restarts the token minter
  unless the image adds a process supervisor. LMS requests on this pod then fail, and Kubernetes does not notice.
- Bad, because it has the same namespace problem as the sidecar, see
  [Signing Key in Another Namespace](#signing-key-in-another-namespace).

## More Information

- Envoy ExtProc fields in the kgateway API:
  [`GatewayExtension` CRD, v2.5.0](https://github.com/kgateway-dev/kgateway/blob/v2.5.0/install/helm/kgateway-crds/templates/gateway.kgateway.dev_gatewayextensions.yaml)
- Static `Backend` fields:
  [`Backend` CRD, v2.5.0](https://github.com/kgateway-dev/kgateway/blob/v2.5.0/install/helm/kgateway-crds/templates/gateway.kgateway.dev_backends.yaml)

---
status: proposed
decision-makers: []
tags: [architecture, configuration]
---

# Configure Data-Plane Resources through an OAuth-Protected Kubernetes API Proxy

## Context and Problem Statement

The [distributed control plane (dCP)](https://github.com/telekom/controlplane) translates Rover configuration into
gateway configuration. For kgateway, the dCP must manage Gateway API and kgateway resources in separate data-plane
clusters. The dCP must not get ServiceAccount credentials for the data-plane clusters. It must also not get direct
network access to their Kubernetes API servers. Access through an authenticated proxy that uses the Kubernetes API
protocol is acceptable.

We trust the configuration that the dCP renders. This record answers two questions:

- Where do translation, validation and reconciliation run?
- How does the dCP find out about changes to data-plane resources?

## Decision Drivers

- Reuse the dCP lifecycle. Keep one owner of the desired route configuration.
- Keep Kubernetes credentials and permission enforcement inside each data-plane cluster.
- Do not duplicate route semantics or Kubernetes API behavior in a custom service.
- Detect drift in the target clusters. Recover from missed events, partial writes and control-plane outages.
- Install nothing in a data-plane cluster beyond the gateway chart. Managed (CaaS) and NatCo clusters are partly outside
  our control, and tenants there can lack cluster-wide permissions.

## Considered Options

- A transparent, OAuth-protected Kubernetes API proxy, with reconciliation in the dCP.
- A custom route-operation API that validates and applies resource bundles.
- A data-plane controller with its own desired-state model and route reconciliation.

## Decision Outcome

Proposed option: **an OAuth-protected Kubernetes API proxy with central reconciliation**. Deploy one proxy endpoint for
each data-plane cluster. The dCP translates the configuration one time. Then it uses a Kubernetes client through that
endpoint. The proxy authenticates and authorizes the OAuth client. Then the proxy forwards the request with a local,
namespace-scoped ServiceAccount. The proxy has no route model, no work queue and no reconciliation loop of its own.

A custom route-operation API adds a route-specific contract, but no responsibility needs that contract. A data-plane
controller adds a second layer of route reconciliation. The dCP owns and renders trusted configuration, so neither
option is necessary.

### Validation belongs to the layer that owns the rule

| Layer | Responsibility |
| --- | --- |
| dCP | Validates route intent and supported features. Renders resources. Selects targets. Tracks resource ownership. Decides a safe order for creation, update and deletion. |
| OAuth proxy | Validates the access token and the permitted machine-client identity. Replaces the caller credentials with the local Kubernetes identity. Forwards requests and responses. |
| Kubernetes API server | Authorizes that identity with RBAC. Validates objects against the installed CRDs and sets their defaults. Runs the installed admission policies. Enforces native write preconditions. |
| kgateway and runtime services | Resolve resource references. Publish controller status. Configure the proxies. Enforce authentication and authorization of traffic. |

Use RBAC to limit the proxy ServiceAccount by namespace, API resource and verb. Labels identify the resources that the
dCP owns, for inventory and cleanup. Labels are not an authorization boundary. The proxy does not validate route
ownership, backend references or security-policy fields again. If other field restrictions are necessary, put them in
Kubernetes admission. Do not implement the dCP validation a second time.

Before the dCP applies rendered resources, it requests a server-side preflight with `dryRun=All`. The proxy forwards
that option, and the Kubernetes API server does the validation. A dry run does not reserve anything. It is also not a
transaction across objects. A valid schema does not prove that a reference resolves or that traffic is protected.

### Keep the Kubernetes API semantics

Keep the native Kubernetes operations and responses. These include discovery, list/watch and server-side apply. The dCP
owns managed-field comparison, cleanup, retries and recovery after uncertain writes. A successful write confirms
persistence, not readiness. A deletion can stay pending because of finalizers.

The platform installs the CRDs, the shared Gateway infrastructure and the runtime protection. During partial updates,
traffic must keep JWT validation, consumer authorization and last-mile backend authentication. If this is not possible,
the gateway must deny the traffic. The dCP update plan and the runtime configuration must make sure of this. Write order
and accepted-resource conditions alone do not prove it. Verify the traffic behavior in the integration spike. This
requirement stays the same when semantic checks move out of the proxy.

### Reuse the dCP lifecycle and add a kgateway adapter

At
[controlplane commit `6c33e47`](https://github.com/telekom/controlplane/commit/6c33e47adefa88ae4da0b7d5e701752f96020d65),
the configuration goes from `Rover` through `ApiExposure`, `gateway.Route` and `RouteHandler` to `Kong`. The
[Route controller][route-controller] watches local Route and ConsumeRoute changes. The [Kong adapter][kong-reconcile]
reads the actual state and writes the differences. [Periodic requeues][shared-controller] also check sources that did
not change. The requeue interval is configurable and has jitter. Its [default is 30 minutes][requeue-config].

Reuse the shared lifecycle for scheduling, retries and finalizers. The [RouteHandler][route-handler] and the feature
builder are specific to Kong. Add a kgateway renderer and a client for the target resources. The
[client factory][client-factory] already uses OAuth client credentials. Adapt its transport lifetimes for watch streams.
These source findings do not show which version is deployed. They also do not show an existing integration for remote
Kubernetes watches.

At intake, `roverctl` [parses `rover.yaml` and submits JSON][rover-submit]. The
[Rover API stores the CR and returns `202 Accepted`][rover-api]. Then the CLI polls the source status. Submission and
source completion are different from the proposed gateway-acceptance milestone. We have not yet defined how the Rover
status API shows this milestone.

### Write and clean up target resources safely

The dCP does not use transactions with Kong. The [shared lifecycle][shared-controller] adds the finalizer before the
first external call. Each Kong write is an idempotent upsert with a stable name. The first error stops the
[Kong build][kong-build], and the next reconciliation completes it. Only after all writes succeed, the adapter deletes
the owned plugins that are no longer in the desired set. The Route finalizer stays until the deletion in Kong succeeds.

The kgateway adapter keeps this model and adds these rules:

- **Stable identity.** Derive each resource name from the Route and its environment. Label each resource with the source
  UID, the environment and its role. The cleanup selector of one owner must not match the resources of another owner.
- **Preflight first.** Request `dryRun=All` for every resource of the route before the first real write. If one resource
  fails, write nothing and report the name of that resource.
- **Protection before exposure.** Apply the policies first and the HTTPRoute last. While the HTTPRoute does not exist,
  the `/` fallback sends the traffic to Kong. For an update, apply the changes that keep or add protection before the
  changes that widen access. Verify in the spike that kgateway applies a policy that exists before its target route in
  the first translation of that route.
- **No rollback.** Do not undo successful writes after an error. Keep the Route not ready and requeue it. Server-side
  apply with a stable field manager makes the next attempt idempotent.
- **Clean up after success only.** Delete stale resources only after all applies of the run succeed and the label query
  returned a complete list. A failed or partial list is not an empty inventory.
- **Delete in reverse order.** Delete the HTTPRoute first, so the traffic goes to Kong again. Delete the policies only
  after the HTTPRoute is gone. Remove the Route finalizer only when the label query returns no resources, also none with
  pending finalizers. If the target is unreachable, the finalizer stays.
- **No owner references.** An owner reference cannot point to the Route in the control-plane cluster. In the target
  cluster, the HTTPRoute could own its policies, but then it must exist before them. Also, foreground deletion removes
  the policies while the HTTPRoute still exists. Use the explicit order and the label cleanup instead.
- **Error classes.** Retry network errors, `429` and `5xx` with backoff. After a `409`, read the object again before the
  next attempt. Report `403` and `422` as blocked, with the message from Kubernetes. A `404` on delete is a success. A
  watch that ends with `410 Gone` needs a new list.

DHEI-21831 asks for metrics on apply and delete operations, errors and latency. The dCP client already supplies them:
controller-runtime registers [`rest_client_requests_total`][client-metrics] with the labels `code`, `method` and `host`,
and `rest_client_request_duration_seconds` with `verb` and `host`. The host identifies the target. `PATCH` counts
applies, and `DELETE` counts deletions. Requests that the proxy rejects appear only in the proxy metrics.

### Prefer a central watch over a separate notification service

For the first implementation, we propose that the dCP opens namespace-scoped list/watch streams through the proxy. The
dCP opens one stream for each target and resource kind. Map each event to a central `gateway.Route` identity. The
identity includes the source UID, the target and the Gateway. A change to a shared resource enqueues all affected
Routes. A watch event schedules a reconciliation. The reconciliation reads the current desired and actual state before
it decides to write. It does not need to rebuild the Rover chain.

The watch client runs in the dCP. The dCP sends an HTTP request, and the Kubernetes API server streams the changes in
the response. The proxy forwards that response. In the `kubectl proxy` composition, that process runs no informer and no
dCP callback. Each watch is a request and response stream that the dCP starts. The dCP needs no separate inbound
connection.

By default, a watch sends the complete changed object, also for status updates. It does not read unchanged objects
again. Event filtering in the dCP reduces reconciliation work, but not network traffic. When you add watches, examine
the periodic GET schedule again. If the schedule stays the same, the watches add traffic and the polling traffic does
not decrease.

Keep a slower recovery schedule and central inventory audits for each target. Requeues for each Route cannot find
orphans whose source no longer exists. An informer resync processes the cached objects again. It does not get a new
remote inventory. Do not start cleanup after an incomplete source read. Do not start cleanup when the target cache is
stale or not synchronized. Test reconnect and relist, missed deletions and readiness events.

The simpler alternative is to use only frequent inventory audits, without a remote watch. Each audit lists the managed
resources and their status. It compares them with the intended state and schedules repairs or readiness updates. This
alternative needs no lifecycle work for remote streams and caches. But it transfers unchanged resources again and again.
It also detects drift only at the next audit. Both options keep the local watches of the source controllers and the
reconciliation of kgateway. We have not yet decided the audit intervals.

A data-plane watcher can filter and combine events locally and send small hints. But it adds a deployment and a
notification protocol. The watcher can call an authenticated dCP endpoint, or it can expose a stream that the dCP opens.
Only the callback needs a connection that the data plane starts. In both cases, the dCP still reads the state and
decides what to reconcile. Use this extra component only if local filtering or network constraints make it necessary.
The inspected dCP does not yet implement either remote observation path.

### Observe configuration acceptance separately from serving

When the desired objects match, the kgateway adapter observes their status through the same watch and read interface.
For the source baseline, kgateway v2.3.0 with Envoy, check these resources:

| Resource | Required evidence for configuration acceptance |
| --- | --- |
| [HTTPRoute][route-status] | The entry for the intended parent and controller has `Accepted=True` and `ResolvedRefs=True`. The entry reports no `PartiallyInvalid=True`. If the parent reference specifies a listener section, match that section. |
| [Required policies][policy-status] | The entry for the intended ancestor and controller is accepted. If the status reports attachment, the policy is attached. Route acceptance does not prove policy acceptance. |
| Gateway and chosen listener | The relevant acceptance, programming and reference-resolution conditions are positive. The listener has no conflict. |

The conditions must describe the current desired generation of each resource. A Gateway generation does not track a
later change to a route or policy. If a condition is missing, stale or `Unknown`, the resource stays pending. A negative
condition shows the reason, and the next reconciliation examines the resource again. Status-only changes must start
readiness checks. A filter that accepts only generation changes would miss them. A timeout reports progress that is not
confirmed. It does not report success.

These checks prove configuration acceptance. They do not prove serving. In the reviewed version, kgateway sets the
[Gateway and listener condition `Programmed`][kgateway-status] from translation reports.
[Status publication and xDS snapshot updates][kgateway-sync] do not wait for proxy ACKs. A serving check needs more
evidence, for example a probe for the specific route. One successful probe proves only the tested path. It does not
prove every replica or all authorization behavior.

The current dCP [RouteHandler][route-handler] sets `Ready` after its Kong feature build. The new adapter must observe
the asynchronous target status instead. The meaning of the public dCP condition `Ready` is still open. We prefer that
`Ready` means "configuration accepted by kgateway", without a live-route probe in that readiness gate. Serving
verification stays a separate signal from operations or integration tests. Confirm the contract of the public condition
before implementation.

### Security requirements

The internal wiki page [Cross-cluster Configuration][security-wiki] assesses this design against the PSA requirements.
It also lists the security questions that we must answer before we accept this record.
[ADR 0005](0005-reuse-an-existing-kubernetes-api-proxy.md#protect-the-adapter-from-in-cluster-access) records how the
adapter is protected.

### Consequences

- Existing Kubernetes clients and API machinery replace a custom bundle protocol. The dCP must match the CRD versions in
  the target cluster.
- We trust the dCP semantics within the granted RBAC scope. The proxy cannot detect a manifest that is logically wrong
  but authorized. One endpoint grants one execution scope. Separate trust scopes need separate identities.
- The resources stay in place during a proxy or dCP outage. kgateway continues to reconcile the resources that it sees.
  The dCP restores the intended route resources only after it connects again.
- Source finalizers must wait for remote cleanup, including pending finalizers in the target cluster. An unreachable
  target can block the deletion of a source. Consider this dependency when you decommission a target.
- [ADR 0005](0005-reuse-an-existing-kubernetes-api-proxy.md) covers the product selection.

## More Information

- [Route configuration flow](../diagrams/route-configuration-flow.md)
- [Kubernetes API concepts, dry run and watch](https://kubernetes.io/docs/reference/using-api/api-concepts/)
- [Kubernetes RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/)

[route-controller]: https://github.com/telekom/controlplane/blob/6c33e47adefa88ae4da0b7d5e701752f96020d65/gateway/internal/controller/route_controller.go#L45-L68
[kong-reconcile]: https://github.com/telekom/controlplane/blob/6c33e47adefa88ae4da0b7d5e701752f96020d65/gateway/pkg/kong/client/reconcile.go#L32-L68
[shared-controller]: https://github.com/telekom/controlplane/blob/6c33e47adefa88ae4da0b7d5e701752f96020d65/common/pkg/controller/controller.go#L67-L219
[requeue-config]: https://github.com/telekom/controlplane/blob/6c33e47adefa88ae4da0b7d5e701752f96020d65/common/pkg/config/config.go#L27-L38
[route-handler]: https://github.com/telekom/controlplane/blob/6c33e47adefa88ae4da0b7d5e701752f96020d65/gateway/internal/handler/route/handler.go#L30-L166
[client-factory]: https://github.com/telekom/controlplane/blob/6c33e47adefa88ae4da0b7d5e701752f96020d65/gateway/pkg/kongutil/client_factory.go#L83-L99
[kong-build]: https://github.com/telekom/controlplane/blob/6c33e47adefa88ae4da0b7d5e701752f96020d65/gateway/internal/features/builder.go#L270-L326
[client-metrics]: https://github.com/kubernetes-sigs/controller-runtime/blob/v0.25.2/pkg/metrics/client_go_adapter.go
[security-wiki]: https://wiki.telekom.de/spaces/DHEI/pages/4600044528/Cross-cluster+Configuration
[kgateway-status]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/pkg/reports/status.go#L518-L622
[kgateway-sync]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/pkg/kgateway/proxy_syncer/proxy_syncer.go#L443-L475
[route-status]: https://github.com/kubernetes-sigs/gateway-api/blob/v1.5.1/apis/v1/shared_types.go#L327-L507
[policy-status]: https://github.com/kgateway-dev/kgateway/blob/v2.3.0/pkg/reports/policy.go#L106-L197
[rover-submit]: https://github.com/telekom/controlplane/blob/6c33e47adefa88ae4da0b7d5e701752f96020d65/rover-ctl/pkg/handlers/common/base_handler.go#L155-L182
[rover-api]: https://github.com/telekom/controlplane/blob/6c33e47adefa88ae4da0b7d5e701752f96020d65/rover-server/internal/server/rover_server.go#L45-L65

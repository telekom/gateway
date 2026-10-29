# Route configuration flow

These diagrams support [ADR 0004](../decisions/0004-use-an-oauth-proxy-for-cross-cluster-configuration.md) and
[ADR 0005](../decisions/0005-reuse-an-existing-kubernetes-api-proxy.md). They show a proposed design. Nobody has
implemented the new gateway integration yet.

The distributed control plane (dCP) owns the desired route configuration. It reaches each data-plane cluster only
through an OAuth-protected Kubernetes API proxy. The proxy forwards requests with a local ServiceAccount.

## Observe the target cluster

We compare two options. Both options use the same proxy and the same target Kubernetes API.

```mermaid
sequenceDiagram
  box rgba(46,90,168,0.15) Control-plane cluster
    participant dcp as dCP
  end
  box rgba(191,69,32,0.15) Data-plane cluster
    participant proxy as OAuth API proxy
    participant k8s as Target Kubernetes API
  end

  alt Option A, investigate first. List/watch with slow inventory audits.
    dcp->>proxy: LIST, then WATCH
    proxy->>k8s: LIST, then WATCH with the local ServiceAccount
    k8s--)proxy: Initial state, then a stream of changed objects
    proxy--)dcp: The same stream, in the response to the dCP request
    Note over dcp: Slow inventory audits find missed changes and orphans.
  else Option B. Frequent inventory audits only.
    loop On a timer
      dcp->>proxy: LIST the managed resources
      proxy->>k8s: LIST with the local ServiceAccount
      k8s-->>proxy: Spec and status
      proxy-->>dcp: Spec and status
    end
  end
```

The dCP starts every request. No separate watcher or notification service runs in the data plane. For details, see
[Prefer a central watch over a separate notification service](../decisions/0004-use-an-oauth-proxy-for-cross-cluster-configuration.md#prefer-a-central-watch-over-a-separate-notification-service).

## Apply a route configuration

```mermaid
sequenceDiagram
  participant cli as roverctl
  box rgba(46,90,168,0.15) Control-plane cluster
    participant dcp as dCP with the gateway adapter (proposed)
  end
  box rgba(191,69,32,0.15) Data-plane cluster
    participant dp as OAuth API proxy and Kubernetes API
  end

  cli->>dcp: Submit rover.yaml
  dcp-->>cli: 202 Accepted
  Note over cli,dcp: The dCP stored the source.<br/>The route is not ready yet.
  Note over dcp: Derive the route and render the desired resources.
  dcp->>dp: Read the actual state
  dp-->>dcp: Spec and status
  Note over dcp: Compare the states and plan a safe write order.
  loop For each resource or safe stage
    dcp->>dp: Apply with dryRun=All
    dp-->>dcp: Preflight result
    dcp->>dp: Apply
    dp-->>dcp: Persisted
  end
  dp--)dcp: Resource conditions from kgateway
  Note over dcp: Evaluate configuration acceptance.
```

The dCP translates the configuration one time and owns the write order. Kubernetes validates each request with RBAC,
schemas and admission. A persisted resource is not ready yet. The dCP observes the kgateway conditions to decide
configuration acceptance. The meaning of the public `Ready` condition is still open. For details, see
[Observe configuration acceptance separately from serving](../decisions/0004-use-an-oauth-proxy-for-cross-cluster-configuration.md#observe-configuration-acceptance-separately-from-serving).

### Request path through the proxy

Each arrow from the dCP in the diagram above is an ordinary Kubernetes API call. This diagram shows the path of one call
through the data-plane cluster, in the composition with an authentication sidecar in the adapter pod. kgateway routes
the call. The sidecar only checks the request. It does not route.

```mermaid
sequenceDiagram
  box rgba(46,90,168,0.15) Control-plane cluster
    participant dcp as dCP (controller-runtime client)
  end
  box rgba(191,69,32,0.15) Data-plane cluster
    participant gw as kgateway management route
    participant sc as Auth sidecar (adapter pod)
    participant kp as kubectl proxy (adapter pod)
    participant api as kube-apiserver
    participant ctl as kgateway controller
  end

  Note over dcp,api: Read: GET /apis/gateway.networking.k8s.io/v1/namespaces/{ns}/httproutes?labelSelector=…
  Note over dcp,api: Preflight and apply: PATCH …/trafficpolicies/{name}?fieldManager=dcp&dryRun=All
  Note over dcp,api: The PATCH requests use Content-Type: application/apply-patch+yaml.
  Note over dcp,api: Observe: GET …?watch=true&resourceVersion=…<br/>Clean up: DELETE …
  dcp->>gw: HTTPS request with the Iris access token
  Note over gw: Validate the Iris JWT. Check the client.<br/>Mint the Gateway Token (LMS).<br/>Route to the adapter Service.
  gw->>sc: Forward the request over mTLS
  Note over sc: Validate the Gateway Token.<br/>Remove the Authorization header.<br/>Set the Host to localhost.
  sc->>kp: Forward to the loopback address
  Note over kp: Add the ServiceAccount token.
  kp->>api: Request as the proxy ServiceAccount
  Note over api: RBAC, schema validation and admission.<br/>Persist the object in etcd.
  api-->>dcp: Response or watch stream, back through the same hops
  api--)ctl: Watch event for the changed resource
  Note over ctl: Translate the resources.<br/>Push the configuration to Envoy over xDS.
```

A watch is one long HTTP response. Every hop must stream it without buffering and must allow its duration. See
[Connect the dCP through the proxy](../decisions/0005-reuse-an-existing-kubernetes-api-proxy.md#connect-the-dcp-through-the-proxy).

ADR 0005 has not selected a proxy product yet.

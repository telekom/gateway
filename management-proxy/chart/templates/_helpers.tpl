{{/*
SPDX-FileCopyrightText: 2026 Deutsche Telekom AG

SPDX-License-Identifier: Apache-2.0
*/}}

{{/* Names, labels and shared formatting */}}

{{/* Chart name, or nameOverride. Value of the app.kubernetes.io/name label. */}}
{{- define "management-proxy.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Name prefix of all resources: the release name and the chart name, or fullnameOverride. */}}
{{- define "management-proxy.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/* Labels that select the pods. Do not change them: Deployment selectors are immutable. */}}
{{- define "management-proxy.selectorLabels" -}}
app.kubernetes.io/name: {{ include "management-proxy.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/* Common labels of all resources. */}}
{{- define "management-proxy.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "management-proxy.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: management-proxy
{{- end }}

{{/* Image reference from an image value. The tag includes the digest. Expects envoy.image or kubectlProxy.image. */}}
{{- define "management-proxy.image" -}}
{{- printf "%s:%s" .repository .tag }}
{{- end }}

{{/* Fails when the list is empty. `required` accepts an empty list. Expects the keys value and message. */}}
{{- define "management-proxy.requiredList" -}}
{{- if not .value }}
{{- fail .message }}
{{- end }}
{{- toJson .value }}
{{- end }}

{{/* Shared request settings */}}

{{/* Validated pathPrefix: starts with a slash and does not end with one. */}}
{{- define "management-proxy.pathPrefix" -}}
{{- $prefix := required "pathPrefix is required" .Values.pathPrefix }}
{{- if or (not (hasPrefix "/" $prefix)) (hasSuffix "/" $prefix) }}
{{- fail "pathPrefix must start with a slash and must not end with a slash" }}
{{- end }}
{{- $prefix }}
{{- end }}

{{/* allowedClients as a JSON list. Fails when it is empty. */}}
{{- define "management-proxy.allowedClients" -}}
{{- include "management-proxy.requiredList" (dict
      "value" .Values.allowedClients
      "message" "allowedClients needs at least one client ID"
    ) }}
{{- end }}

{{/* TLS certificate names and identities */}}

{{/* Secret with the server certificate of the sidecar. Default with cert-manager: <fullname>-server. */}}
{{- define "management-proxy.serverSecretName" -}}
{{- if .Values.tls.serverCertificate.secretName }}
{{- .Values.tls.serverCertificate.secretName }}
{{- else if .Values.tls.certManager.enabled }}
{{- printf "%s-server" (include "management-proxy.fullname" .) }}
{{- else }}
{{- fail "tls.serverCertificate.secretName is required when tls.certManager.enabled is false" }}
{{- end }}
{{- end }}

{{/* Secret with the ca.crt that kgateway uses to verify the sidecar without mutual TLS. Default: server Secret. */}}
{{- define "management-proxy.serverCASecretName" -}}
{{- default (include "management-proxy.serverSecretName" .) .Values.tls.serverCertificate.caSecretName }}
{{- end }}

{{/* Secret with the client certificate of kgateway for mutual TLS. Default with cert-manager: <fullname>-client. */}}
{{- define "management-proxy.clientSecretName" -}}
{{- if .Values.tls.clientCertificate.secretName }}
{{- .Values.tls.clientCertificate.secretName }}
{{- else if .Values.tls.certManager.enabled }}
{{- printf "%s-client" (include "management-proxy.fullname" .) }}
{{- else }}
{{- fail "tls.clientCertificate.secretName is required when mutual TLS and the route are enabled and tls.certManager.enabled is false" }}
{{- end }}
{{- end }}

{{/* Secret with the ca.crt that the sidecar uses to verify kgateway with mutual TLS. Default: the server Secret. */}}
{{- define "management-proxy.clientCASecretName" -}}
{{- default (include "management-proxy.serverSecretName" .) .Values.tls.clientCertificate.caSecretName }}
{{- end }}

{{/* DNS SAN of the client certificate of kgateway. The sidecar accepts only this name. */}}
{{- define "management-proxy.clientDNSName" -}}
{{- default
      (printf "%s-client.%s" (include "management-proxy.fullname" .) .Release.Namespace)
      .Values.tls.clientCertificate.dnsName }}
{{- end }}

{{/* DNS names of the Service as a JSON list, for the server certificate. The route uses the first name for SNI. */}}
{{- define "management-proxy.serviceDNSNames" -}}
{{- $svc := include "management-proxy.fullname" . }}
{{- $ns := .Release.Namespace }}
{{- list
      (printf "%s.%s.svc" $svc $ns)
      (printf "%s.%s.svc.%s" $svc $ns .Values.clusterDomain)
      (printf "%s.%s" $svc $ns)
      $svc | toJson }}
{{- end }}

{{/* Route authentication and extension references */}}

{{/* CEL expressions as a JSON list. Sets defaults, validates clients and builds one expression per issuer. */}}
{{- define "management-proxy.routeMatchExpressions" -}}
{{- $allowed := include "management-proxy.allowedClients" . | fromJsonArray }}
{{- $payload := "metadata.filter_metadata['envoy.filters.http.jwt_authn']['payload']" }}
{{- $expressions := list }}
{{- $entries := include "management-proxy.requiredList" (dict
      "value" .Values.route.issuers
      "message" "route.issuers is required when the route is enabled"
    ) | fromJsonArray }}
{{- range $entries }}
{{- $clients := default $allowed .clients }}
{{- range $clients }}
{{- if not (has . $allowed) }}
{{- fail (printf "route.issuers: the client %s is not in allowedClients" .) }}
{{- end }}
{{- end }}
{{- $issuer := required "route.issuers[].issuer is required" .issuer }}
{{- $clientClaim := default "clientId" .clientClaim }}
{{- $expressions = append $expressions (printf
      "%s['iss'] == %s && %s[%s] in %s"
      $payload (toJson $issuer) $payload (toJson $clientClaim) (toJson $clients)
    ) }}
{{- end }}
{{- toJson $expressions }}
{{- end }}

{{/* Reference to a GatewayExtension. Expects a dict with the keys ref and field. */}}
{{- define "management-proxy.extensionRef" -}}
name: {{ required (printf "%s.name is required when the route is enabled" .field) .ref.name }}
{{- with .ref.namespace }}
namespace: {{ . }}
{{- end }}
{{- end }}

{{/* JWKS endpoint and Envoy request binding */}}

{{/*
Validated JWKS endpoint of the gateway as a JSON dict with the keys uri, host, address, port and caFile. Envoy connects
to address and port, and checks the certificate against host and the CA bundle in caFile. address and port default to
the host and port of the URI. caFile is the mounted caConfigMap, or the system CA bundle of the Envoy image.
*/}}
{{- define "management-proxy.jwks" -}}
{{- $uri := required "envoy.gatewayToken.jwks.uri is required" .Values.envoy.gatewayToken.jwks.uri }}
{{- $u := urlParse $uri }}
{{- if ne $u.scheme "https" }}
{{- fail "envoy.gatewayToken.jwks.uri must be an https URL" }}
{{- end }}
{{- $hostPort := splitList ":" $u.host }}
{{- $host := first $hostPort }}
{{- $port := 443 }}
{{- if gt (len $hostPort) 1 }}
{{- $port = last $hostPort | int }}
{{- end }}
{{- $address := default $host .Values.envoy.gatewayToken.jwks.address }}
{{- if .Values.envoy.gatewayToken.jwks.port }}
{{- $port = .Values.envoy.gatewayToken.jwks.port | int }}
{{- end }}
{{- $caFile := ternary
      "/etc/management-proxy/jwks-ca/ca.crt"
      "/etc/ssl/certs/ca-certificates.crt"
      (not (empty .Values.envoy.gatewayToken.jwks.caConfigMap)) }}
{{- dict "uri" $uri "host" $host "address" $address "port" $port "caFile" $caFile | toJson }}
{{- end }}

{{/*
CEL expression in parsed form (google.api.expr.v1alpha1.Expr) that reads one claim of the Gateway Token:
metadata.filter_metadata['envoy.filters.http.jwt_authn']['gateway_token'][<claim>]
Envoy v1.40 accepts only parsed CEL expressions in the RBAC filter.
*/}}
{{- define "management-proxy.celClaim" -}}
{
  "call_expr": {
    "function": "_[_]",
    "args": [
      {
        "call_expr": {
          "function": "_[_]",
          "args": [
            {
              "call_expr": {
                "function": "_[_]",
                "args": [
                  {
                    "select_expr": {
                      "operand": {"ident_expr": {"name": "metadata"}},
                      "field": "filter_metadata"
                    }
                  },
                  {"const_expr": {"string_value": "envoy.filters.http.jwt_authn"}}
                ]
              }
            },
            {"const_expr": {"string_value": "gateway_token"}}
          ]
        }
      },
      {"const_expr": {"string_value": {{ . | toJson }}}}
    ]
  }
}
{{- end }}

{{/*
request.method == claim('operation') && '<pathPrefix>' + request.url_path == claim('requestPath')
A missing claim is an evaluation error, and the policy does not match.
*/}}
{{- define "management-proxy.requestBindingCondition" -}}
{
  "call_expr": {
    "function": "_&&_",
    "args": [
      {
        "call_expr": {
          "function": "_==_",
          "args": [
            {"select_expr": {"operand": {"ident_expr": {"name": "request"}}, "field": "method"}},
            {{ include "management-proxy.celClaim" "operation" | nindent 12 }}
          ]
        }
      },
      {
        "call_expr": {
          "function": "_==_",
          "args": [
            {
              "call_expr": {
                "function": "_+_",
                "args": [
                  {"const_expr": {"string_value": {{ include "management-proxy.pathPrefix" . | toJson }}}},
                  {"select_expr": {"operand": {"ident_expr": {"name": "request"}}, "field": "url_path"}}
                ]
              }
            },
            {{ include "management-proxy.celClaim" "requestPath" | nindent 12 }}
          ]
        }
      }
    ]
  }
}
{{- end }}

{{/* Kubernetes forwarding and permissions */}}

{{/*
Regex for --accept-paths of kubectl proxy: discovery, OpenAPI and the configured API groups.
kubectl proxy rejects all other paths with 403.
*/}}
{{- define "management-proxy.acceptPaths" -}}
{{- $groups := list }}
{{- $apiGroups := include "management-proxy.requiredList" (dict
      "value" .Values.kubectlProxy.apiGroups
      "message" "kubectlProxy.apiGroups needs at least one API group"
    ) | fromJsonArray }}
{{- range $apiGroups }}
{{- $groups = append $groups (regexQuoteMeta .) }}
{{- end }}
{{- printf "^/(version|api|api/v1|apis|openapi/.*|apis/(%s)(/.*)?)$" (join "|" $groups) }}
{{- end }}

{{/*
rbac.namespaces as a JSON list. Fails for an empty list or a protected namespace.
Protected namespaces hold the proxy or, with the route enabled, its parent Gateways and GatewayExtensions.
A Role there would let the dCP change the management route or the policies of its Gateway.
*/}}
{{- define "management-proxy.rbacNamespaces" -}}
{{- $protected := list .Release.Namespace }}
{{- if .Values.route.enabled }}
{{- range .Values.route.parentRefs }}
{{- $protected = append $protected (default $.Release.Namespace .namespace) }}
{{- end }}
{{- $protected = append $protected (default .Release.Namespace .Values.route.jwtExtensionRef.namespace) }}
{{- $protected = append $protected (default .Release.Namespace .Values.route.extProcExtensionRef.namespace) }}
{{- end }}
{{- $namespaces := include "management-proxy.requiredList" (dict
      "value" .Values.rbac.namespaces
      "message" "rbac.namespaces needs at least one namespace"
    ) | fromJsonArray }}
{{- range $namespaces }}
{{- if has . $protected }}
{{- fail (printf
      "rbac.namespaces must not contain %s: it holds the management proxy, the management route, its Gateway or its GatewayExtensions"
      .
    ) }}
{{- end }}
{{- end }}
{{- toJson $namespaces }}
{{- end }}

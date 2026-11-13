#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
#
# SPDX-License-Identifier: Apache-2.0

# Checks the rendered chart with kubeconform against exact versions of Kubernetes and of the CRDs that the chart uses.
#
# kubeconform validates only against JSON schemas. It has the schemas of Kubernetes itself, but the Gateway API,
# kgateway and cert-manager publish their CRDs only as YAML. So this script downloads the CRDs of each release and
# converts them to JSON schemas first.
#
# Usage: validate-schemas.sh FILE|DIRECTORY...
# Needs kubeconform, helm, yq, curl and internet access.
set -uo pipefail

# One line for each environment: the Kubernetes, Gateway API, kgateway and cert-manager versions. The lines are the
# minimum versions of the chart (see Chart.yaml), the versions in use, and the next planned versions. Update them when
# the clusters change.
# "KUBERNETES GATEWAY_API KGATEWAY CERT_MANAGER"
ENVIRONMENTS=(
  "1.30.0 v1.5.1 v2.3.0 v1.21.1"
  "1.36.0 v1.5.1 v2.3.0 v1.21.1"
  "1.37.0 v1.7.0 v2.5.1 v1.21.3"
)

# A temporary directory for the downloads and schemas. The trap deletes it when the script ends.
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# Prints the CRD manifests of one release.
download() { # project version
  case $1 in
    gateway-api)
      curl -fsSL "https://github.com/kubernetes-sigs/gateway-api/releases/download/$2/standard-install.yaml" ;;
    kgateway)
      # kgateway publishes its CRDs as a Helm chart.
      helm template crds oci://cr.kgateway.dev/kgateway-dev/charts/kgateway-crds --version "$2" 2>/dev/null ;;
    cert-manager)
      curl -fsSL "https://github.com/cert-manager/cert-manager/releases/download/$2/cert-manager.crds.yaml" ;;
  esac
}

# Converts each CRD version in the manifest file to a JSON schema in <dir>/<group>/<kind>_<version>.json. kubeconform
# finds the schemas by this path. This is the same conversion as kubeconform's openapi2jsonschema.py.
convert() { # manifest dir
  local path schema
  # yq prints one line for each CRD version: the file path, a tab, and the schema as JSON. The schema gets
  # "additionalProperties": false on each object that lists its properties, so that kubeconform -strict reports unknown
  # fields. Objects that Kubernetes marks to keep unknown fields stay open.
  # shellcheck disable=SC2016 # $crd is a yq variable, not a shell variable.
  yq -N '
    select(.kind == "CustomResourceDefinition") | .spec as $crd | $crd.versions[] |
    ($crd.group + "/" + ($crd.names.kind | downcase) + "_" + .name) + "\t" + (.schema.openAPIV3Schema |
      (.properties | .. | select(tag == "!!map" and has("properties") and (has("additionalProperties") | not) and
        .["x-kubernetes-preserve-unknown-fields"] != true)) |= . + {"additionalProperties": false} | @json)
  ' "$1" | while IFS=$'\t' read -r path schema; do
    mkdir -p "$2/$(dirname "$path")"
    printf '%s\n' "$schema" >"$2/$path.json"
  done
}

status=0
for environment in "${ENVIRONMENTS[@]}"; do
  read -r kubernetes gateway_api kgateway cert_manager <<<"$environment"

  # kubeconform looks for a schema in each location in this order. "default" holds the Kubernetes schemas.
  locations=(-schema-location default)
  for release in "gateway-api $gateway_api" "kgateway $kgateway" "cert-manager $cert_manager"; do
    read -r project version <<<"$release"
    dir="$work/$project-$version"
    # Environments can share a release. Download and convert each release only once.
    if [ ! -d "$dir" ]; then
      if ! download "$project" "$version" >"$dir.yaml" || ! convert "$dir.yaml" "$dir" || [ ! -d "$dir" ]; then
        echo "FAIL  cannot get the CRDs of $project $version"
        exit 1
      fi
    fi
    # kubeconform fills in the {{...}} fields from each resource, for example gateway.kgateway.dev, trafficpolicy and
    # v1alpha1.
    locations+=(-schema-location "$dir/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json")
  done

  printf 'Kubernetes %s, Gateway API %s, kgateway %s, cert-manager %s: ' "$kubernetes" "$gateway_api" "$kgateway" \
    "$cert_manager"
  # Check all environments, and fail at the end if one of them failed.
  kubeconform -strict -summary -kubernetes-version "$kubernetes" "${locations[@]}" "$@" || status=1
done
exit "$status"

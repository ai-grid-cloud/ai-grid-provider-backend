#!/usr/bin/env bash
# One-time migration for a catalog repository the backend wrote before it
# gave each MaaSSubscription the Argo CD sync option Delete=false: adds that
# option to every MaaSSubscription in catalog/subscriptions/*.yaml, as the
# backend now writes it (see "Git catalog" in README.md). It keeps any other
# sync option, once each, and replaces any other Delete option. It leaves the
# MaaSAuthPolicy documents, the ai-grid/version annotation and every other
# file as they are. Running it again changes nothing.
#
#   hack/add-subscription-delete-false.sh <catalog checkout>
#
# Then review `git diff` in the checkout, commit and push, and wait until
# every ingress cluster's acm-models-catalog-subscriptions-<cluster>
# Application is Synced at that commit before removing any cluster from the
# grid: Argo CD reads Delete=false from the object on the cluster, so a
# cluster removed before its copy has the option still revokes the keys.
#
# Needs bash and a mikefarah yq v4 with --yaml-compact-seq-indent (tested
# with v4.54.1), which keeps the backend's list indentation, so the diff is
# the annotation only.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <catalog checkout>" >&2
  exit 2
fi
dir="$1/catalog/subscriptions"
if [[ ! -d "$dir" ]]; then
  echo "$0: no $dir" >&2
  exit 1
fi

# Same result as withArgoDeleteFalse in internal/servers/subscriptions_git.go.
expr='with(select(.kind == "MaaSSubscription");
  .metadata.annotations["argocd.argoproj.io/sync-options"] |= (
    ((. // "") | split(",")
      | map(sub("^\s+", "") | sub("\s+$", ""))
      | map(select(. != "" and (test("^Delete=") | not)))
      | unique) + ["Delete=false"] | join(",")))'

shopt -s nullglob
changed=0
for f in "$dir"/*.yaml; do
  before="$(cat "$f")"
  yq -i --yaml-compact-seq-indent "$expr" "$f"
  if [[ "$(cat "$f")" != "$before" ]]; then
    echo "updated $f"
    changed=$((changed + 1))
  fi
done
echo "$changed file(s) updated"

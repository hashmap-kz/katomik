#!/bin/bash
set -euo pipefail

kubectl apply -f manifests/
kubectl -n pgrwl-test rollout restart sts postgres
kubectl -n pgrwl-test rollout restart sts prometheus
kubectl -n pgrwl-test rollout restart deploy grafana

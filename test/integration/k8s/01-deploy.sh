#!/bin/bash
set -euo pipefail

kubectl apply -f manifests/
kubectl -n katomik-test rollout restart sts postgres
kubectl -n katomik-test rollout restart sts prometheus
kubectl -n katomik-test rollout restart deploy grafana

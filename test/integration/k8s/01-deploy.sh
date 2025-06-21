#!/bin/bash
set -euo pipefail

kubectl apply -f manifests/
kubectl -n pgrwl-test rollout restart sts postgres
kubectl -n pgrwl-test rollout restart sts prometheus
kubectl -n pgrwl-test rollout restart sts loki
kubectl -n pgrwl-test rollout restart ds promtail
kubectl -n pgrwl-test rollout restart ds node-exporter
kubectl -n pgrwl-test rollout restart deploy grafana
kubectl -n pgrwl-test rollout restart deploy kube-state-metrics

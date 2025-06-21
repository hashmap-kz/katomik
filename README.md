# kubectl-atomic_apply

`kubectl-atomic_apply` is a kubectl plugin that applies multiple Kubernetes manifests with **all-or-nothing**
guarantees. Unlike `kubectl apply -f`, it ensures transactional behavior: if any resource fails to apply or reach a
ready state, all previously applied resources are rolled back automatically.

---

## Features

* **Atomic behavior**: Applies multiple manifests as a unit. If anything fails, restores the original state.
* **Server-Side Apply** (SSA): Uses `PATCH` with SSA to minimize conflicts and preserve intent.
* **Status tracking**: Waits for all resources to become `Current` (Ready/Available) before succeeding.
* **Rollback support**: Automatically restores previous state if apply or wait fails.
* **Recursive**: Like `kubectl`, supports directories and `-R` for recursive traversal.
* **STDIN support**: Use `-f -` to read from `stdin`.

---

## Installation

### Manual Installation

1. Download the latest binary for your platform from
   the [Releases page](https://github.com/hashmap-kz/kubectl-atomic_apply/releases).
2. Place the binary in your system's `PATH` (e.g., `/usr/local/bin`).

### Installation script

```bash
(
set -euo pipefail

OS="$(uname | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m | sed -e 's/x86_64/amd64/' -e 's/\(arm\)\(64\)\?.*/\1\2/' -e 's/aarch64$/arm64/')"
TAG="$(curl -s https://api.github.com/repos/hashmap-kz/kubectl-atomic_apply/releases/latest | jq -r .tag_name)"

curl -L "https://github.com/hashmap-kz/kubectl-atomic_apply/releases/download/${TAG}/kubectl-atomic_apply_${TAG}_${OS}_${ARCH}.tar.gz" |
tar -xzf - -C /usr/local/bin && \
chmod +x /usr/local/bin/kubectl-atomic_apply
)
```

### Homebrew installation

```bash
brew tap hashmap-kz/homebrew-tap
brew install kubectl-atomic_apply
```

---

## Usage

```bash
# Apply multiple files atomically
kubectl atomic-apply -f manifests/

# Read from stdin
kubectl atomic-apply -f - < all.yaml

# Apply recursively
kubectl atomic-apply -R -f ./deploy/

# Set a custom timeout (default: 5m)
kubectl atomic-apply --timeout 2m -f ./manifests/

# Process and apply a manifest located on a remote server
kubectl atomic-apply \
  -f https://raw.githubusercontent.com/user/repo/refs/heads/master/manifests/deployment.yaml
```

---

## Example Output

```
✓ Init clients
✓ Decoding manifests
✓ Preparing apply plan
✓ Applying manifests
✓ Waiting
⏳ Waiting for resources:
 - namespace/pgrwl-test in (cluster)
 - configmap/postgresql-init-script in pgrwl-test
 - configmap/postgresql-envs in pgrwl-test
 - configmap/postgresql-conf in pgrwl-test
 - service/postgres in pgrwl-test
 - persistentvolumeclaim/postgres-data in pgrwl-test
 - statefulset/postgres in pgrwl-test
 - configmap/prometheus-config in pgrwl-test
 - persistentvolumeclaim/prometheus-data in pgrwl-test
 - service/prometheus in pgrwl-test
 - statefulset/prometheus in pgrwl-test
 - persistentvolumeclaim/grafana-data in pgrwl-test
 - service/grafana in pgrwl-test
 - configmap/grafana-datasources in pgrwl-test
 - deployment/grafana in pgrwl-test
 - serviceaccount/metrics-server in kube-system
 - clusterrole/system:aggregated-metrics-reader in (cluster)
 - clusterrole/system:metrics-server in (cluster)
 - rolebinding/metrics-server-auth-reader in kube-system
 - clusterrolebinding/metrics-server:system:auth-delegator in (cluster)
 - clusterrolebinding/system:metrics-server in (cluster)
 - service/metrics-server in kube-system
 - deployment/metrics-server in kube-system
 - apiservice/v1beta1.metrics.k8s.io in (cluster)
[watch] waiting: service/grafana in pgrwl-test -> actualStatus=Unknown expectedStatus=Current
[watch] waiting: deployment/grafana in pgrwl-test -> actualStatus=Unknown expectedStatus=Current
[watch] waiting: service/metrics-server in kube-system -> actualStatus=Unknown expectedStatus=Current
[watch] waiting: deployment/metrics-server in kube-system -> actualStatus=Unknown expectedStatus=Current
[watch] waiting: apiservice/v1beta1.metrics.k8s.io in (cluster) -> actualStatus=Unknown expectedStatus=Current
✓ Success
```

---

## Quick Start

```
cd test/integration/k8s
bash 00-setup-kind.sh
kubectl atomic-apply -f manifests/
```

---

## 🔒 Rollback Guarantees

On failure (bad manifest, missing dependency, timeout, etc.):

* Existing objects are reverted to their exact pre-apply state.
* New objects are deleted.

This guarantees your cluster remains consistent - no partial updates.

---

## Flags

| Flag        | Description                       |
|-------------|-----------------------------------|
| `-f`        | File, directory, or `-` for stdin |
| `-R`        | Recurse into directories          |
| `--timeout` | Timeout to wait for readiness     |

---

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

---

## Feedback

Have a feature request or issue? Feel free to [open an issue](https://github.com/hashmap-kz/kubectl-atomic_apply/issues)
or submit a PR!

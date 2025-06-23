//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestManifestWithExplicitNamespace(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	ns := "explicit-ns"
	_ = exec.Command("kubectl", "create", "ns", ns).Run()

	manifest := strings.ReplaceAll(baseDeployment, "test-nginx", "explicit-ns-nginx")
	manifest = strings.ReplaceAll(manifest, "name: explicit-ns-nginx", "name: explicit-ns-nginx\n  namespace: "+ns)

	path := filepath.Join(tmp, "ns.yaml")
	_ = os.WriteFile(path, []byte(manifest), 0o644)

	_, err := exec.Command("katomik", "apply", "-f", path).CombinedOutput()
	assert.NoError(t, err)

	client := kubeClient(t)
	_, err = client.AppsV1().Deployments(ns).Get(ctx, "explicit-ns-nginx", metav1.GetOptions{})
	assert.NoError(t, err)
}

func TestFallbackNamespaceFlag(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	ns := "fallback-ns"
	_ = exec.Command("kubectl", "create", "ns", ns).Run()

	manifest := strings.ReplaceAll(baseDeployment, "test-nginx", "fallback-nginx")
	path := filepath.Join(tmp, "fallback.yaml")
	_ = os.WriteFile(path, []byte(manifest), 0o644)

	_, err := exec.Command("katomik", "apply", "-f", path, "--namespace", ns).CombinedOutput()
	assert.NoError(t, err)

	client := kubeClient(t)
	_, err = client.AppsV1().Deployments(ns).Get(ctx, "fallback-nginx", metav1.GetOptions{})
	assert.NoError(t, err)
}

func TestMixedScopeResources(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	// ClusterRole is cluster-scoped, ConfigMap is namespaced
	mixed := `
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: test-global-role
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["list"]
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-ns-config
data:
  foo: bar
`

	path := filepath.Join(tmp, "mixed.yaml")
	_ = os.WriteFile(path, []byte(mixed), 0o644)

	_, err := exec.Command("katomik", "apply", "-f", path).CombinedOutput()
	assert.NoError(t, err)

	client := kubeClient(t)

	_, err = client.RbacV1().ClusterRoles().Get(ctx, "test-global-role", metav1.GetOptions{})
	assert.NoError(t, err)

	_, err = client.CoreV1().ConfigMaps("default").Get(ctx, "test-ns-config", metav1.GetOptions{})
	assert.NoError(t, err)
}

func TestNamespaceFlagDoesNotOverrideMetadata(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	_ = exec.Command("kubectl", "create", "ns", "manifest-ns").Run()
	_ = exec.Command("kubectl", "create", "ns", "flag-ns").Run()

	manifest := strings.ReplaceAll(baseDeployment, "test-nginx", "ns-override")
	manifest = strings.ReplaceAll(manifest, "name: ns-override", "name: ns-override\n  namespace: manifest-ns")

	path := filepath.Join(tmp, "override.yaml")
	_ = os.WriteFile(path, []byte(manifest), 0o644)

	_, err := exec.Command("katomik", "apply", "-f", path, "--namespace", "flag-ns").CombinedOutput()
	assert.NoError(t, err)

	client := kubeClient(t)

	_, err = client.AppsV1().Deployments("manifest-ns").Get(ctx, "ns-override", metav1.GetOptions{})
	assert.NoError(t, err)

	_, err = client.AppsV1().Deployments("flag-ns").Get(ctx, "ns-override", metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "should not apply to fallback namespace")
}

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const baseDeployment = `---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-nginx
spec:
  replicas: 1
  selector:
    matchLabels:
      app: test-nginx
  template:
    metadata:
      labels:
        app: test-nginx
    spec:
      containers:
      - name: nginx
        image: nginx:latest
`

func TestApplyUpdateOverExistingResources(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	initial := strings.ReplaceAll(baseDeployment, "nginx:latest", "nginx:1.21")
	updated := strings.ReplaceAll(baseDeployment, "nginx:latest", "nginx:1.25")

	initialPath := filepath.Join(tmp, "initial.yaml")
	updatedPath := filepath.Join(tmp, "updated.yaml")
	_ = os.WriteFile(initialPath, []byte(initial), 0644)
	_ = os.WriteFile(updatedPath, []byte(updated), 0644)

	_, _ = exec.Command("katomik", "apply", "-f", initialPath).CombinedOutput()
	_, _ = exec.Command("katomik", "apply", "-f", updatedPath).CombinedOutput()

	cfg, _ := kubeConfig()
	client, _ := kubernetes.NewForConfig(cfg)

	deploy, err := client.AppsV1().Deployments("default").Get(ctx, "test-nginx", metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, "nginx:1.25", deploy.Spec.Template.Spec.Containers[0].Image)
}

func TestEmptyOrNoopManifest(t *testing.T) {
	tmp := t.TempDir()
	noopPath := filepath.Join(tmp, "noop.yaml")
	_ = os.WriteFile(noopPath, []byte("---"), 0644)

	out, err := exec.Command("katomik", "apply", "-f", noopPath).CombinedOutput()
	t.Logf("output:\n%s", string(out))
	assert.NoError(t, err)
	assert.Contains(t, string(out), "✓ no trackable resources")
}

func TestMultipleResourcesOfSameKind(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	multi := baseDeployment + "\n" + strings.ReplaceAll(baseDeployment, "test-nginx", "test-nginx-2")
	multiPath := filepath.Join(tmp, "multi.yaml")
	_ = os.WriteFile(multiPath, []byte(multi), 0644)

	_, err := exec.Command("katomik", "apply", "-f", multiPath).CombinedOutput()
	assert.NoError(t, err)

	cfg, _ := kubeConfig()
	client, _ := kubernetes.NewForConfig(cfg)

	_, err = client.AppsV1().Deployments("default").Get(ctx, "test-nginx", metav1.GetOptions{})
	assert.NoError(t, err)
	_, err = client.AppsV1().Deployments("default").Get(ctx, "test-nginx-2", metav1.GetOptions{})
	assert.NoError(t, err)
}

func TestRollbackAfterMixedApply(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	good := baseDeployment + "\n" + strings.ReplaceAll(baseDeployment, "test-nginx", "test-nginx-2")
	bad := strings.ReplaceAll(good, "nginx:latest", "nginx:nonexistent")

	goodPath := filepath.Join(tmp, "good.yaml")
	badPath := filepath.Join(tmp, "bad.yaml")
	_ = os.WriteFile(goodPath, []byte(good), 0644)
	_ = os.WriteFile(badPath, []byte(bad), 0644)

	_, _ = exec.Command("katomik", "apply", "-f", goodPath).CombinedOutput()
	out, err := exec.Command("katomik", "apply", "-f", badPath, "--timeout=10s").CombinedOutput()
	t.Logf("output:\n%s", string(out))
	assert.Error(t, err)

	cfg, _ := kubeConfig()
	client, _ := kubernetes.NewForConfig(cfg)

	for _, name := range []string{"test-nginx", "test-nginx-2"} {
		dep, err := client.AppsV1().Deployments("default").Get(ctx, name, metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, "nginx:latest", dep.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestRollbackHandlesDeletesCorrectly(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	good := baseDeployment + "\n" + strings.ReplaceAll(baseDeployment, "test-nginx", "test-nginx-2")
	bad := strings.ReplaceAll(baseDeployment, "nginx:latest", "nginx:nonexistent")

	goodPath := filepath.Join(tmp, "good.yaml")
	badPath := filepath.Join(tmp, "bad.yaml")
	_ = os.WriteFile(goodPath, []byte(good), 0644)
	_ = os.WriteFile(badPath, []byte(bad), 0644)

	_, _ = exec.Command("katomik", "apply", "-f", goodPath).CombinedOutput()
	_, err := exec.Command("katomik", "apply", "-f", badPath, "--timeout=10s").CombinedOutput()
	assert.Error(t, err)

	cfg, _ := kubeConfig()
	client, _ := kubernetes.NewForConfig(cfg)

	for _, name := range []string{"test-nginx", "test-nginx-2"} {
		_, err := client.AppsV1().Deployments("default").Get(ctx, name, metav1.GetOptions{})
		assert.NoError(t, err)
	}
}

func TestApplyWithCustomNamespace(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	ns := "apptomic-test"
	_ = exec.Command("kubectl", "create", "ns", ns).Run()

	deploy := strings.ReplaceAll(baseDeployment, "test-nginx", "custom-ns-nginx")
	deployPath := filepath.Join(tmp, "custom.yaml")
	_ = os.WriteFile(deployPath, []byte(deploy), 0644)

	_, err := exec.Command("katomik", "apply", "-f", deployPath, "--namespace", ns).CombinedOutput()
	assert.NoError(t, err)

	cfg, _ := kubeConfig()
	client, _ := kubernetes.NewForConfig(cfg)

	_, err = client.AppsV1().Deployments(ns).Get(ctx, "custom-ns-nginx", metav1.GetOptions{})
	assert.NoError(t, err)
}

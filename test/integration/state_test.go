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
)

const goodManifests = `---
apiVersion: v1
kind: Service
metadata:
  name: nginx
  labels:
    app: nginx
spec:
  type: NodePort
  selector:
    app: nginx
  ports:
    - protocol: TCP
      port: 80
      targetPort: 80
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx
  labels:
    app: nginx
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
        - name: nginx
          image: nginx:latest
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: nginx
  labels:
    app: nginx
data:
  TZ: "Asia/Aqtau"
`

func TestAtomicRollback(t *testing.T) {
	badManifests := strings.ReplaceAll(goodManifests, "nginx:latest", "nginx:nonexistent")

	ctx := context.Background()
	tmp := t.TempDir()

	// write manifests
	goodPath := filepath.Join(tmp, "good.yaml")
	badPath := filepath.Join(tmp, "bad.yaml")
	assert.NoError(t, os.WriteFile(goodPath, []byte(goodManifests), 0o644))
	assert.NoError(t, os.WriteFile(badPath, []byte(badManifests), 0o644))

	// apply good manifest
	t.Log("== applying good manifest ==")
	out1, err := exec.Command("katomik", "apply", "-f", goodPath).CombinedOutput()
	t.Logf("output:\n%s", string(out1))
	assert.NoError(t, err)

	// verify resources exist and are correct
	client := kubeClient(t)
	ns := "default"

	deploy, err := client.AppsV1().Deployments(ns).Get(ctx, "nginx", metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, "nginx:latest", deploy.Spec.Template.Spec.Containers[0].Image)

	svc, err := client.CoreV1().Services(ns).Get(ctx, "nginx", metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, int32(80), svc.Spec.Ports[0].Port)

	cm, err := client.CoreV1().ConfigMaps(ns).Get(ctx, "nginx", metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, "Asia/Aqtau", cm.Data["TZ"])

	// apply broken manifest (bad image)
	t.Log("== applying broken manifest (expect rollback) ==")
	out2, err := exec.Command("katomik", "apply", "-f", badPath, "--timeout=15s").CombinedOutput()
	t.Logf("output:\n%s", string(out2))
	assert.Error(t, err)

	// verify rollback: Deployment image should remain unchanged
	deploy2, err := client.AppsV1().Deployments(ns).Get(ctx, "nginx", metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, "nginx:latest", deploy2.Spec.Template.Spec.Containers[0].Image)

	// ensure Service and ConfigMap still exist
	_, err = client.CoreV1().Services(ns).Get(ctx, "nginx", metav1.GetOptions{})
	assert.NoError(t, err)

	_, err = client.CoreV1().ConfigMaps(ns).Get(ctx, "nginx", metav1.GetOptions{})
	assert.NoError(t, err)
}

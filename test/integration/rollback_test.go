//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const binPath = "bin/kubectl-atomic-apply" // compiled in advance

func kubeConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	return clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
}

func dynClient(t *testing.T) dynamic.Interface {
	cfg, err := kubeConfig()
	require.NoError(t, err)
	dc, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err)
	return dc
}

func cmResource(dyn dynamic.Interface) dynamic.NamespaceableResourceInterface {
	return dyn.Resource(schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "configmaps",
	})
}

func TestAtomicApply(t *testing.T) {
	tmp := t.TempDir()
	okFile := filepath.Join(tmp, "ok.yaml")
	badFile := filepath.Join(tmp, "bad.yaml")

	// 1. First apply — should succeed
	require.NoError(t, os.WriteFile(okFile, []byte(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: stateful
data:
  foo: bar
`), 0o644))

	cmd := exec.Command(binPath, "-f", okFile, "--timeout", "10s")
	out, err := cmd.CombinedOutput()
	t.Logf("first apply output:\n%s", string(out))
	require.NoError(t, err, "first apply must succeed")

	dyn := dynClient(t)
	cm, err := cmResource(dyn).Namespace("default").
		Get(context.TODO(), "stateful", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "bar", cm.Object["data"].(map[string]interface{})["foo"])

	initialRV := cm.GetResourceVersion()

	// 2. Second apply — should fail, trigger rollback
	require.NoError(t, os.WriteFile(badFile, []byte(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: stateful
data:
  foo: baz
---
apiVersion: v1
kind: ConfigMap
metadata:
  # Invalid DNS 1123 name → causes validation error
  name: INVALID_NAME
data:
  bad: data
`), 0o644))

	cmd = exec.Command(binPath, "-f", badFile, "--timeout", "10s")
	out, err = cmd.CombinedOutput()
	t.Logf("second apply output (expected failure):\n%s", string(out))
	require.Error(t, err, "second apply must fail to invoke rollback")

	// 3. Verify rollback
	cm, err = cmResource(dyn).Namespace("default").
		Get(context.TODO(), "stateful", metav1.GetOptions{})
	require.NoError(t, err)

	// data matches original
	require.Equal(t, "bar", cm.Object["data"].(map[string]interface{})["foo"],
		"ConfigMap data should have been rolled back")

	// resourceVersion *advanced* (write occurred) but object is otherwise identical
	require.Greater(t, cm.GetResourceVersion(), initialRV,
		"RV should bump after rollback update")

	// The invalid object never persisted
	_, err = cmResource(dyn).Namespace("default").
		Get(context.TODO(), "INVALID_NAME", metav1.GetOptions{})
	require.Error(t, err, "invalid ConfigMap must not exist after rollback")
}

func init() { time.Sleep(2 * time.Second) }

//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func TestApplySuccess(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "success.yaml")

	err := os.WriteFile(file, []byte(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-success
data:
  foo: bar
`), 0o644)
	assert.NoError(t, err)

	cmd := exec.Command("bin/kubectl-atomic_apply", "-f", file, "--timeout", "10s")
	out, err := cmd.CombinedOutput()
	t.Logf("Output:\n%s", string(out))
	assert.NoError(t, err)

	// Validate that object exists
	cfg, err := rest.InClusterConfig()
	if err != nil {
		cfg, err = clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		assert.NoError(t, err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	assert.NoError(t, err)

	u, err := dyn.Resource(schema.GroupVersionResource{
		Version:  "v1",
		Group:    "",
		Resource: "configmaps",
	}).Namespace("default").Get(context.TODO(), "test-success", metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, "test-success", u.GetName())
}

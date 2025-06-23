//go:build integration

package integration

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestApplyFromStdin(t *testing.T) {
	ctx := context.Background()

	manifest := strings.ReplaceAll(baseDeployment, "test-nginx", "stdin-nginx")

	cmd := exec.Command("katomik", "apply", "-f", "-")
	stdin, _ := cmd.StdinPipe()

	go func() {
		defer stdin.Close()
		io.WriteString(stdin, manifest)
	}()

	out, err := cmd.CombinedOutput()
	t.Logf("output:\n%s", string(out))
	assert.NoError(t, err)

	client := kubeClient(t)
	_, err = client.AppsV1().Deployments("default").Get(ctx, "stdin-nginx", metav1.GetOptions{})
	assert.NoError(t, err)
}

func TestApplyRecursiveDirectory(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	// Nested dir structure
	nestedDir := filepath.Join(tmp, "manifests", "subdir")
	_ = os.MkdirAll(nestedDir, 0o755)

	// Two files, one in root, one nested
	rootManifest := strings.ReplaceAll(baseDeployment, "test-nginx", "root-nginx")
	nestedManifest := strings.ReplaceAll(baseDeployment, "test-nginx", "nested-nginx")

	_ = os.WriteFile(filepath.Join(tmp, "manifests", "root.yaml"), []byte(rootManifest), 0o644)
	_ = os.WriteFile(filepath.Join(nestedDir, "nested.yaml"), []byte(nestedManifest), 0o644)

	cmd := exec.Command("katomik", "apply", "-f", filepath.Join(tmp, "manifests"), "-R")
	out, err := cmd.CombinedOutput()
	t.Logf("output:\n%s", string(out))
	assert.NoError(t, err)

	client := kubeClient(t)

	for _, name := range []string{"root-nginx", "nested-nginx"} {
		_, err := client.AppsV1().Deployments("default").Get(ctx, name, metav1.GetOptions{})
		assert.NoError(t, err)
	}
}

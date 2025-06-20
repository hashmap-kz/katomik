package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestReadManifests(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantErr  bool
		wantObjs int
	}{
		{
			name: "single valid manifest",
			input: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
  namespace: default
`,
			wantErr:  false,
			wantObjs: 1,
		},
		{
			name: "multiple manifests with separator",
			input: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: config-1
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: config-2
`,
			wantErr:  false,
			wantObjs: 2,
		},
		{
			name: "empty document ignored",
			input: `
---
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: config-final
`,
			wantErr:  false,
			wantObjs: 1,
		},
		{
			name: "invalid yaml document",
			input: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: broken
  namespace: default
  - oops
`,
			wantErr: true,
		},
		{
			name:     "completely empty input",
			input:    ``,
			wantErr:  false,
			wantObjs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs, err := readManifests([]byte(tt.input))

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantObjs, len(objs))
				for _, obj := range objs {
					assert.IsType(t, &unstructured.Unstructured{}, obj)
					assert.NotEmpty(t, obj.GetKind())
				}
			}
		})
	}
}

func TestStripMeta(t *testing.T) {
	obj := map[string]interface{}{
		"status": "something",
		"metadata": map[string]interface{}{
			"uid":               "123",
			"resourceVersion":   "1",
			"managedFields":     []string{"a"},
			"creationTimestamp": "2024-01-01",
			"name":              "test",
		},
	}
	stripMeta(obj)

	//nolint:errcheck
	meta := obj["metadata"].(map[string]interface{})
	assert.NotContains(t, obj, "status")
	assert.NotContains(t, meta, "uid")
	assert.NotContains(t, meta, "resourceVersion")
	assert.Contains(t, meta, "name")
}

package utils

import (
	"strings"
	"testing"
)

func TestReadObjects_DropsInvalid(t *testing.T) {
	testCases := []struct {
		name      string
		resources string
		expected  int
	}{
		{
			name: "valid resources",
			resources: `
---
apiVersion: v1
kind: Secret
metadata:
  name: test
  namespace: default
immutable: true
stringData:
  key: "private-key"
---
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: crossplane-provider-aws2
spec:
  package: crossplane/provider-aws:v0.23.0
  controllerConfigRef:
    name: provider-aws
`,
			expected: 2,
		},
		{
			name: "some invalid resources",
			resources: `
---
piVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: crossplane-provider-aws1
spec:
  package: crossplane/provider-aws:v0.23.0
  controllerConfigRef:
    name: provider-aws
---
apiVersion: v1
kind: Secret
metadata:
  name: test
  namespace: default
immutable: true
stringData:
  key: "private-key"
`,
			expected: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			objects, err := ReadObjects(strings.NewReader(tc.resources))
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if len(objects) != tc.expected {
				t.Errorf("unexpected number of objects in %v", objects)
			}
		})
	}
}

package printer

import (
	"fmt"

	"sigs.k8s.io/cli-utils/pkg/object"
)

// various utils

type Len struct {
	KindNameMaxLen  int
	NamespaceMaxLen int
}

func CalcLen(resources []object.ObjMetadata) *Len {
	k := 0
	n := 0
	for _, r := range resources {
		// kind/name
		kn := fmt.Sprintf("%s/%s", r.GroupKind.Kind, r.Name)
		if len(kn) > k {
			k = len(kn)
		}
		// namespace
		ns := r.Namespace
		if ns == "" {
			ns = "(cluster)"
		}
		if len(ns) > n {
			n = len(ns)
		}
	}
	return &Len{
		KindNameMaxLen:  k,
		NamespaceMaxLen: n,
	}
}

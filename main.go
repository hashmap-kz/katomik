// go 1.21   client-go v0.30   sigs.k8s.io/yaml v1.4
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"k8s.io/client-go/rest"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

var dec = yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)

type applyItem struct {
	obj     *unstructured.Unstructured
	dr      dynamic.ResourceInterface
	existed bool
	backup  []byte
}

func main() {
	// ---------- CLI ----------
	var file, ns string
	var toStr string
	flag.StringVar(&file, "f", "", "manifest.yaml")
	flag.StringVar(&ns, "namespace", "default", "fallback ns")
	flag.StringVar(&toStr, "timeout", "30s", "wait timeout")
	flag.Parse()
	if file == "" {
		fmt.Println("usage: atomic-apply -f manifest.yaml")
		os.Exit(1)
	}
	timeout, _ := time.ParseDuration(toStr)

	// ---------- clients ----------
	cfg, err := rest.InClusterConfig()
	if err != nil {
		cfg, err = clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		if err != nil {
			log.Fatal(err)
		}
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		log.Fatal(err)
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		log.Fatal(err)
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disc))

	// ---------- parse yaml ----------
	raw, _ := os.ReadFile(file)
	docs := strings.Split(string(raw), "\n---")
	var plan []applyItem

	for _, d := range docs {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		u := &unstructured.Unstructured{}
		_, gvk, _ := dec.Decode([]byte(d), nil, u)
		u.SetGroupVersionKind(*gvk)

		m, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			mapper.Reset()
			m, _ = mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		}
		var dr dynamic.ResourceInterface
		if m.Scope.Name() == meta.RESTScopeNameNamespace {
			if u.GetNamespace() == "" {
				u.SetNamespace(ns)
			}
			dr = dyn.Resource(m.Resource).Namespace(u.GetNamespace())
		} else {
			dr = dyn.Resource(m.Resource)
		}

		it := applyItem{obj: u, dr: dr}
		// ---- existence + backup
		if cur, err := dr.Get(context.TODO(), u.GetName(), metav1.GetOptions{}); err == nil {
			it.existed = true
			stripMeta(cur.Object)
			it.backup, _ = json.Marshal(cur.Object)
		}
		plan = append(plan, it)
	}

	// ---------- apply ----------
	for _, it := range plan {
		var err error
		if it.existed {
			_, err = it.dr.Update(context.TODO(), it.obj, metav1.UpdateOptions{})
		} else {
			_, err = it.dr.Create(context.TODO(), it.obj, metav1.CreateOptions{})
		}
		if err != nil {
			rollback(plan)
		}
	}

	// ---------- wait ----------
	if err := waitReady(plan, timeout); err != nil {
		rollback(plan)
	}
	fmt.Println("✓ success")
}

/* ---- readiness (very short) ---- */
func waitReady(plan []applyItem, to time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), to)
	defer cancel()
	var checks []func() (bool, error)
	for _, p := range plan {
		kind := strings.ToLower(p.obj.GetKind())
		name := p.obj.GetName()
		switch kind {
		case "deployment":
			checks = append(checks, func() (bool, error) {
				o, err := p.dr.Get(ctx, name, metav1.GetOptions{})
				if err != nil {
					return false, err
				}

				gen, _, _ := unstructured.NestedInt64(o.Object, "metadata", "generation")
				obs, _, _ := unstructured.NestedInt64(o.Object, "status", "observedGeneration")

				want, _, _ := unstructured.NestedInt64(o.Object, "spec", "replicas")
				updated, _, _ := unstructured.NestedInt64(o.Object, "status", "updatedReplicas")
				total, _, _ := unstructured.NestedInt64(o.Object, "status", "replicas")
				avail, _, _ := unstructured.NestedInt64(o.Object, "status", "availableReplicas")

				if want == 0 {
					want = 1
				} // default when .spec.replicas omitted

				progressOk := (gen == obs) &&
					(updated == want) &&
					(total == want) &&
					(avail == want)

				// Fast-fail if the controller already declared the rollout dead
				if conds, _, _ := unstructured.NestedSlice(o.Object, "status", "conditions"); conds != nil {
					for _, c := range conds {
						if m, ok := c.(map[string]interface{}); ok &&
							m["type"] == "Progressing" &&
							m["reason"] == "ProgressDeadlineExceeded" {
							return false, fmt.Errorf("deployment %s stalled", name)
						}
					}
				}
				return progressOk, nil
			})

		case "statefulset":
			checks = append(checks, func() (bool, error) {
				o, err := p.dr.Get(ctx, name, metav1.GetOptions{})
				if err != nil {
					return false, err
				}
				ready, _, _ := unstructured.NestedInt64(o.Object, "status", "readyReplicas")
				want, _, _ := unstructured.NestedInt64(o.Object, "spec", "replicas")
				return ready >= want, nil
			})
		case "job":
			checks = append(checks, func() (bool, error) {
				o, err := p.dr.Get(ctx, name, metav1.GetOptions{})
				if err != nil {
					return false, err
				}
				succ, _, _ := unstructured.NestedInt64(o.Object, "status", "succeeded")
				comp, _, _ := unstructured.NestedInt64(o.Object, "spec", "completions")
				return (comp == 0 && succ > 0) || succ >= comp, nil
			})
		}
	}
	return wait.PollUntilContextCancel(ctx, 2*time.Second, true,
		func(ctx context.Context) (bool, error) {
			for _, f := range checks {
				ok, err := f()
				if err != nil || !ok {
					return false, err
				}
			}
			return true, nil
		})
}

/* ---- rollback ---- */
func rollback(plan []applyItem) {
	fmt.Println("⟲ rollback …")
	for _, it := range plan {
		if it.existed {
			// restore previous JSON
			u := &unstructured.Unstructured{}
			_ = u.UnmarshalJSON(it.backup)
			_, _ = it.dr.Update(context.TODO(), u, metav1.UpdateOptions{})
		} else {
			_ = it.dr.Delete(context.TODO(), it.obj.GetName(), metav1.DeleteOptions{})
		}
	}
	fmt.Println("rollback complete")
	os.Exit(1)
}

/* ---- util ---- */
func stripMeta(o map[string]interface{}) {
	delete(o, "status")
	if m, ok := o["metadata"].(map[string]interface{}); ok {
		for _, k := range []string{"managedFields", "resourceVersion", "uid", "creationTimestamp"} {
			delete(m, k)
		}
	}
}

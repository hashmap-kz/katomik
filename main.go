package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

type snapshot struct {
	gvr  schema.GroupVersionResource
	ns   string
	name string
	obj  *unstructured.Unstructured
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: atomic-apply manifest.yaml")
		os.Exit(1)
	}

	manifestPath := os.Args[1]
	data, err := os.ReadFile(manifestPath)
	check(err)

	// Load kubeconfig
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = clientcmd.RecommendedHomeFile
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	check(err)

	dyn, err := dynamic.NewForConfig(cfg)
	check(err)

	disco, err := discovery.NewDiscoveryClientForConfig(cfg)
	check(err)

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disco))

	// Parse manifests
	objs := parseManifests(bytes.NewReader(data))

	var prev []snapshot
	ctx := context.Background()

	for _, obj := range objs {
		gvk := obj.GroupVersionKind()
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		check(err)

		ns := obj.GetNamespace()
		if ns == "" {
			ns = "default"
			obj.SetNamespace(ns)
		}

		resource := dyn.Resource(mapping.Resource).Namespace(ns)
		existing, err := resource.Get(ctx, obj.GetName(), metav1.GetOptions{})
		if err == nil {
			prev = append(prev, snapshot{
				gvr:  mapping.Resource,
				ns:   ns,
				name: obj.GetName(),
				obj:  existing.DeepCopy(),
			})
			_, err = resource.Update(ctx, obj, metav1.UpdateOptions{})
		} else {
			_, err = resource.Create(ctx, obj, metav1.CreateOptions{})
		}

		if err != nil {
			fmt.Printf("❌ Failed to apply %s/%s: %v\n", obj.GetKind(), obj.GetName(), err)
			rollback(ctx, dyn, prev)
			os.Exit(1)
		}
	}

	for _, obj := range objs {
		if err := waitForReady(ctx, dyn, mapper, obj, 30*time.Second); err != nil {
			fmt.Printf("⏳ Timeout waiting for %s/%s: %v\n", obj.GetKind(), obj.GetName(), err)
			rollback(ctx, dyn, prev)
			os.Exit(1)
		}
	}

	fmt.Println("✅ All resources applied and ready.")
}

func parseManifests(r io.Reader) []*unstructured.Unstructured {
	decoder := yaml.NewYAMLOrJSONDecoder(r, 4096)
	var docs []*unstructured.Unstructured

	for {
		u := &unstructured.Unstructured{}
		err := decoder.Decode(u)
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error decoding manifest: %v\n", err)
			continue
		}
		if u.Object != nil && u.GetKind() != "" {
			docs = append(docs, u)
		}
	}
	return docs
}

func rollback(ctx context.Context, dyn dynamic.Interface, prev []snapshot) {
	fmt.Println("🔁 Rolling back...")
	for _, s := range prev {
		res := dyn.Resource(s.gvr).Namespace(s.ns)
		_, err := res.Update(ctx, s.obj, metav1.UpdateOptions{})
		if err != nil {
			fmt.Printf("⚠️ Failed to rollback %s/%s: %v\n", s.gvr.Resource, s.name, err)
		} else {
			fmt.Printf("✅ Rolled back %s/%s\n", s.gvr.Resource, s.name)
		}
	}
}

func waitForReady(ctx context.Context, dyn dynamic.Interface, rm meta.RESTMapper, obj *unstructured.Unstructured, timeout time.Duration) error {
	gvk := obj.GroupVersionKind()
	mapping, err := rm.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return err
	}
	ns := obj.GetNamespace()
	res := dyn.Resource(mapping.Resource).Namespace(ns)
	name := obj.GetName()

	start := time.Now()
	for {
		if time.Since(start) > timeout {
			return fmt.Errorf("timeout exceeded")
		}

		u, err := res.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		conds, found, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		if found {
			for _, cond := range conds {
				if m, ok := cond.(map[string]interface{}); ok {
					if m["type"] == "Available" && m["status"] == "True" {
						return nil
					}
				}
			}
		}

		time.Sleep(2 * time.Second)
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: %v\n", err)
		os.Exit(1)
	}
}

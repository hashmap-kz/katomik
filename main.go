// go 1.21   client-go v0.30   sigs.k8s.io/yaml v1.4
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"

	"sigs.k8s.io/cli-utils/pkg/kstatus/polling"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling/aggregator"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling/collector"
	pollEvent "sigs.k8s.io/cli-utils/pkg/kstatus/polling/event"
	kstatus "sigs.k8s.io/cli-utils/pkg/kstatus/status"
	"sigs.k8s.io/cli-utils/pkg/object"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

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

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)

	crClient, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		log.Fatal(err)
	}

	// ---------- parse yaml ----------
	raw, err := os.ReadFile(file)
	if err != nil {
		log.Fatal(err)
	}
	docs, err := readManifests(raw)
	if err != nil {
		log.Fatal(err)
	}

	// ---------- gen plan ----------
	var plan []applyItem
	for _, u := range docs {
		gvk := u.GroupVersionKind()

		m, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			mapper.Reset()
			m, err = mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
			if err != nil {
				log.Fatalf("could not map GVK %v: %v", gvk, err)
			}
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
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := waitStatus(ctx, plan, crClient, mapper); err != nil {
		rollback(plan)
	}

	fmt.Println("✓ success")
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

// status watcher

func waitStatus(
	ctx context.Context,
	plan []applyItem,
	reader ctrlclient.Reader,
	mapper meta.RESTMapper,
) error {
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 1. Convert to ObjMetadata list
	var resources []object.ObjMetadata
	for _, it := range plan {
		// You could decode and skip paused Deployments here if desired
		id, err := object.RuntimeToObjMeta(it.obj)
		if err != nil {
			return err
		}
		resources = append(resources, id)
	}

	if len(resources) == 0 {
		fmt.Println("✓ no trackable resources")
		return nil
	}

	fmt.Println("⏳ waiting for resources:")
	for _, id := range resources {
		fmt.Printf(" - %s\n", id)
	}

	// 2. Start polling
	poller := polling.NewStatusPoller(reader, mapper, polling.Options{})
	eventCh := poller.Poll(cancelCtx, resources, polling.PollOptions{
		PollInterval: 2 * time.Second,
	})

	// 3. Start collector with observer
	statusCollector := collector.NewResourceStatusCollector(resources)
	done := statusCollector.ListenWithObserver(eventCh, statusObserver(cancel, kstatus.CurrentStatus))

	<-done

	// 4. On error
	if statusCollector.Error != nil {
		return statusCollector.Error
	}

	// 5. Context deadline reached?
	if ctx.Err() != nil {
		var errs []error
		for _, id := range resources {
			rs := statusCollector.ResourceStatuses[id]
			if rs != nil && rs.Status != kstatus.CurrentStatus {
				errs = append(errs, fmt.Errorf("resource not ready: %s (%s)", id.String(), rs.Status))
			}
		}
		errs = append(errs, ctx.Err())
		return errors.Join(errs...)
	}

	return nil
}

func statusObserver(cancel context.CancelFunc, desired kstatus.Status) collector.ObserverFunc {
	return func(c *collector.ResourceStatusCollector, _ pollEvent.Event) {
		var rss []*pollEvent.ResourceStatus
		var nonReady []*pollEvent.ResourceStatus

		for _, rs := range c.ResourceStatuses {
			if rs == nil {
				continue
			}
			if rs.Status == kstatus.UnknownStatus && desired == kstatus.NotFoundStatus {
				continue
			}
			rss = append(rss, rs)
			if rs.Status != desired {
				nonReady = append(nonReady, rs)
			}
		}

		if aggregator.AggregateStatus(rss, desired) == desired {
			cancel()
			return
		}

		if len(nonReady) > 0 {
			sort.Slice(nonReady, func(i, j int) bool {
				return nonReady[i].Identifier.Name < nonReady[j].Identifier.Name
			})
			first := nonReady[0]
			fmt.Printf("[watch] waiting: %s %s → %s\n",
				first.Identifier.GroupKind.Kind,
				first.Identifier.Name,
				first.Status)
		}
	}
}

func readManifests(data []byte) ([]*unstructured.Unstructured, error) {
	docs := []*unstructured.Unstructured{}
	stream := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for {
		obj := &unstructured.Unstructured{}
		err := stream.Decode(obj)
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, err
		}
		if obj.Object != nil && len(obj.Object) > 0 {
			docs = append(docs, obj)
		}
	}
	return docs, nil
}

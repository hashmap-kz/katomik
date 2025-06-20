package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"time"

	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/hashmap-kz/kubectl-atomic-apply/internal/resolve"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/restmapper"
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
	rv      string
}

type atomicApplyOptions struct {
	filenames []string
	timeout   time.Duration
	recursive bool
}

type atomicApplyRunOptions struct {
	configFlags *genericclioptions.ConfigFlags
	streams     genericiooptions.IOStreams
	applyOpts   atomicApplyOptions
}

// NewAtomicApplyCmd builds the root cobra.Command for atomic-apply.
//
// It keeps the important flags (-f/-R/--timeout) at the top and pushes the
// kubectl connection flags into their own section so the --help text is short
// and readable.
func NewAtomicApplyCmd(streams genericiooptions.IOStreams) *cobra.Command {
	cfgFlags := genericclioptions.NewConfigFlags(true) // all kubectl connection flags
	aa := atomicApplyOptions{}

	cmd := &cobra.Command{
		Use:   "atomic-apply -f FILE [-f FILE...]",
		Short: "Atomically apply Kubernetes manifests and roll back on failure",
		Long: `atomic-apply is a transactional 'kubectl apply'.

 * Applies a set of manifests in one transaction
 * Rolls back automatically if any object fails
 * Waits for all resources to become Ready
`,
		Example: `
  # Apply a single manifest
  atomic-apply -f deploy.yaml

  # Apply everything under ./manifests, descending into sub-dirs
  atomic-apply -f ./manifests -R

  # Use a specific kube-context
  atomic-apply -f app.yaml --context staging
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(aa.filenames) == 0 {
				return fmt.Errorf("at least one --filename/-f must be specified")
			}

			run := &atomicApplyRunOptions{
				configFlags: cfgFlags,
				streams:     streams,
				applyOpts:   aa,
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), aa.timeout)
			defer cancel()
			return runApply(ctx, run)
		},
	}

	// core flags
	f := cmd.Flags()
	f.SortFlags = false // preserve insertion order

	f.StringSliceVarP(&aa.filenames, "filename", "f", nil,
		"Manifest files, glob patterns, or directories to apply.")
	//nolint:errcheck
	_ = cmd.MarkFlagRequired("filename")

	f.BoolVarP(&aa.recursive, "recursive", "R", false,
		"Recurse into directories specified with --filename.")
	f.DurationVar(&aa.timeout, "timeout", 30*time.Second,
		"Wait timeout for resources to reach the desired state.")

	// Kubernetes connection flags (own section)
	conn := pflag.NewFlagSet("Kubernetes connection flags", pflag.ContinueOnError)
	cfgFlags.AddFlags(conn)
	cmd.Flags().AddFlagSet(conn)

	return cmd
}

func runApply(ctx context.Context, runOpts *atomicApplyRunOptions) error {
	// init clients
	cfg, err := runOpts.configFlags.ToRESTConfig()
	if err != nil {
		return err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disc))

	scheme := runtime.NewScheme()
	err = clientgoscheme.AddToScheme(scheme)
	if err != nil {
		return err
	}

	crClient, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	// resolve all filenames: expand all glob-patterns, list directories, etc...
	files, err := resolve.ResolveAllFiles(runOpts.applyOpts.filenames, runOpts.applyOpts.recursive)
	if err != nil {
		return err
	}

	// collect all files as docs
	var allDocs []*unstructured.Unstructured
	for _, file := range files {
		fileContent, err := resolve.ReadFileContent(file)
		if err != nil {
			return err
		}
		docs, err := readManifests(fileContent)
		if err != nil {
			return err
		}
		allDocs = append(allDocs, docs...)
	}

	// make plan
	plan := make([]applyItem, 0, len(allDocs))
	for _, u := range allDocs {
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
				var ns string
				if runOpts.configFlags.Namespace != nil {
					ns = *runOpts.configFlags.Namespace
					if ns == "" {
						ns = "default"
					}
				}
				u.SetNamespace(ns)
			}
			dr = dyn.Resource(m.Resource).Namespace(u.GetNamespace())
		} else {
			dr = dyn.Resource(m.Resource)
		}

		it := applyItem{obj: u, dr: dr}
		// existence + backup
		cur, err := dr.Get(context.TODO(), u.GetName(), metav1.GetOptions{})
		if err == nil {
			it.existed = true
			it.rv = cur.GetResourceVersion() // keep it
			stripMeta(cur.Object)            // only for the backup
			it.backup, err = json.Marshal(cur.Object)
			if err != nil {
				return err
			}
		}
		plan = append(plan, it)
	}

	// apply
	for _, it := range plan {
		objJSON, jErr := json.Marshal(it.obj)
		if jErr != nil {
			return rollback(plan)
		}

		// SSA: create if absent, patch if present
		_, err = it.dr.Patch(
			ctx,
			it.obj.GetName(),
			types.ApplyPatchType,
			objJSON,
			metav1.PatchOptions{
				FieldManager: "atomic-apply",
				Force:        ptr.To(true), // overwrite last-applied on conflicts
			},
		)
		if err != nil {
			return rollback(plan)
		}
	}

	// wait until all resources are ready, rollback otherwise
	if err := waitStatus(ctx, plan, crClient, mapper); err != nil {
		return rollback(plan)
	}

	fmt.Println("✓ success")
	return nil
}

// rollback to initial state
func rollback(plan []applyItem) error {
	fmt.Println("⟲ rollback ...")
	for _, it := range plan {
		if it.existed {
			// restore previous JSON
			u := &unstructured.Unstructured{}
			err := u.UnmarshalJSON(it.backup)
			if err != nil {
				return err
			}
			_, err = it.dr.Update(context.TODO(), u, metav1.UpdateOptions{})
			if err != nil {
				return err
			}
		} else {
			err := it.dr.Delete(context.TODO(), it.obj.GetName(), metav1.DeleteOptions{})
			if err != nil {
				return err
			}
		}
	}

	// TODO: revive

	fmt.Println("rollback complete")
	os.Exit(1)

	return nil
}

// stripMeta removes undesired properties
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
	resources := make([]object.ObjMetadata, 0, len(plan))
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
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if len(obj.Object) > 0 {
			docs = append(docs, obj)
		}
	}
	return docs, nil
}

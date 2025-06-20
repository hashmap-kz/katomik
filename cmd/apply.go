package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/hashmap-kz/kubectl-atomic-apply/internal/apply"

	"github.com/spf13/pflag"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/spf13/cobra"
)

// NewAtomicApplyCmd builds the root cobra.Command for atomic-apply.
//
// It keeps the important flags (-f/-R/--timeout) at the top and pushes the
// kubectl connection flags into their own section so the --help text is short
// and readable.
func NewAtomicApplyCmd(streams genericiooptions.IOStreams) *cobra.Command {
	cfgFlags := genericclioptions.NewConfigFlags(true) // all kubectl connection flags
	aa := apply.AtomicApplyOptions{}

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
			if len(aa.Filenames) == 0 {
				return fmt.Errorf("at least one --filename/-f must be specified")
			}

			run := &apply.AtomicApplyRunOptions{
				ConfigFlags: cfgFlags,
				Streams:     streams,
				ApplyOpts:   aa,
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), aa.Timeout)
			defer cancel()
			return apply.RunApply(ctx, run)
		},
	}

	// core flags
	f := cmd.Flags()
	f.SortFlags = false // preserve insertion order

	f.StringSliceVarP(&aa.Filenames, "filename", "f", nil, "Manifest files, glob patterns, or directories to apply.")
	//nolint:errcheck
	_ = cmd.MarkFlagRequired("filename")

	f.BoolVarP(&aa.Recursive, "recursive", "R", false, "Recurse into directories specified with --filename.")
	f.DurationVar(&aa.Timeout, "timeout", 30*time.Second, "Wait timeout for resources to reach the desired state.")

	// Kubernetes connection flags (own section)
	conn := pflag.NewFlagSet("Kubernetes connection flags", pflag.ContinueOnError)
	cfgFlags.AddFlags(conn)
	cmd.Flags().AddFlagSet(conn)

	return cmd
}

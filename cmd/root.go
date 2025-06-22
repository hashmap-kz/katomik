package cmd

import (
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"
)

func NewRootCmd(streams genericiooptions.IOStreams) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "katomik",
		Short:         "Atomic apply of multiple Kubernetes manifests with rollback on failure.",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.SetHelpCommand(&cobra.Command{
		Use:    "no-help",
		Hidden: true,
	})
	rootCmd.AddCommand(NewAtomicApplyCmd(streams))
	return rootCmd
}

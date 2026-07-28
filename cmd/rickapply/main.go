// Command rickapply extracts the most recent unified diff produced by the agent
// in a rick session and applies it to the working tree with git apply.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"rick/internal/apply"
)

func main() {
	var (
		flagDryRun    bool
		flagSessionID string
		flagLast      bool
	)

	root := &cobra.Command{
		Use:   "rickapply",
		Short: "Apply the latest agent-produced diff with git apply",
		Long: "rickapply finds the most recent unified diff in a rick session — from an\n" +
			"apply_patch tool call or any tool output containing patch text — and\n" +
			"applies it to the working tree via git apply.\n\n" +
			"Examples:\n" +
			"  rickapply\n" +
			"  rickapply --dry-run\n" +
			"  rickapply --session 01j2k3",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = flagLast // --last is the default behaviour; the flag documents it.
			return apply.Run(".", apply.Options{SessionID: flagSessionID, DryRun: flagDryRun})
		},
	}

	root.Flags().BoolVarP(&flagDryRun, "dry-run", "n", false, "only run git apply --check")
	root.Flags().StringVar(&flagSessionID, "session", "", "apply the diff from a specific session id")
	root.Flags().BoolVar(&flagLast, "last", true, "apply the most recent diff (default)")

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rickapply: "+err.Error())
		os.Exit(1)
	}
}

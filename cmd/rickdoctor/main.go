// Command rickdoctor diagnoses a rick installation. It inspects the toolchain,
// external binaries, configuration, credentials, data directories, themes and
// plugins, then prints a status table. It performs no network access unless
// --network is passed.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"rick/internal/doctor"
)

func main() {
	root := &cobra.Command{
		Use:   "rickdoctor",
		Short: "Check the health of the rick installation and environment",
		Long: "rickdoctor runs a series of local diagnostics — toolchain, external\n" +
			"binaries, configuration, credentials, MCP servers, data directory,\n" +
			"themes and plugins — and prints a status table.\n\n" +
			"Network probes are skipped unless --network is passed.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			checks := doctor.RunChecks()
			doctor.PrintReport(checks)
			os.Exit(doctor.ExitCode(checks))
			return nil
		},
	}
	root.Flags().BoolVar(&doctor.CheckNetwork, "network", false,
		"probe provider endpoints for connectivity (makes network calls)")

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rickdoctor: "+err.Error())
		os.Exit(1)
	}
}

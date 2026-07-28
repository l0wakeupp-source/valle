// Command ricksecurity audits project dependencies against OSV.dev for known
// supply-chain vulnerabilities.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"rick/internal/security"
)

func main() {
	var (
		flagDir    string
		flagForce  bool
		flagFormat string
	)

	cmd := &cobra.Command{
		Use:   "ricksecurity",
		Short: "Audit project dependencies for known vulnerabilities",
		Long: "Scan the project's dependency manifests (go.mod, package.json, Cargo.toml,\n" +
			"requirements.txt, pyproject.toml) and query OSV.dev for known\n" +
			"supply-chain vulnerabilities in each dependency.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagFormat != "table" && flagFormat != "json" {
				return fmt.Errorf("unknown format %q (want table or json)", flagFormat)
			}

			if flagForce {
				// Skip cache: remove the on-disk cache before auditing.
				cachePath := filepath.Join(flagDir, ".rick", "security-cache.json")
				_ = os.Remove(cachePath)
			}

			findings, err := security.Audit(flagDir)
			if err != nil {
				return err
			}

			if flagFormat == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(findings)
			}

			renderTable(findings)
			renderSummary(findings)

			if len(findings) > 0 {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flagDir, "dir", ".", "project directory to audit")
	cmd.Flags().BoolVar(&flagForce, "force", false, "skip the cache and re-query OSV.dev")
	cmd.Flags().StringVar(&flagFormat, "format", "table", "output format: table | json")

	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "ricksecurity: "+err.Error())
		os.Exit(1)
	}
}

func renderTable(findings []security.Finding) {
	if len(findings) == 0 {
		fmt.Println("no vulnerabilities found")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PACKAGE\tVERSION\tSEVERITY\tCVE\tOSV ID\tURL")
	for _, f := range findings {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			f.Package, f.Version, f.Severity, f.CVE, f.OSVID, f.URL)
	}
	w.Flush()
}

func renderSummary(findings []security.Finding) {
	counts := map[string]int{}
	for _, f := range findings {
		sev := f.Severity
		if sev == "" {
			sev = "unknown"
		}
		counts[sev]++
	}

	fmt.Printf("\n%d vulnerabilities found", len(findings))
	parts := []string{}
	for _, sev := range []string{"critical", "high", "moderate", "low", "unknown"} {
		if n := counts[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, sev))
		}
	}
	if len(parts) > 0 {
		fmt.Printf(" (%s)", joinParts(parts))
	}
	fmt.Println()
}

func joinParts(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

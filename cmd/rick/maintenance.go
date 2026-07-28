package main

import (
	"os"

	"github.com/spf13/cobra"

	"rick/internal/maintenance"
)

func updateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update Rick to the latest GitHub release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return maintenance.Update(cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

func uninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Rick, optionally including its user data",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return maintenance.Uninstall(os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

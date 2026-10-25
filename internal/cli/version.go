package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/devlyft/cli/internal/cmdutil"
	"github.com/devlyft/cli/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "version",
		Annotations: map[string]string{cmdutil.NoConfig: "true"},
		Short:       "Print the devlyft version",
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version.Get())
			return err
		},
	}
}

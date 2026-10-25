package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/devlyft/cli/internal/cmdutil"
)

// newCompletionCmd replaces Cobra's default completion command (Cobra skips
// its own when one named "completion" exists) so the help text is ours.
func newCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "completion bash|zsh|fish|powershell",
		Annotations: map[string]string{cmdutil.NoConfig: "true"},
		Short:       "Generate a shell completion script",
		Long: `Generate a completion script for devlyft for the given shell.

The script is printed to stdout. Load it in your shell's start-up file so
completions are available in every session.

Bash (needs the bash-completion package):

  echo 'eval "$(devlyft completion bash)"' >> ~/.bashrc

Zsh (enable completion first with "autoload -U compinit && compinit"):

  devlyft completion zsh > "${fpath[1]}/_devlyft"

Fish:

  devlyft completion fish > ~/.config/fish/completions/devlyft.fish

PowerShell:

  devlyft completion powershell | Out-String | Invoke-Expression

  To load it in every session, add that line to the file shown by $PROFILE.`,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			out := cmd.OutOrStdout()
			switch args[0] {
			case "bash":
				return root.GenBashCompletionV2(out, true)
			case "zsh":
				return root.GenZshCompletion(out)
			case "fish":
				return root.GenFishCompletion(out, true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(out)
			default:
				return fmt.Errorf("unsupported shell %q", args[0])
			}
		},
	}
}

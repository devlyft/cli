// Package config implements `devlyft config` and `devlyft context`.
package config

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/devlyft/cli/internal/cmdutil"
	"github.com/devlyft/cli/internal/config"
	"github.com/devlyft/cli/internal/output"
)

// noScope lets these commands run, and repair the file, even when the
// current scope does not resolve (for example a dangling current_context).
var noScope = map[string]string{cmdutil.NoScope: "true"}

type setting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// NewConfigCmd returns `devlyft config`.
func NewConfigCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and change CLI settings",
		Long: `Read and change the top-level settings in the config file:

  api_url          DevLyft API URL (DEVLYFT_API_URL and --api-url override it)
  ca_bundle        PEM file of extra CA certificates to trust (DEVLYFT_CA_BUNDLE overrides it)
  current_context  Context used when --context and DEVLYFT_CONTEXT are not set

Per-context defaults (org, project, environment, location, account) are
managed with 'devlyft context'.`,
		Annotations: noScope,
	}
	cmd.AddCommand(newConfigGetCmd(f), newConfigSetCmd(f), newConfigListCmd(f))
	return cmd
}

func newConfigGetCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get KEY",
		Short: "Print a setting",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			v, err := f.Config.Get(args[0])
			if err != nil {
				return err
			}
			return f.Printer.Print(output.Result{
				Data:  setting{Key: args[0], Value: v},
				Table: &output.Table{Headers: []string{"value"}, Rows: [][]string{{v}}},
				IDs:   []string{v},
			})
		},
	}
}

func newConfigSetCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "set KEY VALUE",
		Short: "Change a setting; an empty VALUE unsets it",
		Example: `  devlyft config set api_url https://api.devlyft.io
  devlyft config set ca_bundle ~/corp-ca.pem
  devlyft config set current_context ""`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := f.Config.Set(args[0], args[1]); err != nil {
				return err
			}
			if err := f.Config.Save(); err != nil {
				return err
			}
			v, _ := f.Config.Get(args[0])
			return f.Printer.Print(output.Result{
				Data:  setting{Key: args[0], Value: v},
				Table: &output.Table{Headers: []string{"key", "value"}, Rows: [][]string{{args[0], v}}},
				IDs:   []string{args[0]},
			})
		},
	}
}

func newConfigListCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Print every setting",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			t := &output.Table{Headers: []string{"key", "value"}}
			data := make(map[string]string, len(config.Keys)+1)
			for _, k := range config.Keys {
				v, _ := f.Config.Get(k)
				t.Rows = append(t.Rows, []string{k, v})
				data[k] = v
			}
			data["path"] = f.Config.Path()
			t.Rows = append(t.Rows, []string{"path", f.Config.Path()})
			return f.Printer.Print(output.Result{Data: data, Table: t, IDs: config.Keys})
		},
	}
}

// scopeFlags reads the global scope flags as given on this command line.
func scopeFlags(cmd *cobra.Command) (config.Flags, map[string]bool) {
	get := func(name string) string {
		v, _ := cmd.Flags().GetString(name)
		return v
	}
	changed := map[string]bool{}
	for _, n := range []string{"org", "project", "environment", "location"} {
		changed[n] = cmd.Flags().Changed(n)
	}
	return config.Flags{
		Context:  get("context"),
		APIURL:   get("api-url"),
		Org:      get("org"),
		Project:  get("project"),
		Env:      get("environment"),
		Location: get("location"),
	}, changed
}

func describeContext(name string, current bool) string {
	if current {
		return fmt.Sprintf("context %q (current)", name)
	}
	return fmt.Sprintf("context %q", name)
}

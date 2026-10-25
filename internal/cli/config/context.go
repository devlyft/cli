package config

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/cmdutil"
	"github.com/devlyft/cli/internal/config"
	"github.com/devlyft/cli/internal/output"
	"github.com/devlyft/cli/internal/prompt"
)

// contextView is a context as printed.
type contextView struct {
	Name    string `json:"name"`
	Current bool   `json:"current"`
	config.Context
}

func view(file *config.File, name string) contextView {
	c := file.Contexts[name]
	if c == nil {
		c = &config.Context{}
	}
	return contextView{Name: name, Current: file.CurrentContext == name, Context: *c}
}

func (v *contextView) row() []string {
	cur := ""
	if v.Current {
		cur = "*"
	}
	return []string{cur, v.Name, v.Account, v.Org, v.Project, v.Env, v.Location}
}

var contextHeaders = []string{"current", "name", "account", "org", "project", "environment", "location"}

// NewContextCmd returns `devlyft context`.
func NewContextCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage named sets of defaults",
		Long: `A context saves the organization, project, environment, location and
account that commands use when no flag or environment variable sets them.

Every value is resolved by the same precedence: flag, then environment
variable (DEVLYFT_ORG, DEVLYFT_PROJECT, DEVLYFT_ENV, DEVLYFT_LOCATION), then
the context, then the default. --context or DEVLYFT_CONTEXT picks a context
for one command without changing the current one.`,
		Annotations: noScope,
	}
	cmd.AddCommand(newContextUseCmd(f), newContextListCmd(f), newContextShowCmd(f), newContextDeleteCmd(f))
	return cmd
}

func newContextUseCmd(f *cmdutil.Factory) *cobra.Command {
	var account string
	cmd := &cobra.Command{
		Use:   "use NAME",
		Short: "Switch to a context, creating it if needed, and save the scope flags into it",
		Long: `Make NAME the current context. If it does not exist it is created.

Any of --org, --project, --environment, --location and --account given on
the same command line are saved into the context; pass an empty value
(--project "") to clear one. Values may be names or IDs; IDs are
recommended for organizations, whose names are not unique.`,
		Example: `  devlyft context use prod --org acme --project web --environment production
  devlyft context use staging
  devlyft context use prod --project ""`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if name == "" {
				return clierr.Usagef("context name must not be empty")
			}
			file := f.Config
			if file.Contexts == nil {
				file.Contexts = map[string]*config.Context{}
			}
			c, existed := file.Contexts[name]
			if !existed {
				c = &config.Context{}
				file.Contexts[name] = c
			}
			flags, changed := scopeFlags(cmd)
			if changed["org"] {
				c.Org = flags.Org
			}
			if changed["project"] {
				c.Project = flags.Project
			}
			if changed["environment"] {
				c.Env = flags.Env
			}
			if changed["location"] {
				c.Location = flags.Location
			}
			if cmd.Flags().Changed("account") {
				c.Account = account
			}
			file.CurrentContext = name
			if err := file.Save(); err != nil {
				return err
			}
			verb := "Switched to"
			if !existed {
				verb = "Created and switched to"
			}
			_, _ = fmt.Fprintf(f.IO.ErrOut, "%s context %q.\n", verb, name)
			v := view(file, name)
			return f.Printer.Print(output.Result{
				Data:  v,
				Table: &output.Table{Headers: contextHeaders, Rows: [][]string{v.row()}},
				IDs:   []string{name},
			})
		},
	}
	cmd.Flags().StringVar(&account, "account", "", "Credential account to use with this context (see 'devlyft auth list')")
	return cmd
}

func newContextListCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List contexts",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			names := f.Config.ContextNames()
			views := make([]contextView, 0, len(names))
			t := &output.Table{Headers: contextHeaders}
			for _, n := range names {
				v := view(f.Config, n)
				views = append(views, v)
				t.Rows = append(t.Rows, v.row())
			}
			return f.Printer.Print(output.Result{Data: views, Table: t, IDs: names})
		},
	}
}

func newContextShowCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "show [NAME]",
		Aliases: []string{"describe"},
		Short:   "Show a context, or the settings this command line resolves to",
		Long: `With NAME, show that context's saved values.

Without NAME, show the settings a command would use right now, after
flags, environment variables and the context are applied, and where each
value came from.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				name := args[0]
				if _, ok := f.Config.Contexts[name]; !ok {
					return config.NoSuchContext(name)
				}
				v := view(f.Config, name)
				return f.Printer.Print(output.Result{
					Data:  v,
					Table: &output.Table{Headers: contextHeaders, Rows: [][]string{v.row()}},
					IDs:   []string{name},
				})
			}
			flags, _ := scopeFlags(cmd)
			s, err := config.ResolveScope(f.Config, &flags, f.Getenv)
			if err != nil {
				return err
			}
			rows := [][]string{}
			for _, kv := range []struct {
				key string
				v   config.Value
			}{
				{"context", s.Context},
				{"api_url", s.APIURL},
				{"ca_bundle", s.CABundle},
				{"account", s.Account},
				{"org", s.Org},
				{"project", s.Project},
				{"environment", s.Env},
				{"location", s.Location},
			} {
				rows = append(rows, []string{kv.key, kv.v.Value, string(kv.v.Source)})
			}
			return f.Printer.Print(output.Result{
				Data:  s,
				Table: &output.Table{Headers: []string{"setting", "value", "source"}, Rows: rows},
				IDs:   []string{s.Context.Value},
			})
		},
	}
}

func newContextDeleteCmd(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a context",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if _, ok := f.Config.Contexts[name]; !ok {
				return config.NoSuchContext(name)
			}
			current := f.Config.CurrentContext == name
			ok, err := prompt.Confirm(f.IO, "Delete "+describeContext(name, current)+"?", yes)
			if err != nil || !ok {
				return err
			}
			delete(f.Config.Contexts, name)
			if current {
				f.Config.CurrentContext = ""
			}
			if err := f.Config.Save(); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(f.IO.ErrOut, "Deleted context %q.\n", name)
			return f.Printer.Print(output.Result{
				Data:  map[string]any{"name": name, "deleted": true},
				Table: &output.Table{Headers: []string{"name", "deleted"}, Rows: [][]string{{name, "true"}}},
				IDs:   []string{name},
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, cmdutil.YesFlagUsage)
	return cmd
}

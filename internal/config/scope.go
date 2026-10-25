package config

import (
	"errors"
	"fmt"

	"github.com/devlyft/cli/internal/clierr"
)

// Environment variables read by ResolveScope.
const (
	EnvContext  = "DEVLYFT_CONTEXT"
	EnvAPIURL   = "DEVLYFT_API_URL"
	EnvCABundle = "DEVLYFT_CA_BUNDLE"
	EnvOrg      = "DEVLYFT_ORG"
	EnvProject  = "DEVLYFT_PROJECT"
	EnvEnv      = "DEVLYFT_ENV"
	EnvLocation = "DEVLYFT_LOCATION"
)

// Source says where a resolved value came from.
type Source string

// Sources, highest precedence first.
const (
	SourceFlag    Source = "flag"
	SourceEnv     Source = "env"
	SourceContext Source = "context"
	SourceConfig  Source = "config"
	SourceDefault Source = "default"
	SourceUnset   Source = "unset"
)

// Value is a resolved setting and where it came from.
type Value struct {
	Value  string `json:"value"`
	Source Source `json:"source"`
}

// Flags are the global flags that feed the scope. Empty means not given.
type Flags struct {
	Context  string
	APIURL   string
	Org      string
	Project  string
	Env      string
	Location string
}

// Scope is what a command runs against. Org, project, env and location
// are names, slugs or IDs as the user gave them; package resolve turns
// them into IDs.
type Scope struct {
	Context  Value `json:"context"`
	APIURL   Value `json:"api_url"`
	CABundle Value `json:"ca_bundle"`
	Account  Value `json:"account"`
	Org      Value `json:"org"`
	Project  Value `json:"project"`
	Env      Value `json:"env"`
	Location Value `json:"location"`
}

// ResolveScope applies the precedence flag > environment variable >
// context (or config file) > default to every value.
func ResolveScope(f *File, flags *Flags, getenv func(string) string) (*Scope, error) {
	s := &Scope{}

	s.Context = pick(flags.Context, getenv(EnvContext), f.CurrentContext, SourceConfig, "")
	var ctx *Context
	if s.Context.Value != "" {
		c, ok := f.Contexts[s.Context.Value]
		if !ok {
			e := NoSuchContext(s.Context.Value)
			if s.Context.Source == SourceConfig {
				e.Hint = fmt.Sprintf("current_context in %s names a missing context; run `devlyft config set current_context \"\"` or `devlyft context use NAME`.", f.Path())
			}
			return nil, e
		}
		ctx = c
	}
	if ctx == nil {
		ctx = &Context{}
	}

	s.APIURL = pick(flags.APIURL, getenv(EnvAPIURL), f.APIURL, SourceConfig, DefaultAPIURL)
	u, err := NormalizeAPIURL(s.APIURL.Value)
	if err != nil {
		if e, ok := errors.AsType[*clierr.Error](err); ok && s.APIURL.Source != SourceFlag {
			e.Message += fmt.Sprintf(" (from %s)", describe(s.APIURL.Source, EnvAPIURL))
		}
		return nil, err
	}
	s.APIURL.Value = u

	s.CABundle = pick("", getenv(EnvCABundle), f.CABundle, SourceConfig, "")
	s.Account = pick("", "", ctx.Account, SourceContext, "")
	s.Org = pick(flags.Org, getenv(EnvOrg), ctx.Org, SourceContext, "")
	s.Project = pick(flags.Project, getenv(EnvProject), ctx.Project, SourceContext, "")
	s.Env = pick(flags.Env, getenv(EnvEnv), ctx.Env, SourceContext, "")
	s.Location = pick(flags.Location, getenv(EnvLocation), ctx.Location, SourceContext, "")
	return s, nil
}

func pick(flag, env, stored string, storedSource Source, def string) Value {
	switch {
	case flag != "":
		return Value{flag, SourceFlag}
	case env != "":
		return Value{env, SourceEnv}
	case stored != "":
		return Value{stored, storedSource}
	case def != "":
		return Value{def, SourceDefault}
	}
	return Value{"", SourceUnset}
}

func describe(src Source, envVar string) string {
	if src == SourceEnv {
		return envVar
	}
	return string(src)
}

// Missing returns the usage error for a scope value a command needs but
// that is not set anywhere.
func Missing(what, flag, envVar string) *clierr.Error {
	e := clierr.Usagef("no %s set: pass --%s or set %s", what, flag, envVar)
	e.Hint = fmt.Sprintf("Or save it in a context: `devlyft context use NAME --%s VALUE`.", flag)
	return e
}

package config

import (
	"testing"

	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/exitcode"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func testFile() *File {
	return &File{
		APIURL:         "https://config.example.com",
		CABundle:       "/etc/ca.pem",
		CurrentContext: "prod",
		Contexts: map[string]*Context{
			"prod":    {Account: "work", Org: "acme", Project: "web", Env: "production", Location: "eu-west"},
			"staging": {Org: "acme-staging", Project: "web", Env: "staging"},
		},
	}
}

// Every value follows flag > env > context (or config file) > default.
func TestResolveScopePrecedence(t *testing.T) {
	t.Parallel()

	type field func(*Scope) Value
	fields := map[string]struct {
		get                       field
		setFlag                   func(*Flags, string)
		envVar                    string
		fromFile, def             string
		fileSource, defaultSource Source
	}{
		"api_url": {
			get: func(s *Scope) Value { return s.APIURL }, setFlag: func(f *Flags, v string) { f.APIURL = v },
			envVar: EnvAPIURL, fromFile: "https://config.example.com", def: DefaultAPIURL,
			fileSource: SourceConfig, defaultSource: SourceDefault,
		},
		"org": {
			get: func(s *Scope) Value { return s.Org }, setFlag: func(f *Flags, v string) { f.Org = v },
			envVar: EnvOrg, fromFile: "acme", fileSource: SourceContext, defaultSource: SourceUnset,
		},
		"project": {
			get: func(s *Scope) Value { return s.Project }, setFlag: func(f *Flags, v string) { f.Project = v },
			envVar: EnvProject, fromFile: "web", fileSource: SourceContext, defaultSource: SourceUnset,
		},
		"env": {
			get: func(s *Scope) Value { return s.Env }, setFlag: func(f *Flags, v string) { f.Env = v },
			envVar: EnvEnv, fromFile: "production", fileSource: SourceContext, defaultSource: SourceUnset,
		},
		"location": {
			get: func(s *Scope) Value { return s.Location }, setFlag: func(f *Flags, v string) { f.Location = v },
			envVar: EnvLocation, fromFile: "eu-west", fileSource: SourceContext, defaultSource: SourceUnset,
		},
	}
	for name, fl := range fields {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			flagVal, envVal := "https://flag.example.com", "https://env.example.com"
			if name != "api_url" {
				flagVal, envVal = "from-flag", "from-env"
			}

			var flags Flags
			fl.setFlag(&flags, flagVal)
			check := func(desc string, s *Scope, err error, want Value) {
				t.Helper()
				if err != nil {
					t.Fatalf("%s: %v", desc, err)
				}
				if got := fl.get(s); got != want {
					t.Errorf("%s: got %+v, want %+v", desc, got, want)
				}
			}

			s, err := ResolveScope(testFile(), &flags, env(map[string]string{fl.envVar: envVal}))
			check("flag beats env", s, err, Value{flagVal, SourceFlag})

			s, err = ResolveScope(testFile(), &Flags{}, env(map[string]string{fl.envVar: envVal}))
			check("env beats file", s, err, Value{envVal, SourceEnv})

			s, err = ResolveScope(testFile(), &Flags{}, env(nil))
			check("file", s, err, Value{fl.fromFile, fl.fileSource})

			s, err = ResolveScope(&File{}, &Flags{}, env(nil))
			check("default", s, err, Value{fl.def, fl.defaultSource})
		})
	}
}

func TestResolveScopeContextSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		flags   Flags
		env     map[string]string
		file    *File
		context Value
		org     string
		account string
		exit    int
	}{
		{name: "current context", file: testFile(), context: Value{"prod", SourceConfig}, org: "acme", account: "work"},
		{name: "env picks context", env: map[string]string{EnvContext: "staging"}, file: testFile(), context: Value{"staging", SourceEnv}, org: "acme-staging"},
		{name: "flag beats env", flags: Flags{Context: "prod"}, env: map[string]string{EnvContext: "staging"}, file: testFile(), context: Value{"prod", SourceFlag}, org: "acme", account: "work"},
		{name: "no context", file: &File{}, context: Value{"", SourceUnset}},
		{name: "unknown flag context", flags: Flags{Context: "nope"}, file: testFile(), exit: exitcode.NotFound},
		{name: "dangling current context", file: &File{CurrentContext: "gone"}, exit: exitcode.NotFound},
		{name: "invalid api url from env", env: map[string]string{EnvAPIURL: "http://api.example.com"}, file: &File{}, exit: exitcode.Usage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, err := ResolveScope(tt.file, &tt.flags, env(tt.env))
			if got := clierr.ExitCode(err); got != tt.exit {
				t.Fatalf("exit = %d (%v), want %d", got, err, tt.exit)
			}
			if err != nil {
				return
			}
			if s.Context != tt.context || s.Org.Value != tt.org || s.Account.Value != tt.account {
				t.Errorf("got context %+v org %q account %q", s.Context, s.Org.Value, s.Account.Value)
			}
		})
	}
}

func TestResolveScopeCABundle(t *testing.T) {
	t.Parallel()

	s, err := ResolveScope(testFile(), &Flags{}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.CABundle != (Value{"/etc/ca.pem", SourceConfig}) {
		t.Errorf("CABundle = %+v", s.CABundle)
	}
	s, err = ResolveScope(testFile(), &Flags{}, env(map[string]string{EnvCABundle: "/tmp/ca.pem"}))
	if err != nil {
		t.Fatal(err)
	}
	if s.CABundle != (Value{"/tmp/ca.pem", SourceEnv}) {
		t.Errorf("CABundle = %+v", s.CABundle)
	}
}

func TestMissing(t *testing.T) {
	t.Parallel()

	e := Missing("project", "project", EnvProject)
	if e.ExitCode() != exitcode.Usage || e.Hint == "" {
		t.Errorf("unexpected %+v", e)
	}
}

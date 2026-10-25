package resolve

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/devlyft/cli/internal/api"
	"github.com/devlyft/cli/internal/api/mock"
	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/exitcode"
)

var (
	acme      = api.Organization{Id: uuid.MustParse("11111111-1111-4111-8111-111111111111"), Name: "Acme", Slug: "acme"}
	acmeTwin  = api.Organization{Id: uuid.MustParse("22222222-2222-4222-8222-222222222222"), Name: "Acme", Slug: "acme-eu"}
	globex    = api.Organization{Id: uuid.MustParse("33333333-3333-4333-8333-333333333333"), Name: "Globex", Slug: "acme-labs"}
	web       = api.Project{Id: uuid.MustParse("44444444-4444-4444-8444-444444444444"), Name: "web"}
	webCopy   = api.Project{Id: uuid.MustParse("55555555-5555-4555-8555-555555555555"), Name: "Web"}
	api2      = api.Project{Id: uuid.MustParse("66666666-6666-4666-8666-666666666666"), Name: "api"}
	prod      = api.Environment{Id: uuid.MustParse("77777777-7777-4777-8777-777777777777"), Name: "production"}
	locations = []api.Location{{Id: "eu-west-1"}, {Id: "us-east-1"}}
)

func newResolver(t *testing.T) (*Resolver, *mock.MockClient) {
	t.Helper()
	m := mock.NewMockClient(gomock.NewController(t))
	return &Resolver{Client: m}, m
}

func TestOrg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ref  string
		want string
		exit int
	}{
		{ref: acme.Id.String(), want: acme.Id.String()},
		{ref: "acme", want: acme.Id.String()},
		{ref: "acme-labs", want: globex.Id.String()}, // slug wins
		{ref: "Globex", want: globex.Id.String()},
		{ref: "globex", want: globex.Id.String()}, // case-insensitive fallback
		{ref: "Acme", exit: exitcode.Usage},       // two orgs share the name
		{ref: "initech", exit: exitcode.NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			t.Parallel()
			r, m := newResolver(t)
			if !IsID(tt.ref) {
				m.EXPECT().ListOrganizations(gomock.Any()).Return([]api.Organization{acme, acmeTwin, globex}, nil)
			}
			got, err := r.Org(t.Context(), tt.ref)
			if code := clierr.ExitCode(err); code != tt.exit {
				t.Fatalf("exit = %d (%v), want %d", code, err, tt.exit)
			}
			if got != tt.want {
				t.Errorf("Org(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestAmbiguityListsMatches(t *testing.T) {
	t.Parallel()

	r, m := newResolver(t)
	m.EXPECT().ListOrganizations(gomock.Any()).Return([]api.Organization{acmeTwin, acme}, nil)
	_, err := r.Org(t.Context(), "Acme")
	e, ok := errors.AsType[*clierr.Error](err)
	if !ok {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{acme.Id.String(), acmeTwin.Id.String(), "matches 2"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("message %q lacks %q", e.Message, want)
		}
	}
	if !strings.Contains(e.Hint, "--org") || e.Details["matches"] == nil {
		t.Errorf("hint %q details %v", e.Hint, e.Details)
	}
}

func TestProject(t *testing.T) {
	t.Parallel()

	t.Run("id needs no org", func(t *testing.T) {
		t.Parallel()
		r, _ := newResolver(t)
		got, err := r.Project(t.Context(), "", web.Id.String())
		if err != nil || got != web.Id.String() {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("name needs org", func(t *testing.T) {
		t.Parallel()
		r, _ := newResolver(t)
		_, err := r.Project(t.Context(), "", "web")
		if clierr.ExitCode(err) != exitcode.Usage {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("name in org by slug", func(t *testing.T) {
		t.Parallel()
		r, m := newResolver(t)
		m.EXPECT().ListOrganizations(gomock.Any()).Return([]api.Organization{acme}, nil)
		m.EXPECT().ListProjects(gomock.Any(), acme.Id.String()).Return([]api.Project{web, api2}, nil)
		got, err := r.Project(t.Context(), "acme", "api")
		if err != nil || got != api2.Id.String() {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("exact name beats case-insensitive", func(t *testing.T) {
		t.Parallel()
		r, m := newResolver(t)
		m.EXPECT().ListProjects(gomock.Any(), acme.Id.String()).Return([]api.Project{web, webCopy}, nil)
		got, err := r.Project(t.Context(), acme.Id.String(), "Web")
		if err != nil || got != webCopy.Id.String() {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("case-insensitive ambiguity", func(t *testing.T) {
		t.Parallel()
		r, m := newResolver(t)
		m.EXPECT().ListProjects(gomock.Any(), acme.Id.String()).Return([]api.Project{web, webCopy}, nil)
		_, err := r.Project(t.Context(), acme.Id.String(), "WEB")
		if clierr.ExitCode(err) != exitcode.Usage {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("list error passes through", func(t *testing.T) {
		t.Parallel()
		r, m := newResolver(t)
		m.EXPECT().ListProjects(gomock.Any(), acme.Id.String()).Return(nil, clierr.FromHTTP(403, "forbidden", ""))
		_, err := r.Project(t.Context(), acme.Id.String(), "web")
		if clierr.ExitCode(err) != exitcode.Auth {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestEnv(t *testing.T) {
	t.Parallel()

	t.Run("id", func(t *testing.T) {
		t.Parallel()
		r, _ := newResolver(t)
		got, err := r.Env(t.Context(), "", "", prod.Id.String())
		if err != nil || got != prod.Id.String() {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("name needs project", func(t *testing.T) {
		t.Parallel()
		r, _ := newResolver(t)
		_, err := r.Env(t.Context(), "acme", "", "production")
		if clierr.ExitCode(err) != exitcode.Usage {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("name in project id", func(t *testing.T) {
		t.Parallel()
		r, m := newResolver(t)
		m.EXPECT().ListEnvironments(gomock.Any(), web.Id.String()).Return([]api.Environment{prod}, nil)
		got, err := r.Env(t.Context(), "", web.Id.String(), "production")
		if err != nil || got != prod.Id.String() {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		r, m := newResolver(t)
		m.EXPECT().ListEnvironments(gomock.Any(), web.Id.String()).Return([]api.Environment{prod}, nil)
		_, err := r.Env(t.Context(), "", web.Id.String(), "staging")
		if clierr.ExitCode(err) != exitcode.NotFound {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestLocation(t *testing.T) {
	t.Parallel()

	r, m := newResolver(t)
	m.EXPECT().ListLocations(gomock.Any()).Return(locations, nil).Times(2)
	got, err := r.Location(t.Context(), "EU-WEST-1")
	if err != nil || got != "eu-west-1" {
		t.Fatalf("got %q, %v", got, err)
	}
	_, err = r.Location(t.Context(), "mars-1")
	e, ok := errors.AsType[*clierr.Error](err)
	if !ok || e.ExitCode() != exitcode.NotFound || !strings.Contains(e.Hint, "eu-west-1, us-east-1") {
		t.Fatalf("err = %+v", err)
	}
}

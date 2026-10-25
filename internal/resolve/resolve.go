// Package resolve turns the names, slugs and IDs users type into the IDs
// the API takes. Names are looked up with list calls on every invocation;
// nothing is cached. A name that matches several resources is an error
// that lists them, since organization, project and environment names are
// not guaranteed to be unique; scripts should pass IDs.
package resolve

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/devlyft/cli/internal/api"
	"github.com/devlyft/cli/internal/clierr"
)

// Match is one resource a name could refer to.
type Match struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Resolver resolves references through the API.
type Resolver struct {
	Client api.Client
}

// IsID reports whether ref is a UUID, which is used as-is without a lookup.
func IsID(ref string) bool {
	_, err := uuid.Parse(ref)
	return err == nil
}

// Org resolves an organization by ID, slug or name. Slugs are unique, so a
// slug match wins over name matches.
func (r *Resolver) Org(ctx context.Context, ref string) (string, error) {
	if IsID(ref) {
		return ref, nil
	}
	orgs, err := r.Client.ListOrganizations(ctx)
	if err != nil {
		return "", err
	}
	for i := range orgs {
		if orgs[i].Slug == ref {
			return orgs[i].Id.String(), nil
		}
	}
	matches := make([]Match, 0, 1)
	for i := range orgs {
		matches = append(matches, Match{ID: orgs[i].Id.String(), Name: orgs[i].Name})
	}
	return pick("organization", "org", ref, matches)
}

// Project resolves a project by ID, or by name within orgRef. orgRef may
// be empty when ref is an ID.
func (r *Resolver) Project(ctx context.Context, orgRef, ref string) (string, error) {
	if IsID(ref) {
		return ref, nil
	}
	if orgRef == "" {
		e := clierr.Usagef("project %q is a name, so an organization is needed to find it: pass --org or set DEVLYFT_ORG", ref)
		e.Hint = "Or pass the project ID."
		return "", e
	}
	orgID, err := r.Org(ctx, orgRef)
	if err != nil {
		return "", err
	}
	projects, err := r.Client.ListProjects(ctx, orgID)
	if err != nil {
		return "", err
	}
	matches := make([]Match, 0, len(projects))
	for i := range projects {
		matches = append(matches, Match{ID: projects[i].Id.String(), Name: projects[i].Name})
	}
	return pick("project", "project", ref, matches)
}

// Env resolves an environment by ID, or by name within the project
// (resolved with orgRef when it is a name).
func (r *Resolver) Env(ctx context.Context, orgRef, projectRef, ref string) (string, error) {
	if IsID(ref) {
		return ref, nil
	}
	if projectRef == "" {
		e := clierr.Usagef("environment %q is a name, so a project is needed to find it: pass --project or set DEVLYFT_PROJECT", ref)
		e.Hint = "Or pass the environment ID."
		return "", e
	}
	projectID, err := r.Project(ctx, orgRef, projectRef)
	if err != nil {
		return "", err
	}
	envs, err := r.Client.ListEnvironments(ctx, projectID)
	if err != nil {
		return "", err
	}
	matches := make([]Match, 0, len(envs))
	for i := range envs {
		matches = append(matches, Match{ID: envs[i].Id.String(), Name: envs[i].Name})
	}
	return pick("environment", "environment", ref, matches)
}

// Location validates a location ID (a region name), ignoring case.
func (r *Resolver) Location(ctx context.Context, ref string) (string, error) {
	locs, err := r.Client.ListLocations(ctx)
	if err != nil {
		return "", err
	}
	ids := make([]string, 0, len(locs))
	for i := range locs {
		if strings.EqualFold(locs[i].Id, ref) {
			return locs[i].Id, nil
		}
		ids = append(ids, locs[i].Id)
	}
	sort.Strings(ids)
	e := clierr.New(clierr.CodeNotFound, "location %q not found", ref)
	if len(ids) > 0 {
		e.Hint = "Available locations: " + strings.Join(ids, ", ") + "."
	}
	return "", e
}

// pick finds ref among candidates by exact name, then by case-insensitive
// name.
func pick(kind, flag, ref string, candidates []Match) (string, error) {
	for _, eq := range []func(a, b string) bool{
		func(a, b string) bool { return a == b },
		strings.EqualFold,
	} {
		var found []Match
		for _, m := range candidates {
			if eq(m.Name, ref) {
				found = append(found, m)
			}
		}
		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0].ID, nil
		}
		return "", ambiguous(kind, flag, ref, found)
	}
	return "", clierr.New(clierr.CodeNotFound, "%s %q not found", kind, ref)
}

func ambiguous(kind, flag, ref string, found []Match) error {
	sort.Slice(found, func(i, j int) bool { return found[i].ID < found[j].ID })
	lines := make([]string, len(found))
	for i, m := range found {
		lines[i] = fmt.Sprintf("  %s  %s", m.ID, m.Name)
	}
	e := clierr.Usagef("%s name %q matches %d resources:\n%s", kind, ref, len(found), strings.Join(lines, "\n"))
	e.Hint = fmt.Sprintf("Pass the ID with --%s; scripts should always use IDs.", flag)
	e.Details = map[string]any{"matches": found}
	return e
}

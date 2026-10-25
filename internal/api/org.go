package api

import (
	"context"

	"github.com/devlyft/cli/internal/api/gen/org"
	"github.com/devlyft/cli/internal/clierr"
)

// Organizations, projects, environments, secrets, pods and locations are
// served by org-api. Pods are read through org-api, which authorizes the
// caller; the CLI never talks to the pods service directly.

func (c *client) ListOrganizations(ctx context.Context) ([]Organization, error) {
	ctx, n := withAttempts(ctx)
	r, err := c.org.GetV1OrganizationsWithResponse(ctx)
	if err != nil {
		return nil, c.transportError(err)
	}
	v, err := expect(r.HTTPResponse, r.Body, r.JSON200, *n)
	if err != nil {
		return nil, err
	}
	return v.Organizations, nil
}

func (c *client) GetOrganization(ctx context.Context, orgID string) (*Organization, error) {
	id, err := parseID("organization", orgID)
	if err != nil {
		return nil, err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.org.GetV1OrganizationsOrganizationIdWithResponse(ctx, id)
	if err != nil {
		return nil, c.transportError(err)
	}
	return expect(r.HTTPResponse, r.Body, r.JSON200, *n)
}

func (c *client) ListProjects(ctx context.Context, orgID string) ([]Project, error) {
	id, err := parseID("organization", orgID)
	if err != nil {
		return nil, err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.org.GetV1OrganizationsOrganizationIdProjectsWithResponse(ctx, id)
	if err != nil {
		return nil, c.transportError(err)
	}
	v, err := expect(r.HTTPResponse, r.Body, r.JSON200, *n)
	if err != nil {
		return nil, err
	}
	return v.Projects, nil
}

func (c *client) GetProject(ctx context.Context, projectID string) (*Project, error) {
	id, err := parseID("project", projectID)
	if err != nil {
		return nil, err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.org.GetV1ProjectsProjectIdWithResponse(ctx, id)
	if err != nil {
		return nil, c.transportError(err)
	}
	return expect(r.HTTPResponse, r.Body, r.JSON200, *n)
}

func (c *client) ListEnvironments(ctx context.Context, projectID string) ([]Environment, error) {
	id, err := parseID("project", projectID)
	if err != nil {
		return nil, err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.org.GetV1ProjectsProjectIdEnvironmentsWithResponse(ctx, id)
	if err != nil {
		return nil, c.transportError(err)
	}
	v, err := expect(r.HTTPResponse, r.Body, r.JSON200, *n)
	if err != nil {
		return nil, err
	}
	return v.Environments, nil
}

func (c *client) GetEnvironment(ctx context.Context, envID string) (*Environment, error) {
	id, err := parseID("environment", envID)
	if err != nil {
		return nil, err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.org.GetV1EnvironmentsEnvironmentIdWithResponse(ctx, id)
	if err != nil {
		return nil, c.transportError(err)
	}
	return expect(r.HTTPResponse, r.Body, r.JSON200, *n)
}

func (c *client) ListLocations(ctx context.Context) ([]Location, error) {
	ctx, n := withAttempts(ctx)
	r, err := c.org.GetV1LocationsWithResponse(ctx)
	if err != nil {
		return nil, c.transportError(err)
	}
	v, err := expect(r.HTTPResponse, r.Body, r.JSON200, *n)
	if err != nil {
		return nil, err
	}
	return v.Locations, nil
}

func (c *client) ListSecrets(ctx context.Context, projectID, envID string, limit int) ([]EnvironmentSecret, error) {
	pid, err := parseID("project", projectID)
	if err != nil {
		return nil, err
	}
	eid, err := parseID("environment", envID)
	if err != nil {
		return nil, err
	}
	return Paged(ctx, limit, func(ctx context.Context, p Page) ([]EnvironmentSecret, int, error) {
		ctx, n := withAttempts(ctx)
		params := &org.ListEnvironmentSecretsV1Params{Limit: int32Ptr(p.Limit), Offset: int32Ptr(p.Offset)}
		r, err := c.org.ListEnvironmentSecretsV1WithResponse(ctx, pid, eid, params)
		if err != nil {
			return nil, 0, c.transportError(err)
		}
		v, err := expect(r.HTTPResponse, r.Body, r.JSON200, *n)
		if err != nil {
			return nil, 0, err
		}
		return v.Secrets, int(v.Total), nil
	})
}

func (c *client) SetSecret(ctx context.Context, projectID, envID, name, value string) error {
	pid, err := parseID("project", projectID)
	if err != nil {
		return err
	}
	eid, err := parseID("environment", envID)
	if err != nil {
		return err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.org.CreateEnvironmentSecretV1WithResponse(ctx, pid, eid, name, org.CreateEnvironmentSecretV1JSONRequestBody{Value: value})
	if err != nil {
		return c.transportError(err)
	}
	return checkStatus(r.HTTPResponse, r.Body, *n)
}

func (c *client) DeleteSecret(ctx context.Context, projectID, envID, name string) error {
	pid, err := parseID("project", projectID)
	if err != nil {
		return err
	}
	eid, err := parseID("environment", envID)
	if err != nil {
		return err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.org.DeleteEnvironmentSecretV1WithResponse(ctx, pid, eid, name)
	if err != nil {
		return c.transportError(err)
	}
	return checkStatus(r.HTTPResponse, r.Body, *n)
}

// PodScope selects the pods to list: set exactly one ID. The most specific
// one wins if several are set.
type PodScope struct {
	ProjectID     string
	EnvironmentID string
	DeployID      string
}

func (c *client) ListPods(ctx context.Context, scope PodScope, limit int) ([]Pod, error) {
	var fetch PageFunc[Pod]
	switch {
	case scope.DeployID != "":
		id, err := parseID("deploy", scope.DeployID)
		if err != nil {
			return nil, err
		}
		fetch = func(ctx context.Context, p Page) ([]Pod, int, error) {
			ctx, n := withAttempts(ctx)
			r, err := c.org.GetV1DeploysDeployIdPodsWithResponse(ctx, id, &org.GetV1DeploysDeployIdPodsParams{Limit: int32Ptr(p.Limit), Offset: int32Ptr(p.Offset)})
			if err != nil {
				return nil, 0, c.transportError(err)
			}
			return podsPage(r.HTTPResponse, r.Body, r.JSON200, *n)
		}
	case scope.EnvironmentID != "":
		id, err := parseID("environment", scope.EnvironmentID)
		if err != nil {
			return nil, err
		}
		fetch = func(ctx context.Context, p Page) ([]Pod, int, error) {
			ctx, n := withAttempts(ctx)
			r, err := c.org.GetV1EnvironmentsEnvironmentIdPodsWithResponse(ctx, id, &org.GetV1EnvironmentsEnvironmentIdPodsParams{Limit: int32Ptr(p.Limit), Offset: int32Ptr(p.Offset)})
			if err != nil {
				return nil, 0, c.transportError(err)
			}
			return podsPage(r.HTTPResponse, r.Body, r.JSON200, *n)
		}
	case scope.ProjectID != "":
		id, err := parseID("project", scope.ProjectID)
		if err != nil {
			return nil, err
		}
		fetch = func(ctx context.Context, p Page) ([]Pod, int, error) {
			ctx, n := withAttempts(ctx)
			r, err := c.org.GetV1ProjectsProjectIdPodsWithResponse(ctx, id, &org.GetV1ProjectsProjectIdPodsParams{Limit: int32Ptr(p.Limit), Offset: int32Ptr(p.Offset)})
			if err != nil {
				return nil, 0, c.transportError(err)
			}
			return podsPage(r.HTTPResponse, r.Body, r.JSON200, *n)
		}
	default:
		return nil, clierr.Usagef("%v", errNoScope)
	}
	return Paged(ctx, limit, fetch)
}

func podsPage(resp *httpResponse, body []byte, v *org.ListPodsResponse, attempts int) ([]Pod, int, error) {
	v, err := expect(resp, body, v, attempts)
	if err != nil {
		return nil, 0, err
	}
	return v.Results, int(v.Total), nil
}

func (c *client) GetPod(ctx context.Context, podID string) (*Pod, error) {
	ctx, n := withAttempts(ctx)
	r, err := c.org.GetV1PodsPodIdWithResponse(ctx, podID)
	if err != nil {
		return nil, c.transportError(err)
	}
	return expect(r.HTTPResponse, r.Body, r.JSON200, *n)
}

func int32Ptr(v int) *int32 {
	i := int32(min(v, MaxPageSize*1000)) //nolint:gosec // bounded above
	return &i
}

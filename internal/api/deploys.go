package api

import (
	"context"
	"net/http"

	"github.com/devlyft/cli/internal/api/gen/deploys"
)

// httpResponse shortens signatures of helpers over generated responses.
type httpResponse = http.Response

// Builds, deploys, routes and custom domains are served by deploys-api.

func (c *client) ListBuilds(ctx context.Context, projectID string) ([]Build, error) {
	id, err := parseID("project", projectID)
	if err != nil {
		return nil, err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.deploys.GetV1ProjectsProjectIdBuildsWithResponse(ctx, id)
	if err != nil {
		return nil, c.transportError(err)
	}
	v, err := expect(r.HTTPResponse, r.Body, r.JSON200, *n)
	if err != nil {
		return nil, err
	}
	return v.Builds, nil
}

func (c *client) GetBuild(ctx context.Context, buildID string) (*Build, error) {
	id, err := parseID("build", buildID)
	if err != nil {
		return nil, err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.deploys.GetV1BuildsBuildIdWithResponse(ctx, id)
	if err != nil {
		return nil, c.transportError(err)
	}
	return expect(r.HTTPResponse, r.Body, r.JSON200, *n)
}

func (c *client) CreateBuild(ctx context.Context, projectID string, req *CreateBuildRequest) (*Build, error) {
	id, err := parseID("project", projectID)
	if err != nil {
		return nil, err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.deploys.PostV1ProjectsProjectIdBuildsWithResponse(ctx, id, deploys.PostV1ProjectsProjectIdBuildsJSONRequestBody(*req))
	if err != nil {
		return nil, c.transportError(err)
	}
	return expect(r.HTTPResponse, r.Body, r.JSON201, *n)
}

func (c *client) ListDeploys(ctx context.Context, projectID, envID string) ([]Deploy, error) {
	pid, err := parseID("project", projectID)
	if err != nil {
		return nil, err
	}
	eid, err := parseID("environment", envID)
	if err != nil {
		return nil, err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.deploys.GetV1ProjectsProjectIdEnvironmentsEnvironmentIdDeploysWithResponse(ctx, pid, eid)
	if err != nil {
		return nil, c.transportError(err)
	}
	v, err := expect(r.HTTPResponse, r.Body, r.JSON200, *n)
	if err != nil {
		return nil, err
	}
	return v.Deploys, nil
}

func (c *client) GetDeploy(ctx context.Context, deployID string) (*Deploy, error) {
	d, _, err := c.getDeploy(ctx, deployID)
	return d, err
}

// getDeploy also returns the response's ETag, if the server sent one.
func (c *client) getDeploy(ctx context.Context, deployID string) (*Deploy, string, error) {
	id, err := parseID("deploy", deployID)
	if err != nil {
		return nil, "", err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.deploys.GetV1DeploysDeployIdWithResponse(ctx, id)
	if err != nil {
		return nil, "", c.transportError(err)
	}
	d, err := expect(r.HTTPResponse, r.Body, r.JSON200, *n)
	if err != nil {
		return nil, "", err
	}
	return d, r.HTTPResponse.Header.Get("ETag"), nil
}

func (c *client) CreateDeploy(ctx context.Context, projectID, envID string, req *CreateDeployRequest) (*Deploy, error) {
	pid, err := parseID("project", projectID)
	if err != nil {
		return nil, err
	}
	eid, err := parseID("environment", envID)
	if err != nil {
		return nil, err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.deploys.PostV1ProjectsProjectIdEnvironmentsEnvironmentIdDeploysWithResponse(ctx, pid, eid, deploys.PostV1ProjectsProjectIdEnvironmentsEnvironmentIdDeploysJSONRequestBody(*req))
	if err != nil {
		return nil, c.transportError(err)
	}
	return expect(r.HTTPResponse, r.Body, r.JSON201, *n)
}

// UpdateDeploy is a read-modify-write: the API replaces the whole deploy
// and requires build_id, name and location_id on every update. When the
// GET returns an ETag the PUT sends it as If-Match, so a concurrent change
// fails with a conflict instead of being overwritten.
func (c *client) UpdateDeploy(ctx context.Context, deployID string, mutate func(*UpdateDeployRequest) error) (*Deploy, error) {
	cur, etag, err := c.getDeploy(ctx, deployID)
	if err != nil {
		return nil, err
	}
	req := UpdateRequestFromDeploy(cur)
	if err := mutate(&req); err != nil {
		return nil, err
	}
	var editors []deploys.RequestEditorFn
	if etag != "" {
		editors = append(editors, func(_ context.Context, r *http.Request) error {
			r.Header.Set("If-Match", etag)
			return nil
		})
	}
	ctx, n := withAttempts(ctx)
	r, err := c.deploys.PutV1DeploysDeployIdWithResponse(ctx, cur.Id, deploys.PutV1DeploysDeployIdJSONRequestBody(req), editors...)
	if err != nil {
		return nil, c.transportError(err)
	}
	return expect(r.HTTPResponse, r.Body, r.JSON200, *n)
}

// UpdateRequestFromDeploy returns the update request that leaves d as it
// is. `deploy --dry-run` diffs against it.
func UpdateRequestFromDeploy(d *Deploy) UpdateDeployRequest {
	req := UpdateDeployRequest{
		BuildId:                d.BuildId,
		Name:                   d.Name,
		LocationId:             d.LocationId,
		Env:                    d.Env,
		EnableHpa:              d.EnableHpa,
		Replicas:               d.Replicas,
		HpaMinReplicas:         d.HpaMinReplicas,
		HpaMaxReplicas:         d.HpaMaxReplicas,
		HpaTargetCpu:           d.HpaTargetCpu,
		InstanceType:           d.InstanceType,
		RegistryCredentialName: d.RegistryCredentialName,
	}
	expose := d.Expose
	req.Expose = &expose
	if d.TenancyType != "" {
		tenancy := d.TenancyType
		req.TenancyType = &tenancy
	}
	if d.EnableHpa {
		// The API rejects replicas alongside autoscaling.
		req.Replicas = 0
	}
	return req
}

func (c *client) DeleteDeploy(ctx context.Context, deployID string) error {
	id, err := parseID("deploy", deployID)
	if err != nil {
		return err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.deploys.DeleteV1DeploysDeployIdWithResponse(ctx, id)
	if err != nil {
		return c.transportError(err)
	}
	return checkStatus(r.HTTPResponse, r.Body, *n)
}

func (c *client) ListRoutes(ctx context.Context, deployID string) ([]Route, error) {
	id, err := parseID("deploy", deployID)
	if err != nil {
		return nil, err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.deploys.GetV1DeploysDeployIdRoutesWithResponse(ctx, id)
	if err != nil {
		return nil, c.transportError(err)
	}
	v, err := expect(r.HTTPResponse, r.Body, r.JSON200, *n)
	if err != nil {
		return nil, err
	}
	return v.Routes, nil
}

func (c *client) CreateRoute(ctx context.Context, deployID string, req CreateRouteRequest) (*Route, error) {
	id, err := parseID("deploy", deployID)
	if err != nil {
		return nil, err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.deploys.PostV1DeploysDeployIdRoutesWithResponse(ctx, id, deploys.PostV1DeploysDeployIdRoutesJSONRequestBody(req))
	if err != nil {
		return nil, c.transportError(err)
	}
	return expect(r.HTTPResponse, r.Body, r.JSON201, *n)
}

func (c *client) DeleteRoute(ctx context.Context, deployID, routeID string) error {
	did, err := parseID("deploy", deployID)
	if err != nil {
		return err
	}
	rid, err := parseID("route", routeID)
	if err != nil {
		return err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.deploys.DeleteV1DeploysDeployIdRoutesRouteIdWithResponse(ctx, did, rid)
	if err != nil {
		return c.transportError(err)
	}
	return checkStatus(r.HTTPResponse, r.Body, *n)
}

func (c *client) ListDomains(ctx context.Context, orgID string) ([]CustomDomain, error) {
	id, err := parseID("organization", orgID)
	if err != nil {
		return nil, err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.deploys.GetV1OrganizationsOrganizationIdDomainsWithResponse(ctx, id)
	if err != nil {
		return nil, c.transportError(err)
	}
	v, err := expect(r.HTTPResponse, r.Body, r.JSON200, *n)
	if err != nil {
		return nil, err
	}
	return v.Domains, nil
}

func (c *client) CreateDomain(ctx context.Context, orgID, domain string) (*CustomDomain, error) {
	id, err := parseID("organization", orgID)
	if err != nil {
		return nil, err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.deploys.PostV1OrganizationsOrganizationIdDomainsWithResponse(ctx, id, deploys.PostV1OrganizationsOrganizationIdDomainsJSONRequestBody{Domain: domain})
	if err != nil {
		return nil, c.transportError(err)
	}
	return expect(r.HTTPResponse, r.Body, r.JSON201, *n)
}

func (c *client) VerifyDomain(ctx context.Context, orgID, domain string) (*CustomDomain, error) {
	id, err := parseID("organization", orgID)
	if err != nil {
		return nil, err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.deploys.PostV1OrganizationsOrganizationIdDomainsDomainVerifyWithResponse(ctx, id, domain)
	if err != nil {
		return nil, c.transportError(err)
	}
	return expect(r.HTTPResponse, r.Body, r.JSON200, *n)
}

func (c *client) DeleteDomain(ctx context.Context, orgID, domain string) error {
	id, err := parseID("organization", orgID)
	if err != nil {
		return err
	}
	ctx, n := withAttempts(ctx)
	r, err := c.deploys.DeleteV1OrganizationsOrganizationIdDomainsDomainWithResponse(ctx, id, domain)
	if err != nil {
		return c.transportError(err)
	}
	return checkStatus(r.HTTPResponse, r.Body, *n)
}

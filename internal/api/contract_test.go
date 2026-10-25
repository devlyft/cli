package api

import (
	"slices"
	"testing"

	"github.com/devlyft/cli/internal/testutil/apitest"
)

// Contract tests: every request the facade sends must match an operation
// in the vendored specs, and the canned responses must match them too, so a
// spec update that breaks the CLI fails here.

const (
	uuidA = "0b4e1a3c-6c2f-4d0a-8c51-5f0f1e2d3c4b"
	ts    = `"2026-10-01T00:00:00Z"`
)

var (
	orgJSON     = `{"id":"` + uuidA + `","name":"Acme","slug":"acme","created_at":` + ts + `,"updated_at":` + ts + `}`
	projectJSON = `{"id":"` + uuidA + `","organization_id":"` + uuidA + `","name":"web","created_at":` + ts + `,"updated_at":` + ts + `}`
	envJSON     = `{"id":"` + uuidA + `","project_id":"` + uuidA + `","name":"production","tenancy_type":"SHARED","created_at":` + ts + `,"updated_at":` + ts + `}`
	podJSON     = `{"id":"p-1","name":"web-abc","phase":"Running","location_id":"eu-west","cluster_name":"edge-1","project_id":"` + uuidA + `","environment_id":"` + uuidA + `","deploy_id":"` + uuidA + `"}`
	buildJSON   = `{"id":"` + uuidA + `","project_id":"` + uuidA + `","name":"web-1.4.2","image":"ghcr.io/acme/web","tag":"1.4.2","cpu_request":"250m","memory_request":"512Mi","container_port":8080,"created_at":` + ts + `,"updated_at":` + ts + `}`
	routeJSON   = `{"id":"` + uuidA + `","deploy_id":"` + uuidA + `","hostname":"www.acme.dev","path_prefix":"/","created_at":` + ts + `,"updated_at":` + ts + `}`
	domainJSON  = `{"id":"` + uuidA + `","organization_id":"` + uuidA + `","domain":"acme.dev","status":"pending","verification_token":"t","challenge_record_name":"_devlyft-challenge.acme.dev","verified_at":null,"last_checked_at":null,"created_at":` + ts + `,"updated_at":` + ts + `}`
	metricsJSON = `{"metric":"cpu","series":[{"labels":{"pod":"web-abc"},"points":[{"timestamp":` + ts + `,"value":0.5}]}]}`
)

// responses are canned bodies by operation ID.
//
//nolint:gosec // fixtures, not credentials
var responses = map[string]string{
	"GetV1Organizations":                                      `{"organizations":[` + orgJSON + `]}`,
	"GetV1OrganizationsOrganizationId":                        orgJSON,
	"GetV1OrganizationsOrganizationIdProjects":                `{"projects":[` + projectJSON + `]}`,
	"GetV1ProjectsProjectId":                                  projectJSON,
	"GetV1ProjectsProjectIdEnvironments":                      `{"environments":[` + envJSON + `]}`,
	"GetV1EnvironmentsEnvironmentId":                          envJSON,
	"GetV1Locations":                                          `{"locations":[{"id":"eu-west"}]}`,
	"ListEnvironmentSecretsV1":                                `{"secrets":[{"key":"DB_URL"}],"limit":25,"offset":0,"total":1}`,
	"CreateEnvironmentSecretV1":                               `{"key":"DB_URL"}`,
	"DeleteEnvironmentSecretV1":                               ``,
	"GetV1ProjectsProjectIdPods":                              `{"results":[` + podJSON + `],"limit":25,"offset":0,"total":1}`,
	"GetV1EnvironmentsEnvironmentIdPods":                      `{"results":[` + podJSON + `],"limit":25,"offset":0,"total":1}`,
	"GetV1DeploysDeployIdPods":                                `{"results":[` + podJSON + `],"limit":25,"offset":0,"total":1}`,
	"GetV1PodsPodId":                                          podJSON,
	"GetV1ProjectsProjectIdBuilds":                            `{"builds":[` + buildJSON + `]}`,
	"GetV1BuildsBuildId":                                      buildJSON,
	"PostV1ProjectsProjectIdBuilds":                           buildJSON,
	"GetV1ProjectsProjectIdEnvironmentsEnvironmentIdDeploys":  `{"deploys":[` + deployJSON + `]}`,
	"GetV1DeploysDeployId":                                    deployJSON,
	"PostV1ProjectsProjectIdEnvironmentsEnvironmentIdDeploys": deployJSON,
	"PutV1DeploysDeployId":                                    deployJSON,
	"DeleteV1DeploysDeployId":                                 ``,
	"GetV1DeploysDeployIdRoutes":                              `{"routes":[` + routeJSON + `]}`,
	"PostV1DeploysDeployIdRoutes":                             routeJSON,
	"DeleteV1DeploysDeployIdRoutesRouteId":                    ``,
	"GetV1OrganizationsOrganizationIdDomains":                 `{"domains":[` + domainJSON + `]}`,
	"PostV1OrganizationsOrganizationIdDomains":                domainJSON,
	"PostV1OrganizationsOrganizationIdDomainsDomainVerify":    domainJSON,
	"DeleteV1OrganizationsOrganizationIdDomainsDomain":        ``,
	"GetV1DeploysDeployIdLogs":                                `{"entries":[{"timestamp":` + ts + `,"line":"hello","pod":"web-abc"}],"cursor":` + ts + `}`,
	"GetV1MetricTypes":                                        `{"metrics":["cpu","memory"]}`,
	"GetV1DeploysDeployIdMetrics":                             metricsJSON,
	"GetV1PodsPodIdMetrics":                                   metricsJSON,
}

// canned turns the response bodies into apitest responses.
func canned() map[string]apitest.Response {
	out := make(map[string]apitest.Response, len(responses))
	for id, body := range responses {
		out[id] = apitest.Response{Body: body}
	}
	return out
}

func TestContract(t *testing.T) {
	t.Parallel()

	srv := apitest.NewServer(t, canned())
	c, err := New(Options{BaseURL: srv.URL, Tokens: StaticToken("t"), HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	check := func(name string, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	second := func(_ any, err error) error { return err }

	check("ListOrganizations", second(c.ListOrganizations(ctx)))
	check("GetOrganization", second(c.GetOrganization(ctx, uuidA)))
	check("ListProjects", second(c.ListProjects(ctx, uuidA)))
	check("GetProject", second(c.GetProject(ctx, uuidA)))
	check("ListEnvironments", second(c.ListEnvironments(ctx, uuidA)))
	check("GetEnvironment", second(c.GetEnvironment(ctx, uuidA)))
	check("ListLocations", second(c.ListLocations(ctx)))

	check("ListSecrets", second(c.ListSecrets(ctx, uuidA, uuidA, 10)))
	check("SetSecret", c.SetSecret(ctx, uuidA, uuidA, "DB_URL", "postgres://x"))
	check("DeleteSecret", c.DeleteSecret(ctx, uuidA, uuidA, "DB_URL"))

	check("ListPods(project)", second(c.ListPods(ctx, PodScope{ProjectID: uuidA}, 0)))
	check("ListPods(env)", second(c.ListPods(ctx, PodScope{EnvironmentID: uuidA}, 5)))
	check("ListPods(deploy)", second(c.ListPods(ctx, PodScope{DeployID: uuidA}, 5)))
	check("GetPod", second(c.GetPod(ctx, "p-1")))

	check("ListBuilds", second(c.ListBuilds(ctx, uuidA)))
	check("GetBuild", second(c.GetBuild(ctx, uuidA)))
	port := 8080
	check("CreateBuild", second(c.CreateBuild(ctx, uuidA, &CreateBuildRequest{
		Name: "web-1.4.2", Image: "ghcr.io/acme/web", Tag: "1.4.2", CpuRequest: "250m", MemoryRequest: "512Mi", ContainerPort: &port,
	})))

	check("ListDeploys", second(c.ListDeploys(ctx, uuidA, uuidA)))
	check("GetDeploy", second(c.GetDeploy(ctx, testDeployID)))
	envItems := []DeployEnvItem{EnvVarItem("LOG_LEVEL", "info"), SecretRefItem("DB_URL")}
	check("CreateDeploy", second(c.CreateDeploy(ctx, uuidA, uuidA, &CreateDeployRequest{
		BuildId: uuidMust(uuidA), Name: "web", LocationId: "eu-west", Replicas: 2, Env: &envItems,
	})))
	check("UpdateDeploy", second(c.UpdateDeploy(ctx, testDeployID, func(r *UpdateDeployRequest) error {
		r.Replicas = 3
		return nil
	})))
	check("DeleteDeploy", c.DeleteDeploy(ctx, uuidA))

	check("ListRoutes", second(c.ListRoutes(ctx, uuidA)))
	prefix := "/api"
	check("CreateRoute", second(c.CreateRoute(ctx, uuidA, CreateRouteRequest{Hostname: "www.acme.dev", PathPrefix: &prefix})))
	check("DeleteRoute", c.DeleteRoute(ctx, uuidA, uuidA))

	check("ListDomains", second(c.ListDomains(ctx, uuidA)))
	check("CreateDomain", second(c.CreateDomain(ctx, uuidA, "acme.dev")))
	check("VerifyDomain", second(c.VerifyDomain(ctx, uuidA, "acme.dev")))
	check("DeleteDomain", c.DeleteDomain(ctx, uuidA, "acme.dev"))

	check("GetLogs", second(c.GetLogs(ctx, uuidA, &LogsQuery{Pod: "web-abc", Since: "1h", Filter: "err", Match: []string{"status=500", "path=~/api"}, Limit: 100})))
	check("ListMetricTypes", second(c.ListMetricTypes(ctx)))
	check("GetDeployMetrics", second(c.GetDeployMetrics(ctx, uuidA, MetricsQuery{Metric: "cpu", Pod: "web-abc", Range: "1h", Step: "30s"})))
	check("GetPodMetrics", second(c.GetPodMetrics(ctx, "web-abc", MetricsQuery{Metric: "memory"})))

	// Every vendored operation is exercised, so the allowlist carries
	// nothing the CLI does not use.
	for id := range responses {
		if !slices.Contains(srv.Seen(), id) {
			t.Errorf("operation %s was never called", id)
		}
	}
}

func uuidMust(s string) UUID {
	u, err := parseID("test", s)
	if err != nil {
		panic(err)
	}
	return u
}

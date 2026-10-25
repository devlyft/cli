//go:build smoke

// Package smoke runs the CLI against the production API with a dedicated
// test organization. It is excluded from normal builds by the `smoke` build
// tag and runs nightly from .github/workflows/smoke.yml, never on pull
// requests. See docs/testing.md.
//
//	DEVLYFT_API_TOKEN=... DEVLYFT_SMOKE_ORG=<org UUID> go test -tags smoke -v ./test/smoke/
package smoke

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devlyft/cli/internal/api"
)

const defaultAPIURL = "https://api.devlyft.io"

// env returns the variable or skips the test when it is unset.
func env(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Skipf("%s is not set", name)
	}
	return v
}

func apiURL() string {
	if u := os.Getenv("DEVLYFT_API_URL"); u != "" {
		return u
	}
	return defaultAPIURL
}

func newClient(t *testing.T) api.Client {
	t.Helper()
	c, err := api.New(api.Options{BaseURL: apiURL(), Tokens: api.StaticToken(env(t, "DEVLYFT_API_TOKEN"))})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestBinary builds the CLI and runs commands that need no API.
func TestBinary(t *testing.T) {
	t.Parallel()
	env(t, "DEVLYFT_API_TOKEN")

	bin := filepath.Join(t.TempDir(), "devlyft")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "../../cmd/devlyft") //nolint:gosec // path under t.TempDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	out, err := exec.CommandContext(t.Context(), bin, "version").Output() //nolint:gosec // built above
	if err != nil {
		t.Fatalf("devlyft version: %v", err)
	}
	if !strings.HasPrefix(string(out), "devlyft ") {
		t.Errorf("devlyft version printed %q", out)
	}
}

// TestRead checks the token works and the test organization is readable.
func TestRead(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	orgID := env(t, "DEVLYFT_SMOKE_ORG")

	org, err := c.GetOrganization(t.Context(), orgID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	t.Logf("organization %s (%s)", org.Name, org.Id)

	projects, err := c.ListProjects(t.Context(), orgID)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	t.Logf("%d projects", len(projects))
}

// TestDeployLifecycle creates a throwaway deploy and deletes it. It changes
// production state, so it only runs with DEVLYFT_SMOKE_WRITE=1 and the
// project, environment, location and image to use.
func TestDeployLifecycle(t *testing.T) {
	t.Parallel()
	if os.Getenv("DEVLYFT_SMOKE_WRITE") != "1" {
		t.Skip("DEVLYFT_SMOKE_WRITE is not 1")
	}
	c := newClient(t)
	projectID := env(t, "DEVLYFT_SMOKE_PROJECT")
	envID := env(t, "DEVLYFT_SMOKE_ENV")
	location := env(t, "DEVLYFT_SMOKE_LOCATION")
	image := env(t, "DEVLYFT_SMOKE_IMAGE") // repo:tag
	repo, tag, ok := strings.Cut(image, ":")
	if !ok {
		t.Fatalf("DEVLYFT_SMOKE_IMAGE %q must be repo:tag", image)
	}

	name := fmt.Sprintf("smoke-%d", time.Now().Unix())
	port := 8080
	build, err := c.CreateBuild(t.Context(), projectID, &api.CreateBuildRequest{
		Name: name, Image: repo, Tag: tag, CpuRequest: "100m", MemoryRequest: "128Mi", ContainerPort: &port,
	})
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}

	deploy, err := c.CreateDeploy(t.Context(), projectID, envID, &api.CreateDeployRequest{
		BuildId: build.Id, Name: name, LocationId: location, Replicas: 1,
	})
	if err != nil {
		t.Fatalf("CreateDeploy: %v", err)
	}
	deployID := deploy.Id.String()
	t.Cleanup(func() {
		// t.Context is already cancelled when cleanups run.
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute) //nolint:usetesting // see above
		defer cancel()
		if err := c.DeleteDeploy(ctx, deployID); err != nil {
			t.Errorf("DeleteDeploy %s: %v (delete it by hand)", deployID, err)
		}
	})

	got, err := c.GetDeploy(t.Context(), deployID)
	if err != nil {
		t.Fatalf("GetDeploy: %v", err)
	}
	if got.Name != name {
		t.Errorf("deploy name = %q, want %q", got.Name, name)
	}
}

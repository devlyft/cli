package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/exitcode"
)

func TestNewRejectsInsecureURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url string
		ok  bool
	}{
		{"https://api.devlyft.io", true},
		{"https://api.devlyft.io/", true},
		{"http://127.0.0.1:8080", true},
		{"http://localhost:8080", true},
		{"http://[::1]:8080", true},
		{"http://api.devlyft.io", false},
		{"ftp://api.devlyft.io", false},
		{"api.devlyft.io", false},
		{"", false},
	}
	for _, tt := range tests {
		_, err := New(Options{BaseURL: tt.url})
		if (err == nil) != tt.ok {
			t.Errorf("New(%q) err = %v, want ok=%v", tt.url, err, tt.ok)
		}
	}
}

func TestInvalidIDIsUsageError(t *testing.T) {
	t.Parallel()

	c, err := New(Options{BaseURL: "https://api.devlyft.io"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.GetDeploy(t.Context(), "web")
	if clierr.ExitCode(err) != exitcode.Usage {
		t.Errorf("exit = %d, want %d (err %v)", clierr.ExitCode(err), exitcode.Usage, err)
	}
}

func TestUpdateDeployReadModifyWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		etag        string
		wantIfMatch string
	}{
		{name: "with ETag sends If-Match", etag: `"v7"`, wantIfMatch: `"v7"`},
		{name: "without ETag sends no precondition"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var put *http.Request
			var putBody map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPut {
					put = r
					_ = json.NewDecoder(r.Body).Decode(&putBody)
				}
				if tt.etag != "" {
					w.Header().Set("ETag", tt.etag)
				}
				_, _ = w.Write([]byte(deployJSON))
			}))
			defer srv.Close()
			c, _ := newTestClient(t, srv, nil)

			_, err := c.UpdateDeploy(t.Context(), testDeployID, func(r *UpdateDeployRequest) error {
				r.Replicas = 4
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if put == nil {
				t.Fatal("no PUT sent")
			}
			if got := put.Header.Get("If-Match"); got != tt.wantIfMatch {
				t.Errorf("If-Match = %q, want %q", got, tt.wantIfMatch)
			}
			// The server requires these on every update; unchanged fields
			// must be carried over from the GET.
			want := map[string]any{
				"build_id":     "11111111-2222-4333-8444-555555555555",
				"name":         "web",
				"location_id":  "eu-west",
				"replicas":     float64(4),
				"expose":       true,
				"tenancy_type": "INHERIT",
			}
			for k, v := range want {
				if putBody[k] != v {
					t.Errorf("PUT %s = %v, want %v", k, putBody[k], v)
				}
			}
			if env, ok := putBody["env"].([]any); !ok || len(env) != 1 {
				t.Errorf("PUT env = %v, want the existing entry", putBody["env"])
			}
		})
	}
}

func TestUpdateDeployMutateErrorStops(t *testing.T) {
	t.Parallel()

	s := &scripted{body: deployJSON}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)

	boom := errors.New("bad flag")
	_, err := c.UpdateDeploy(t.Context(), testDeployID, func(*UpdateDeployRequest) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if len(s.requests) != 1 {
		t.Errorf("requests = %d, want only the GET", len(s.requests))
	}
}

func TestUpdateRequestFromDeployWithHPA(t *testing.T) {
	t.Parallel()

	var d Deploy
	if err := json.Unmarshal([]byte(deployJSON), &d); err != nil {
		t.Fatal(err)
	}
	d.EnableHpa = true
	minR, maxR := uint32(2), uint32(5)
	d.HpaMinReplicas, d.HpaMaxReplicas = &minR, &maxR

	req := UpdateRequestFromDeploy(&d)
	if req.Replicas != 0 {
		t.Errorf("Replicas = %d, want 0 with autoscaling", req.Replicas)
	}
	if *req.HpaMinReplicas != 2 || *req.HpaMaxReplicas != 5 {
		t.Errorf("HPA bounds not carried over: %+v", req)
	}
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		status    int
		header    string
		body      string
		wantCode  clierr.Code
		wantMsg   string
		wantReqID string
	}{
		{"problem detail", 404, "", `{"title":"Not Found","detail":"deploy not found","request_id":"req-body"}`, clierr.CodeNotFound, "deploy not found", "req-body"},
		{"header wins over body", 409, "req-hdr", `{"message":"name taken","request_id":"req-body"}`, clierr.CodeConflict, "name taken", "req-hdr"},
		{"error field", 400, "", `{"error":"replicas must be at least 1"}`, clierr.CodeInvalidArgument, "replicas must be at least 1", ""},
		{"plain text", 403, "", "forbidden\n", clierr.CodePermissionDenied, "forbidden", ""},
		{"html is ignored", 401, "", "<html>nope</html>", clierr.CodeAuthRequired, "Unauthorized", ""},
		{"empty body", 412, "req-1", "", clierr.CodeConflict, "Precondition Failed", "req-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.header != "" {
					w.Header().Set("X-Request-Id", tt.header)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			c, _ := newTestClient(t, srv, nil)

			err := getDeploy(context.Background(), c) //nolint:usetesting // parallel subtests outlive t.Context of the parent
			e := clierr.As(err)
			if e.Code != tt.wantCode || e.Message != tt.wantMsg || e.RequestID != tt.wantReqID || e.HTTPStatus != tt.status {
				t.Errorf("got code=%s msg=%q reqID=%q status=%d", e.Code, e.Message, e.RequestID, e.HTTPStatus)
			}
		})
	}
}

func TestListPodsNeedsScope(t *testing.T) {
	t.Parallel()

	c, err := New(Options{BaseURL: "https://api.devlyft.io"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ListPods(t.Context(), PodScope{}, 10)
	if clierr.ExitCode(err) != exitcode.Usage {
		t.Errorf("err = %v, want usage error", err)
	}
}

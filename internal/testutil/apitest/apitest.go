// Package apitest provides a fake DevLyft API for tests. Every request it
// receives must match an operation in the vendored OpenAPI specs
// (internal/api/specs), and every canned success response must match the
// spec too, so a spec update that breaks the CLI fails the tests that use it.
package apitest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
)

// Services are the vendored specs, by file name under internal/api/specs.
var Services = []string{"deploys", "org", "logs", "metrics"}

// Response is a canned reply for one operation.
type Response struct {
	// Status defaults to the operation's documented 2xx status.
	Status int
	// Body is JSON; empty sends no body.
	Body string
	// Header is added to the response.
	Header http.Header
}

// Server is a contract-checking fake API.
type Server struct {
	*httptest.Server

	t       testing.TB
	routers []routers.Router

	mu        sync.Mutex
	responses map[string]Response
	seen      []string
}

// NewServer starts a fake API answering each operation ID in responses.
// Requests for other operations, requests that do not match the spec and
// 2xx responses that do not match the spec fail t. The server is closed
// when the test ends.
func NewServer(tb testing.TB, responses map[string]Response) *Server {
	tb.Helper()
	if responses == nil {
		responses = map[string]Response{}
	}
	s := &Server{t: tb, responses: responses}
	s.Server = httptest.NewServer(s)
	tb.Cleanup(s.Close)

	for _, name := range Services {
		loader := openapi3.NewLoader()
		doc, err := loader.LoadFromFile(SpecPath(name))
		if err != nil {
			tb.Fatalf("loading %s spec: %v", name, err)
		}
		if err := doc.Validate(loader.Context); err != nil {
			tb.Fatalf("%s spec is invalid: %v", name, err)
		}
		doc.Servers = openapi3.Servers{{URL: s.URL}}
		r, err := legacy.NewRouter(doc)
		if err != nil {
			tb.Fatal(err)
		}
		s.routers = append(s.routers, r)
	}
	return s
}

// SpecPath returns the path of a vendored spec, wherever the test runs.
func SpecPath(service string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "api", "specs", service+".yaml")
}

// Respond sets or replaces the response for an operation.
func (s *Server) Respond(operationID string, r Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses[operationID] = r
}

// Seen returns the operation IDs called so far, in order.
func (s *Server) Seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.seen)
}

// ServeHTTP validates the request, then replies with the canned response.
func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Server-side URLs have no scheme or host; the router matches them
	// against the spec's server URL.
	req.URL.Scheme, req.URL.Host = "http", req.Host
	var route *routers.Route
	var params map[string]string
	for _, r := range s.routers {
		if rt, p, err := r.FindRoute(req); err == nil {
			route, params = rt, p
			break
		}
	}
	if route == nil {
		s.t.Errorf("%s %s matches no vendored operation", req.Method, req.URL.Path)
		http.Error(w, "no route", http.StatusNotFound)
		return
	}
	id := route.Operation.OperationID

	s.mu.Lock()
	s.seen = append(s.seen, id)
	resp, ok := s.responses[id]
	s.mu.Unlock()

	ctx := context.Background() //nolint:usetesting // validation outlives no test
	in := &openapi3filter.RequestValidationInput{
		Request:    req,
		PathParams: params,
		Route:      route,
		Options:    &openapi3filter.Options{MultiError: true},
	}
	if err := openapi3filter.ValidateRequest(ctx, in); err != nil {
		s.t.Errorf("%s: request does not match the spec: %v", id, err)
	}
	if !ok {
		s.t.Errorf("%s: no canned response", id)
		http.Error(w, "no canned response", http.StatusNotImplemented)
		return
	}

	status := resp.Status
	if status == 0 {
		status = SuccessStatus(route.Operation)
	}
	if status < http.StatusMultipleChoices {
		out := &openapi3filter.ResponseValidationInput{
			RequestValidationInput: in,
			Status:                 status,
			Header:                 http.Header{"Content-Type": {"application/json"}},
			Body:                   io.NopCloser(strings.NewReader(resp.Body)),
			Options:                &openapi3filter.Options{MultiError: true, IncludeResponseStatus: true},
		}
		if err := openapi3filter.ValidateResponse(ctx, out); err != nil {
			s.t.Errorf("%s: canned response does not match the spec: %v", id, err)
		}
	}

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if resp.Body != "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, resp.Body)
}

// SuccessStatus is the operation's documented 2xx response code.
func SuccessStatus(op *openapi3.Operation) int {
	for _, code := range []int{http.StatusOK, http.StatusCreated, http.StatusNoContent} {
		if op.Responses.Status(code) != nil {
			return code
		}
	}
	return http.StatusOK
}

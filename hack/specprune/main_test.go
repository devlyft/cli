package main

import (
	"regexp"
	"strings"
	"testing"
)

const source = `openapi: 3.0.0
info:
  title: Things API
  version: 1.0.0
  description: Runs on the internal cluster.
servers:
  - url: http://things.devlyft-apps.svc.cluster.local
security:
  - JWT: []
paths:
  /v1/things/{id}:
    parameters:
      - name: id
        in: path
        required: true
        schema: {type: string}
    get:
      operationId: GetThing
      security: [{TOKEN: []}]
      x-internal-note: secret
      responses:
        "200":
          description: The thing
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Thing'}
    delete:
      operationId: DeleteThing
      responses:
        "204": {description: Deleted}
  /v1/admin/things:
    get:
      operationId: AdminListThings
      responses:
        "200":
          description: All things
          content:
            application/json:
              schema: {$ref: '#/components/schemas/AdminThings'}
components:
  schemas:
    Thing:
      type: object
      required: [id, node_name]
      properties:
        id: {type: string}
        size:
          description: Size of the thing
          x-go-type: kresource.Quantity
        node_name: {type: string}
        owner: {$ref: '#/components/schemas/Owner'}
    Owner:
      type: object
      properties:
        name: {type: string, description: Set by the identity-api.}
    AdminThings:
      type: object
      properties:
        things: {type: array, items: {$ref: '#/components/schemas/Thing'}}
`

var (
	deny    = []*regexp.Regexp{regexp.MustCompile(`(?i)identity-api|cluster\.local|\bthe internal\b`)}
	suspect = []*regexp.Regexp{regexp.MustCompile(`(?i)node`)}
)

func TestPrune(t *testing.T) {
	t.Parallel()

	svc := Service{
		Operations:   []string{"GetThing"},
		DropFields:   []string{"schemas/Thing.node_name"},
		Descriptions: map[string]string{"#/components/schemas/Owner/properties/name/description": "Owner's display name."},
	}
	out, problems, err := prune([]byte(source), &svc, deny, suspect)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	got := string(out)

	for _, gone := range []string{
		"DeleteThing", "AdminListThings", "AdminThings", // not allowlisted, or only reachable from them
		"servers", "cluster.local", "security", "JWT", "TOKEN", // servers and security
		"x-internal-note", "x-go-type", "kresource", // extensions
		"node_name",                        // dropped field, also from required
		"identity-api", "internal cluster", // overridden or dropped descriptions
	} {
		if strings.Contains(got, gone) {
			t.Errorf("output still contains %q:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"GetThing", "Thing:", "Owner:", "Owner's display name.", "required: [id]"} {
		if !strings.Contains(got, kept) {
			t.Errorf("output lacks %q:\n%s", kept, got)
		}
	}
	// A type from x-go-type becomes a string.
	if !strings.Contains(got, "size:\n          type: string") {
		t.Errorf("size should become a string:\n%s", got)
	}
}

func TestPruneRefusesUnsafeOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		svc  Service
		want string
	}{
		{
			name: "denied description",
			svc:  Service{Operations: []string{"GetThing"}, DropFields: []string{"schemas/Thing.node_name"}},
			want: "description at #/components/schemas/Owner/properties/name/description matches deny pattern",
		},
		{
			name: "suspect field not reviewed",
			svc: Service{
				Operations:   []string{"GetThing"},
				Descriptions: map[string]string{"#/components/schemas/Owner/properties/name/description": ""},
			},
			want: "property schemas/Thing.node_name looks internal",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, problems, err := prune([]byte(source), &tt.svc, deny, suspect)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(problems, "\n"), tt.want) {
				t.Errorf("problems = %v, want one containing %q", problems, tt.want)
			}
		})
	}
}

func TestPruneReviewedFieldPasses(t *testing.T) {
	t.Parallel()

	svc := Service{
		Operations:   []string{"GetThing"},
		Reviewed:     []string{"schemas/Thing.node_name"},
		Descriptions: map[string]string{"#/components/schemas/Owner/properties/name/description": ""},
	}
	_, problems, err := prune([]byte(source), &svc, deny, suspect)
	if err != nil || len(problems) > 0 {
		t.Errorf("err = %v, problems = %v", err, problems)
	}
}

func TestPruneErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		svc  Service
		want string
	}{
		{"unknown operation", Service{Operations: []string{"Nope"}}, "allowlisted operation Nope not found"},
		{"unknown drop field", Service{Operations: []string{"GetThing"}, DropFields: []string{"schemas/Thing.nope"}}, "no such property"},
		{"bad override pointer", Service{Operations: []string{"GetThing"}, Descriptions: map[string]string{"#/components/schemas/Nope/description": "x"}}, "not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := prune([]byte(source), &tt.svc, deny, suspect)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

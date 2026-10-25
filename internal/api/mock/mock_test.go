package mock_test

import (
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/devlyft/cli/internal/api"
	"github.com/devlyft/cli/internal/api/mock"
)

// The pattern command tests follow: depend on api.Client and expect calls
// on the generated mock.
func TestMockClient(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	c := mock.NewMockClient(ctrl)
	c.EXPECT().ListOrganizations(gomock.Any()).Return([]api.Organization{{Name: "Acme"}}, nil)

	var client api.Client = c
	orgs, err := client.ListOrganizations(t.Context())
	if err != nil || len(orgs) != 1 || orgs[0].Name != "Acme" {
		t.Errorf("got %v, %v", orgs, err)
	}
}

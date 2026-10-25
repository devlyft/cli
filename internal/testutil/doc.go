// Package testutil is the root of the CLI's test harness. See
// docs/testing.md for how the layers fit together:
//
//   - golden: golden-file assertions, regenerated with -update
//   - apitest: an httptest server that validates every exchange against the
//     vendored OpenAPI specs
//   - clitest: runs the CLI in-process with injected streams and a gomock
//     API client
//
// These packages are imported only by tests.
package testutil

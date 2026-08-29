// Package integration drives the swiftcontrol adapter against a real
// containerized Swift (DESIGN.md §57.3): auth, listings, the §14.3
// encoding suite, HEAD semantics, and large-object behaviour, on the
// §58 canonical fixtures loaded fresh under a run-unique prefix.
//
// The suite is env-gated: it runs only when AERONAUTES_SAIO_AUTH_URL is
// set (AERONAUTES_SAIO_USER / AERONAUTES_SAIO_KEY default to the SAIO
// test:tester/testing credentials) and skips silently otherwise, so
// `go test ./...` stays fast on machines without a cluster. CI runs it
// against openstackswift/saio; locally:
//
//	podman run -d --name saio -p 127.0.0.1:8080:8080 docker.io/openstackswift/saio:latest
//	AERONAUTES_SAIO_AUTH_URL=http://127.0.0.1:8080/auth/v1.0 go test ./internal/integration
//
// Fixture setup/teardown writes through the wire library directly —
// test infrastructure on throwaway containers, deliberately outside the
// application's mutation rules (§28.6 binds the product, §57.3 requires
// the CRUD exercise). Local Swift proves Swift semantics; the real
// SwiftStack target remains the §57.4 release gate.
package integration

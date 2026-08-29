// Command mockswift runs the in-process mock Swift cluster (§57.2) as a
// standalone dev server loaded with the §58 canonical fixtures, so the
// desktop app can be exercised end to end with no real cluster. Dev
// tooling only — never part of the shipped application.
//
// Usage: mockswift [-addr 127.0.0.1:8899] [-user test:tester] [-key testing]
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/csmart/aeronautes/internal/swifttest"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8899", "listen address")
	user := flag.String("user", "test:tester", "accepted X-Auth-User")
	key := flag.String("key", "testing", "accepted X-Auth-Key")
	flag.Parse()

	server := swifttest.New(*user, *key)
	server.LoadCanonicalFixtures()

	fmt.Printf("mock swift cluster on http://%s\n", *addr)
	fmt.Printf("  auth URL: http://%s/auth/v1.0\n", *addr)
	fmt.Printf("  user:     %s\n", *user)
	log.Fatal(http.ListenAndServe(*addr, server))
}

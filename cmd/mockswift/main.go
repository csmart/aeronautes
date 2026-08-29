// mockswift serves the in-process mock cluster (internal/swifttest)
// over HTTP for headless smoke runs (§65.6): TempAuth v1 at
// /auth/v1.0, canonical fixtures loaded, test:tester/testing.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/csmart/aeronautes/internal/swifttest"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8899", "listen address")
	fixtures := flag.Bool("fixtures", true, "load the §58 canonical fixture namespace")
	flag.Parse()
	server := swifttest.New("test:tester", "testing")
	if *fixtures {
		server.LoadCanonicalFixtures()
	}
	log.Printf("mockswift on http://%s (auth /auth/v1.0, test:tester/testing)", *addr)
	log.Fatal(http.ListenAndServe(*addr, server))
}

// mockswift serves the in-process mock cluster (internal/swifttest)
// over HTTP for headless smoke runs (§65.6): TempAuth v1 at
// /auth/v1.0, canonical fixtures loaded, test:tester/testing.
package main

import (
	"encoding/json"
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
	server.V3UserID = "user-test"
	server.V3ProjectName = "test"
	server.V3ProjectID = "project-test"
	if *fixtures {
		server.LoadCanonicalFixtures()
	}
	// Request logging: the smoke script verifies UI steps by their wire
	// effects (an auth GET proves connect happened, a PUT proves the
	// copy started) instead of trusting blind sleeps.
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		// The browser's root-URL journey must discover a real same-origin
		// endpoint; /identity still offers no version document.
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMultipleChoices)
			if err := json.NewEncoder(w).Encode(map[string]any{
				"versions": map[string]any{"values": []map[string]any{{
					"id": "v3.0", "status": "stable",
					"links": []map[string]string{{"rel": "self", "href": "http://" + r.Host + "/v3"}},
				}}},
			}); err != nil {
				log.Printf("write version document: %v", err)
			}
			return
		}
		server.ServeHTTP(w, r)
	})
	log.Printf("mockswift on http://%s (auth /auth/v1.0, test:tester/testing)", *addr)
	log.Fatal(http.ListenAndServe(*addr, logged))
}

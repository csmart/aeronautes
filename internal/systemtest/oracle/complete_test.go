package oracle

import (
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

type responseSite struct {
	name, class, body string
	account, json     bool
}

var responseSites = []responseSite{
	{"object", "plain", "object bytes", false, false},
	{"leaf", "dlo", "segment bytes", false, false},
	{"leaf", "slo", "segment bytes", false, false},
	{"manifest", "slo", `[{"name":"/segments/leaf"}]`, false, true},
	{"container-first", "plain", `[{"name":"item"}]`, false, true},
	{"container-next", "plain", `[]`, false, true},
	{"segments-first", "dlo", `[{"name":"leaf"}]`, false, true},
	{"segments-next", "dlo", `[]`, false, true},
	{"account-first", "plain", `[{"name":"objects"}]`, true, true},
	{"account-next", "plain", `[]`, true, true},
}

type responseFault struct {
	status       int
	rangePresent bool
	rangeValue   string
	truncated    bool
	body         []byte
	encoding     string
}

func witnessResponse(t *testing.T, site responseSite, fault responseFault) error {
	t.Helper()
	var hits atomic.Int32
	o := representationOracle(t, func(w http.ResponseWriter, r *http.Request) {
		var role, body string
		query := r.URL.Query()
		switch r.URL.Path {
		case "/":
			role, body = "account-first", `[{"name":"objects"}]`
			if query.Get("marker") != "" {
				role, body = "account-next", `[]`
			}
		case "/objects":
			role, body = "container-first", `[{"name":"item"}]`
			if query.Get("marker") != "" {
				role, body = "container-next", `[]`
			}
		case "/segments":
			role, body = "segments-first", `[{"name":"leaf"}]`
			if query.Get("marker") != "" {
				role, body = "segments-next", `[]`
			}
		case "/segments/leaf":
			role, body = "leaf", "segment bytes"
		case "/objects/item":
			role, body = "object", "object bytes"
			switch {
			case query.Get("multipart-manifest") == "get":
				role, body = "manifest", `[{"name":"/segments/leaf"}]`
			case query.Get("symlink") == "get":
				role = "symlink"
			case site.class == "dlo":
				w.Header().Set("X-Object-Manifest", "segments/leaf")
			case site.class == "slo":
				w.Header().Set("X-Static-Large-Object", "true")
			}
		default:
			http.NotFound(w, r)
			return
		}
		status, payload := http.StatusOK, []byte(body)
		if role == site.name {
			hits.Add(1)
			if body != site.body {
				t.Error("fault reached the wrong response body")
			}
			if fault.status != 0 {
				status = fault.status
			}
			if fault.body != nil {
				payload = fault.body
			}
			if fault.rangePresent {
				w.Header().Set("Content-Range", fault.rangeValue)
			}
			if fault.truncated {
				w.Header().Set("Content-Length", strconv.Itoa(len(payload)+7))
			}
			if fault.encoding != "" {
				w.Header().Set("Content-Encoding", fault.encoding)
			}
		}
		w.WriteHeader(status)
		w.Write(payload)
	})
	var err error
	if site.account {
		var names []string
		names, err = o.ListContainers("")
		if err != nil && names != nil {
			t.Error("refused listing returned partial inventory")
		}
		if err == nil && !reflect.DeepEqual(names, []string{"objects"}) {
			t.Error("successful listing control lost its inventory")
		}
	} else {
		var snapshot *AccountSnapshot
		snapshot, err = o.SnapshotContainers("objects")
		if err != nil && snapshot != nil {
			t.Error("refused snapshot returned partial evidence")
		}
		if err == nil && (snapshot == nil || len(snapshot.Containers["objects"].Objects) != 1 || snapshot.Containers["objects"].Objects["item"].Class != site.class) {
			t.Error("successful snapshot control lost its object")
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("response boundary reached %d times, want exactly once", hits.Load())
	}
	return err
}

func TestSnapshotRequiresCompleteResponses(t *testing.T) {
	for _, site := range responseSites {
		t.Run(site.class+"/"+site.name, func(t *testing.T) {
			if err := witnessResponse(t, site, responseFault{}); err != nil {
				t.Fatalf("complete response control failed: %v", err)
			}
			for _, tc := range []struct {
				name  string
				fault responseFault
			}{
				{"partial-status", responseFault{status: http.StatusPartialContent}},
				{"partial-with-range", responseFault{status: http.StatusPartialContent, rangePresent: true, rangeValue: "bytes 0-1/20"}},
				{"created-status", responseFault{status: http.StatusCreated}},
				{"range-on-200", responseFault{rangePresent: true, rangeValue: "bytes 0-1/20"}},
				{"empty-range-on-200", responseFault{rangePresent: true}},
				{"truncated-body", responseFault{truncated: true}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if err := witnessResponse(t, site, tc.fault); err == nil {
						t.Fatal("incomplete response was certified as a complete representation")
					}
				})
			}
		})
	}
}

func TestSnapshotRequiresSupportedJSON(t *testing.T) {
	for _, site := range responseSites {
		if !site.json {
			continue
		}
		t.Run(site.class+"/"+site.name, func(t *testing.T) {
			for _, tc := range []struct {
				name  string
				fault responseFault
			}{
				{"unparseable", responseFault{body: []byte("{malformed")}},
				{"wrong-json-type", responseFault{body: []byte(`{}`)}},
				{"encoded-json", responseFault{body: gzipBytes(t, []byte(site.body), "structured"), encoding: "gzip"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if err := witnessResponse(t, site, tc.fault); err == nil {
						t.Fatal("unsupported structured response was certified as an inventory")
					}
				})
			}
		})
	}
}

func TestSnapshotEmptyAndMissingContainers(t *testing.T) {
	o := representationOracle(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, "[]")
	})
	snapshot, err := o.SnapshotContainers("empty", "missing")
	if err != nil || snapshot == nil || len(snapshot.Containers) != 1 {
		t.Fatalf("empty and missing containers were not distinguished: %v, %v", snapshot, err)
	}
	if empty, ok := snapshot.Containers["empty"]; !ok || len(empty.Objects) != 0 {
		t.Fatal("valid empty JSON page was not preserved")
	}
}

func TestSnapshotRejectsPartialHeadersBeforeBody(t *testing.T) {
	headers, release := make(chan struct{}), make(chan struct{})
	o := representationOracle(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-1/20")
		w.WriteHeader(http.StatusPartialContent)
		w.(http.Flusher).Flush()
		close(headers)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	result := make(chan error, 1)
	settled := false
	t.Cleanup(func() {
		close(release)
		if !settled {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("snapshot did not settle after body release")
			}
		}
	})
	go func() {
		_, err := o.SnapshotContainers("objects")
		result <- err
	}()
	select {
	case <-headers:
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot did not reach the response-header barrier")
	}
	select {
	case err := <-result:
		settled = true
		if err == nil {
			t.Fatal("partial headers were accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot waited for a body after receiving partial headers")
	}
}

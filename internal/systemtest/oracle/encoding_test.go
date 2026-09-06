package oracle

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type representation struct {
	body      []byte
	encodings []string
}

func gzipBytes(t *testing.T, body []byte, name string) []byte {
	t.Helper()
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	w.Name = name
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func representationOracle(t *testing.T, serve http.HandlerFunc) *Oracle {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth" {
			w.Header().Set("X-Storage-Url", server.URL)
			w.Header().Set("X-Auth-Token", "synthetic-token")
			return
		}
		serve(w, r)
	}))
	t.Cleanup(server.Close)
	o, err := Connect(server.URL+"/auth", "fixture", "synthetic-key")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(o.client.CloseIdleConnections)
	return o
}

func writeRepresentation(w http.ResponseWriter, value representation) {
	w.Header().Set("Content-Type", "application/octet-stream")
	for _, encoding := range value.encodings {
		w.Header().Add("Content-Encoding", encoding)
	}
	w.Write(value.body)
}

func TestSnapshotPreservesEncodedRepresentation(t *testing.T) {
	plain := []byte("the same decoded content")
	a := representation{gzipBytes(t, plain, "a"), []string{"gzip"}}
	b := representation{gzipBytes(t, plain, "b"), []string{"gzip"}}
	for _, encoded := range []representation{a, b} {
		reader, err := gzip.NewReader(bytes.NewReader(encoded.body))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || !bytes.Equal(decoded, plain) {
			t.Fatal("compressed controls do not have the same decoded content")
		}
	}
	if bytes.Equal(a.body, b.body) || len(a.body) != len(b.body) {
		t.Fatal("compressed controls must differ without changing size")
	}
	cases := []struct {
		name          string
		before, after representation
		changed       []Field
	}{
		{"unchanged-plain", representation{plain, nil}, representation{plain, nil}, nil},
		{"unchanged-gzip", a, a, nil},
		{"gzip-to-plain", a, representation{plain, nil}, []Field{"size", "content", "contentHeaders"}},
		{"different-gzip-same-decoded-content", a, b, []Field{"content"}},
		{"encoding-removed", a, representation{a.body, nil}, []Field{"contentHeaders"}},
		{"encoding-added", representation{plain, nil}, representation{plain, []string{"gzip"}}, []Field{"contentHeaders"}},
		{"unknown-encoding", representation{plain, []string{"br"}}, representation{plain, []string{"zstd"}}, []Field{"contentHeaders"}},
		{"malformed-gzip", representation{[]byte("bad-a"), []string{"gzip"}}, representation{[]byte("bad-b"), []string{"gzip"}}, []Field{"content"}},
		{"second-encoding-field", representation{plain, []string{"gzip", "br"}}, representation{plain, []string{"gzip", "zstd"}}, []Field{"contentHeaders"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			states := []representation{tc.before, tc.after}
			var selected atomic.Int32
			o := representationOracle(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept-Encoding") != "" {
					t.Error("oracle negotiated content encoding instead of observing Swift's stored representation")
				}
				switch r.URL.Path {
				case "/objects":
					if r.URL.Query().Get("marker") == "item" {
						fmt.Fprint(w, "[]")
					} else {
						fmt.Fprint(w, `[{"name":"item"}]`)
					}
				case "/objects/item":
					writeRepresentation(w, states[selected.Load()])
				default:
					http.NotFound(w, r)
				}
			})
			snapshots := make([]*AccountSnapshot, 2)
			for i, state := range states {
				selected.Store(int32(i))
				snapshot, err := o.SnapshotContainers("objects")
				if err != nil {
					t.Fatal(err)
				}
				got := snapshot.Containers["objects"].Objects["item"]
				if got.Size != int64(len(state.body)) || got.Digest != digest(state.body) || got.ContentEncoding != strings.Join(state.encodings, ", ") {
					t.Errorf("snapshot lost the encoded representation or its headers: %+v", got)
				}
				snapshots[i] = snapshot
			}
			if got := objectFields(snapshots[0].Containers["objects"].Objects["item"], snapshots[1].Containers["objects"].Objects["item"]); !reflect.DeepEqual(got, tc.changed) {
				t.Errorf("changed fields = %v, want %v", got, tc.changed)
			}
			delta := Diff(snapshots[0], snapshots[1])
			if delta.Empty() != (len(tc.changed) == 0) {
				t.Errorf("snapshot delta hid or invented a representation change: %s", delta)
			}
		})
	}
}

func TestLargeObjectInventoryPreservesEncodedSegments(t *testing.T) {
	for _, class := range []string{"dlo", "slo"} {
		t.Run(class, func(t *testing.T) {
			body := gzipBytes(t, []byte("segment content"), "leaf")
			var changed atomic.Bool
			o := representationOracle(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/objects":
					if r.URL.Query().Get("marker") == "item" {
						fmt.Fprint(w, "[]")
					} else {
						fmt.Fprint(w, `[{"name":"item"}]`)
					}
				case "/segments":
					if r.URL.Query().Get("marker") == "leaf" {
						fmt.Fprint(w, "[]")
					} else {
						fmt.Fprint(w, `[{"name":"leaf"}]`)
					}
				case "/objects/item":
					if r.URL.Query().Get("multipart-manifest") == "get" {
						fmt.Fprint(w, `[{"name":"/segments/leaf"}]`)
						return
					}
					if class == "dlo" {
						w.Header().Set("X-Object-Manifest", "segments/leaf")
					} else {
						w.Header().Set("X-Static-Large-Object", "true")
					}
					writeRepresentation(w, representation{body, []string{"gzip"}})
				case "/segments/leaf":
					encodings := []string{"gzip", "br"}
					if changed.Load() {
						encodings[1] = "zstd"
					}
					writeRepresentation(w, representation{body, encodings})
				default:
					http.NotFound(w, r)
				}
			})
			before, err := o.SnapshotContainers("objects")
			if err != nil {
				t.Fatal(err)
			}
			obj := before.Containers["objects"].Objects["item"]
			if obj.Class != class || obj.Digest != digest(body) || obj.Size != int64(len(body)) || len(obj.Segments) != 1 {
				t.Fatalf("large-object representation was decoded or lost: %+v", obj)
			}
			segment := obj.Segments[0]
			if segment.Digest != digest(body) || segment.Size != int64(len(body)) || segment.ContentEncoding != "gzip, br" {
				t.Fatalf("segment representation was decoded or lost: %+v", segment)
			}
			changed.Store(true)
			after, err := o.SnapshotContainers("objects")
			if err != nil {
				t.Fatal(err)
			}
			if got := objectFields(obj, after.Containers["objects"].Objects["item"]); !reflect.DeepEqual(got, []Field{"segments"}) {
				t.Fatalf("encoding-only change outside the snapshotted container was missed: %v", got)
			}
		})
	}
}

func TestOracleDoesNotChangeDefaultDecoding(t *testing.T) {
	plain := []byte("shared transport control")
	encoded := gzipBytes(t, plain, "default")
	o := representationOracle(t, func(w http.ResponseWriter, r *http.Request) {
		writeRepresentation(w, representation{encoded, []string{"gzip"}})
	})
	client := &http.Client{Timeout: o.client.Timeout}
	response, err := client.Get(o.storageURL + "/default")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || !response.Uncompressed || !bytes.Equal(body, plain) {
		t.Fatal("oracle changed the shared default transport's decoding policy")
	}
}

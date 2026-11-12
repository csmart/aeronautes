package local

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// stageSegmented stages data at staged the way the pinned engine was
// observed to (docs/rclone.md): segments named by a zero-padded decimal
// index under "<staged>/<run id>/" in the container's _segments sidecar,
// each announced by the notice the capture reads, then a dynamic manifest
// at the staged name whose header value is percent-encoded.
func stageSegmented(ctx context.Context, s *swiftcontrol.Session, container, staged string, data []byte, chunk int, onEvent func(rclone.Event)) (rclone.SegmentPrefix, error) {
	sidecar := container + "_segments"
	run := staged + "/2026-11-12-000000-run/"
	for i := 0; i*chunk < len(data); i++ {
		name := run + fmt.Sprintf("%08d", i)
		piece := data[i*chunk : min((i+1)*chunk, len(data))]
		if _, err := s.PutObject(ctx, sidecar, name, strings.NewReader(string(piece)), "application/octet-stream", nil); err != nil {
			return rclone.SegmentPrefix{}, err
		}
		if onEvent != nil {
			onEvent(rclone.Event{Msg: fmt.Sprintf("Uploading segment file %q into %q", name, sidecar)})
		}
	}
	manifest := (&url.URL{Path: sidecar + "/" + run}).EscapedPath()
	if _, err := s.PutObject(ctx, container, staged, strings.NewReader(""), "application/octet-stream", map[string]string{"X-Object-Manifest": manifest}); err != nil {
		return rclone.SegmentPrefix{}, err
	}
	return rclone.SegmentPrefix{Container: sidecar, Prefix: run}, nil
}

// segmentingEngine is the engine for a file larger than its chunk.
type segmentingEngine struct {
	session *swiftcontrol.Session
	chunk   int
}

func (e *segmentingEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	_, path, _ := strings.Cut(dst, ":")
	container, prefix, _ := strings.Cut(path, "/")
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			return nil, err
		}
		if _, err := stageSegmented(ctx, e.session, container, prefix+"/"+name, data, e.chunk, onEvent); err != nil {
			return nil, err
		}
	}
	return &rclone.Result{}, nil
}

// servedSession is a session on mock, which the caller has set up.
func servedSession(t *testing.T, mock *swifttest.Server) *swiftcontrol.Session {
	t.Helper()
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	return bindingSession(t, server)
}

func segmentMock() *swifttest.Server {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	mock.AddContainer("media_segments", "standard")
	return mock
}

func readLive(t *testing.T, s *swiftcontrol.Session, container, name string) string {
	t.Helper()
	body, _, err := s.OpenObject(t.Context(), container, name)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

package swiftcontrol

import (
	"context"
	"net/http"
)

// AdmitRequest checks captured authority again at each request boundary.
// It does not hold publication coordination during I/O or undo the effects
// of operations admitted before a concurrent bookmark change.
func (s *Session) AdmitRequest(ctx context.Context) error {
	if s.admitRequest != nil {
		return s.admitRequest(ctx)
	}
	return nil
}

type requestAdmissionTransport struct {
	inner http.RoundTripper
	admit func(context.Context) error
}

// closeIdle lets a session close what the wrapped transport keeps idle;
// see idleCloser for why this is not the exported method.
func (t requestAdmissionTransport) closeIdle() {
	closeIdleConnections(t.inner)
}

func (t requestAdmissionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.admit(req.Context()); err != nil {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, err
	}
	return t.inner.RoundTrip(req)
}

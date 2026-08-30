package swiftcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// BulkDeleteResult is one bulk request's account of itself.
type BulkDeleteResult struct {
	Deleted  int64
	NotFound int64
	// Errors are per-item failures: [path, status].
	Errors [][2]string
}

// BulkDelete issues one Swift bulk-delete (§31.2 BulkDelete executor):
// an account-level POST ?bulk-delete whose text/plain body lists one
// URL-encoded "/container/object" per line. Deletion is manifest-only
// by construction — the middleware deletes exactly the named objects
// and never follows DLO/SLO segments. Newline-bearing names are safe
// here (unlike rclone lists): each path segment is percent-encoded, so
// a newline travels as %0A.
func (s *Session) BulkDelete(ctx context.Context, container string, names []string) (*BulkDeleteResult, error) {
	var body strings.Builder
	for _, name := range names {
		body.WriteString("/" + url.PathEscape(container) + "/" + escapeObjectPath(name) + "\n")
	}
	// The session's own client (bookmark transport, refused redirects,
	// bounded time) with ONE re-auth on 401 — the lifecycle the wire
	// library gives every other verb.
	send := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			s.conn.StorageUrl+"?bulk-delete", strings.NewReader(body.String()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Auth-Token", s.conn.AuthToken)
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Accept", "application/json")
		return s.raw.Do(req)
	}
	resp, err := send()
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if authErr := s.Authenticate(ctx); authErr != nil {
			return nil, authErr
		}
		resp, err = send()
	}
	if err != nil {
		return nil, Normalize(err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, Normalize(err)
	}
	if resp.StatusCode >= 300 {
		return nil, &SwiftError{Kind: KindServer, HTTPStatus: resp.StatusCode,
			Message: fmt.Sprintf("bulk-delete: %s", strings.TrimSpace(string(payload)))}
	}

	var decoded struct {
		NumberDeleted  int64           `json:"Number Deleted"`
		NumberNotFound int64           `json:"Number Not Found"`
		ResponseStatus string          `json:"Response Status"`
		Errors         [][2]string     `json:"Errors"`
		ResponseBody   json.RawMessage `json:"Response Body"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("bulk-delete response: %w", err)
	}
	result := &BulkDeleteResult{
		Deleted:  decoded.NumberDeleted,
		NotFound: decoded.NumberNotFound,
		Errors:   decoded.Errors,
	}
	// The middleware reports overall failure through Response Status
	// while still returning 200 on the wire; surface it when nothing
	// per-item explains it.
	if !strings.HasPrefix(decoded.ResponseStatus, "200") && len(decoded.Errors) == 0 {
		return result, fmt.Errorf("bulk-delete: %s", decoded.ResponseStatus)
	}
	// Cardinality must reconcile: every requested name is deleted, not
	// found, or individually errored — an undercounted 200 cannot pass
	// as success (K-DEL-002).
	if accounted := result.Deleted + result.NotFound + int64(len(result.Errors)); accounted != int64(len(names)) {
		return result, fmt.Errorf("bulk-delete response accounts for %d of %d requested names — treating the difference as failed", accounted, len(names))
	}
	return result, nil
}

// escapeObjectPath percent-encodes an object name per path segment,
// preserving the "/" separators that are part of the name.
func escapeObjectPath(name string) string {
	segments := strings.Split(name, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// DeleteContainer removes an EMPTY container natively (§33: Delete
// Container empties first through the deletion engine, then calls
// this; Swift answers 409 while contents remain).
func (s *Session) DeleteContainer(ctx context.Context, name string) error {
	return Normalize(s.conn.ContainerDelete(ctx, name))
}

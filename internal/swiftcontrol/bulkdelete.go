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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.conn.StorageUrl+"?bulk-delete", strings.NewReader(body.String()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", s.conn.AuthToken)
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, Normalize(err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
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

// DeleteSLOWithSegments issues Swift's native SLO deletion
// (?multipart-manifest=delete): the server deletes the manifest AND
// every referenced segment. This is §34's fast path and it is exactly
// as destructive as it sounds — callers gate it behind
// ExclusiveOwnershipProof or the deliberate Backstage flow; this
// method only does the wire work.
func (s *Session) DeleteSLOWithSegments(ctx context.Context, container, name string) (*BulkDeleteResult, error) {
	target := s.conn.StorageUrl + "/" + url.PathEscape(container) + "/" + escapeObjectPath(name) + "?multipart-manifest=delete"
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", s.conn.AuthToken)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, Normalize(err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, Normalize(err)
	}
	if resp.StatusCode >= 300 {
		return nil, &SwiftError{Kind: KindServer, HTTPStatus: resp.StatusCode,
			Message: fmt.Sprintf("slo delete: %s", strings.TrimSpace(string(payload)))}
	}
	var decoded struct {
		NumberDeleted  int64       `json:"Number Deleted"`
		NumberNotFound int64       `json:"Number Not Found"`
		ResponseStatus string      `json:"Response Status"`
		Errors         [][2]string `json:"Errors"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("slo delete response: %w", err)
	}
	result := &BulkDeleteResult{Deleted: decoded.NumberDeleted, NotFound: decoded.NumberNotFound, Errors: decoded.Errors}
	if !strings.HasPrefix(decoded.ResponseStatus, "200") && len(decoded.Errors) == 0 {
		return result, fmt.Errorf("slo delete: %s", decoded.ResponseStatus)
	}
	return result, nil
}

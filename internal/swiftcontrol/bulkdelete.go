package swiftcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// BulkDeleteFailure is one per-item failure resolved back to the name
// the caller asked to delete. The reply echoes the request line, which
// is percent-encoded and container-prefixed, so resolving it here — in
// the only place that knows how the line was built — keeps every caller
// from rebuilding that rule and drifting from it.
type BulkDeleteFailure struct {
	// Name is the requested object name, spelled exactly as the caller
	// gave it.
	Name string
	// Status is the per-item status the reply reported.
	Status string
}

// BulkDeleteResult is one bulk request's account of itself.
//
// Deleted and NotFound are counts alone: the reply names no successful
// identity, so which names they refer to is not knowable from it.
type BulkDeleteResult struct {
	Deleted  int64
	NotFound int64
	// Errors are the raw per-item failure rows: [path, status]. The path
	// is the echoed request line, not an object name.
	Errors [][2]string
	// Failures are those rows resolved to requested names, in reply
	// order and preserving repeats, so a caller can see exactly what the
	// reply said about names it asked about.
	Failures []BulkDeleteFailure
	// UnmatchedPaths are error rows whose path matches no line this
	// request sent. They are evidence the reply is not wholly about this
	// request and must never be recorded as one of the caller's names.
	UnmatchedPaths []string
	// Anomalies describe every way this reply failed validation, in the
	// order found. A reply with any anomaly says nothing dependable
	// about which names it handled.
	Anomalies []string
	// Validated is true only when the reply named this request's names,
	// named none of them twice, gave every row a status that denotes a
	// failure, reconciled its counts and carried a usable overall
	// status. Only a validated reply's counts may be credited.
	Validated bool
}

// statusLine matches the shape Swift documents for a status: three
// digits, optionally followed by a space and a reason phrase. It is
// deliberately strict, so a value like "+500", "0500", "200not-a-status"
// or an empty string is not a status at all.
var statusLine = regexp.MustCompile(`^([1-5][0-9]{2})( .*)?$`)

// statusCode returns the code a status line carries, and whether the
// line is a status at all.
func statusCode(status string) (int, bool) {
	match := statusLine.FindStringSubmatch(status)
	if match == nil {
		return 0, false
	}
	code, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return code, true
}

// failureStatus reports whether a per-item status is a well formed
// status that denotes a failure. A row is authoritative about a name
// only when it says something went wrong: a missing, malformed or
// success-valued status does not become a disposition merely because
// the path matched.
func failureStatus(status string) bool {
	code, ok := statusCode(status)
	return ok && code >= 400
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
	// The line each name is sent as, kept so the reply's echoed error
	// rows can be resolved back to the requested spelling.
	requested := make(map[string]string, len(names))
	for _, name := range names {
		line := "/" + url.PathEscape(container) + "/" + escapeObjectPath(name)
		body.WriteString(line + "\n")
		if _, seen := requested[line]; !seen {
			requested[line] = name
		}
	}
	// The session's own client (bookmark transport, refused redirects,
	// bounded time) with ONE re-auth on 401 — the lifecycle the wire
	// library gives every other verb.
	send := func() (*http.Response, error) {
		credentials, err := s.Credentials(ctx, 0)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			credentials.StorageURL+"?bulk-delete", strings.NewReader(body.String()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Auth-Token", credentials.Token)
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Accept", "application/json")
		return s.raw.Do(req)
	}
	resp, err := send()
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		s.ForceReauth()
		resp, err = send()
		if err == nil && resp.StatusCode == http.StatusUnauthorized {
			s.ForceReauth()
		}
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
		Errors         [][]string      `json:"Errors"`
		ResponseBody   json.RawMessage `json:"Response Body"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("bulk-delete response: %w", err)
	}
	result := &BulkDeleteResult{
		Deleted:  decoded.NumberDeleted,
		NotFound: decoded.NumberNotFound,
	}
	// A documented row is exactly a path and a status. A row of any
	// other shape is not discarded quietly: it is an anomaly, and its
	// contents are kept as far as they go.
	rows := make([][2]string, 0, len(decoded.Errors))
	shaped := make([][2]string, 0, len(decoded.Errors))
	for _, row := range decoded.Errors {
		padded := [2]string{}
		copy(padded[:], row)
		rows = append(rows, padded)
		if len(row) != 2 {
			// A row of the wrong shape is not a disposition about
			// anything. It is retained raw and reported, and it never
			// reaches identity resolution.
			result.Anomalies = append(result.Anomalies,
				fmt.Sprintf("a row carries %d elements where the reply format has two", len(row)))
			continue
		}
		shaped = append(shaped, padded)
	}
	result.Errors = rows
	// Validate the reply against this request before anything trusts it.
	// Arithmetic alone accepted replies that named the wrong names, so
	// membership, uniqueness, status shape and counts are all checked,
	// and any violation makes the whole reply untrusted (K-DEL-002).
	seen := make(map[string]string, len(decoded.Errors))
	distinct := int64(0)
	for _, row := range shaped {
		// The path is compared exactly as the reply sent it. Trimming or
		// otherwise normalising it here would let a row that is not one
		// of ours become one of our identities, which no part of the
		// contract authorises.
		name, ok := requested[row[0]]
		if !ok {
			result.UnmatchedPaths = append(result.UnmatchedPaths, row[0])
			result.Anomalies = append(result.Anomalies,
				fmt.Sprintf("a row names %q, which this request did not send", row[0]))
			continue
		}
		if !failureStatus(row[1]) {
			result.Anomalies = append(result.Anomalies,
				fmt.Sprintf("the row for %q carries status %q, which does not denote a failure", name, row[1]))
			continue
		}
		if previous, repeated := seen[name]; repeated {
			result.Anomalies = append(result.Anomalies,
				fmt.Sprintf("the reply names %q more than once, first as %q and again as %q", name, previous, row[1]))
			continue
		}
		seen[name] = row[1]
		distinct++
		result.Failures = append(result.Failures, BulkDeleteFailure{Name: name, Status: row[1]})
	}
	if result.Deleted < 0 || result.NotFound < 0 {
		result.Anomalies = append(result.Anomalies,
			fmt.Sprintf("the reply reports negative counts: %d deleted and %d not found", result.Deleted, result.NotFound))
	}
	// Cardinality must reconcile: every requested name is deleted, not
	// found, or individually errored — an undercounted 200 cannot pass
	// as success (K-DEL-002).
	if accounted := result.Deleted + result.NotFound + distinct; accounted != int64(len(names)) {
		result.Anomalies = append(result.Anomalies,
			fmt.Sprintf("the reply accounts for %d of %d requested names", accounted, len(names)))
	}
	// The middleware reports overall failure through Response Status
	// while still returning 200 on the wire; a status that is not the
	// documented success needs a valid row to explain it.
	overall, wellFormed := statusCode(decoded.ResponseStatus)
	switch {
	case !wellFormed:
		result.Anomalies = append(result.Anomalies,
			fmt.Sprintf("the reply carries overall status %q, which is not a status", decoded.ResponseStatus))
	case overall != http.StatusOK && distinct == 0:
		result.Anomalies = append(result.Anomalies,
			fmt.Sprintf("the reply carries overall status %q with no row explaining it", decoded.ResponseStatus))
	}
	result.Validated = len(result.Anomalies) == 0
	if !result.Validated {
		return result, fmt.Errorf("bulk-delete reply is not dependable: %s", strings.Join(result.Anomalies, "; "))
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

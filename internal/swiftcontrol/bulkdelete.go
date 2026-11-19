package swiftcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// BulkDeleteFailure is one per-item failure resolved back to the name
// the caller asked to delete. The reply returns a container-prefixed,
// percent-encoded path that is the service's own spelling of the name,
// not the line this client sent, so the row is resolved by decoding it
// and comparing identities here — in the only place that knows how the
// line was built — rather than by matching text.
type BulkDeleteFailure struct {
	// Name is the requested object name, spelled exactly as the caller
	// gave it.
	Name string
	// Status keeps the reported code with a local reason phrase, because
	// provider phrases can reflect credentials into a job's failure message.
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
	// is the service's spelling of a container-prefixed path, not an
	// object name, and not necessarily the bytes this client sent.
	Errors [][2]string
	// Failures are those rows resolved to requested names, in reply
	// order and preserving repeats, so a caller can see exactly what the
	// reply said about names it asked about.
	Failures []BulkDeleteFailure
	// MalformedRows are rows whose shape is not the documented pair,
	// retained whole rather than truncated, so the evidence of a bad
	// reply survives the boundary. Bounded, and never a disposition.
	MalformedRows [][]string
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

// resolveReplyPath splits a reply's row path into the container and
// object bytes it names.
//
// The spelling cannot be compared directly. Swift decodes the line it
// was sent and quotes the name again for its error response with a
// stricter encoder than the request used: a container sent as
// "plus+and&equals=at@colon:dollar$" comes back as
// "plus%2Band%26equals%3Dat%40colon%3Adollar%24", observed on the owned
// fixture. Two different spellings therefore name one object, so the
// comparison is on the decoded bytes. Decoding is exactly one layer, so
// percent-like text in a name survives, and a literal plus stays a plus
// because a path segment is not form data.
func resolveReplyPath(raw string) (container, object string, ok bool) {
	if !strings.HasPrefix(raw, "/") {
		return "", "", false
	}
	rest := raw[1:]
	slash := strings.Index(rest, "/")
	if slash <= 0 {
		return "", "", false
	}
	decodedContainer, err := url.PathUnescape(rest[:slash])
	if err != nil {
		return "", "", false
	}
	decodedObject, err := url.PathUnescape(rest[slash+1:])
	if err != nil || decodedObject == "" {
		return "", "", false
	}
	return decodedContainer, decodedObject, true
}

// maxRetainedMalformedRows bounds the malformed-row evidence a single
// reply can carry into a caller's record.
const maxRetainedMalformedRows = 20

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

// BulkDelete issues one Swift bulk-delete (§31.2 BulkDelete executor):
// an account-level POST ?bulk-delete whose text/plain body lists one
// URL-encoded "/container/object" per line. Deletion is manifest-only
// by construction — the middleware deletes exactly the named objects
// and never follows DLO/SLO segments. Newline-bearing names are safe
// here (unlike rclone lists): each path segment is percent-encoded, so
// a newline travels as %0A.
func (s *Session) BulkDelete(ctx context.Context, container string, names []string) (*BulkDeleteResult, error) {
	var body strings.Builder
	// The requested object names, kept so a reply's error rows can be
	// resolved back to them by identity rather than by spelling.
	requested := make(map[string]bool, len(names))
	for _, name := range names {
		body.WriteString("/" + url.PathEscape(container) + "/" + escapeObjectPath(name) + "\n")
		requested[name] = true
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
		// A store answer is classified as every other verb classifies it, so
		// a refused token is unauthorized and a client refusal is not a
		// server error.
		kind, retryable := classifyStatus(resp.StatusCode)
		message := http.StatusText(resp.StatusCode)
		if message == "" {
			message = "Swift request failed"
		}
		return nil, &SwiftError{Kind: kind, HTTPStatus: resp.StatusCode, Retryable: retryable,
			Message: message, TransID: transIDOf(resp.Header)}
	}

	var decoded struct {
		// The counts are pointers so a missing or null field is not read
		// as an observed zero: the protocol requires both, and absent
		// data is not an observation.
		NumberDeleted  *int64          `json:"Number Deleted"`
		NumberNotFound *int64          `json:"Number Not Found"`
		ResponseStatus string          `json:"Response Status"`
		Errors         [][]string      `json:"Errors"`
		ResponseBody   json.RawMessage `json:"Response Body"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		// Decoder errors can quote a provider value, including a reflected key.
		return nil, errors.New("bulk-delete response has invalid JSON or field values")
	}
	result := &BulkDeleteResult{}
	if decoded.NumberDeleted != nil {
		result.Deleted = *decoded.NumberDeleted
	} else {
		result.Anomalies = append(result.Anomalies, "the reply reports no deleted count")
	}
	if decoded.NumberNotFound != nil {
		result.NotFound = *decoded.NumberNotFound
	} else {
		result.Anomalies = append(result.Anomalies, "the reply reports no not-found count")
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
			// anything. It is retained whole and reported, and it never
			// reaches identity resolution.
			result.Anomalies = append(result.Anomalies,
				fmt.Sprintf("a row carries %d elements where the reply format has two", len(row)))
			if len(result.MalformedRows) < maxRetainedMalformedRows {
				result.MalformedRows = append(result.MalformedRows, append([]string(nil), row...))
			}
			continue
		}
		shaped = append(shaped, padded)
	}
	result.Errors = rows
	// Validate the reply against this request before anything trusts it.
	// Arithmetic alone accepted replies that named the wrong names, so
	// membership, uniqueness, status shape and counts are all checked,
	// and any violation makes the whole reply untrusted (K-DEL-002).
	seen := make(map[string]bool, len(decoded.Errors))
	distinct := int64(0)
	for _, row := range shaped {
		// The row is resolved to the bytes it names and matched against
		// the requested identities. Anything that does not decode, names
		// another container, or names something this request did not
		// send is not one of ours.
		rowContainer, name, resolved := resolveReplyPath(row[0])
		ok := resolved && rowContainer == container && requested[name]
		if !ok {
			result.UnmatchedPaths = append(result.UnmatchedPaths, row[0])
			result.Anomalies = append(result.Anomalies,
				"a row names an object this request did not send")
			continue
		}
		code, wellFormed := statusCode(row[1])
		if !wellFormed || code < 400 {
			result.Anomalies = append(result.Anomalies,
				fmt.Sprintf("the row for %q carries a status that does not denote a failure", name))
			continue
		}
		if seen[name] {
			result.Anomalies = append(result.Anomalies,
				fmt.Sprintf("the reply names %q more than once", name))
			continue
		}
		seen[name] = true
		distinct++
		status := strconv.Itoa(code)
		if reason := http.StatusText(code); reason != "" {
			status += " " + reason
		}
		result.Failures = append(result.Failures, BulkDeleteFailure{Name: name, Status: status})
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
	// The documented outcomes are the success the middleware reports when
	// everything it was asked for was handled, or a failure explained by
	// a valid row. Anything else — an informational, redirect or other
	// success code — is not a bulk-delete outcome at all, however well
	// formed it looks. The owned fixture answers a partial failure with
	// "400 Bad Request" and a row, which is why a failure explained by a
	// row is accepted.
	overall, wellFormed := statusCode(decoded.ResponseStatus)
	switch {
	case !wellFormed:
		result.Anomalies = append(result.Anomalies,
			"the reply carries a malformed overall status")
	case overall == http.StatusOK:
		// The documented success.
	case overall >= 400 && distinct > 0:
		// A failure the reply itself explains.
	case overall >= 400:
		result.Anomalies = append(result.Anomalies,
			fmt.Sprintf("the reply carries overall status %d with no row explaining it", overall))
	default:
		result.Anomalies = append(result.Anomalies,
			fmt.Sprintf("the reply carries overall status %d, which is not a bulk-delete outcome", overall))
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

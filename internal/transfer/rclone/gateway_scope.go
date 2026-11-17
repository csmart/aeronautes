package rclone

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	maxGatewayBulkBytes = 4 << 20
	maxGatewayBulkNames = 10000
)

// gatewayScope is where the child may write: everything under object in the
// container and in that container's segments container. An empty object is
// the whole container: an upload to a container's top writes each file at
// its own name there (ADR 0122 decision 3).
type gatewayScope struct {
	container string
	object    string
	// keepsObjects refuses every DELETE the child sends. The engine, writing
	// over a large object, deletes that object's old segments; this is how
	// they are kept where they still belong to something (destinationRules).
	keepsObjects bool
	// setsTimes lets the child POST to an object it may write. Told to
	// compare, the engine sets the time of an object it found the same but
	// for its time, by a POST, as it does used alone (ADR 0122 decision 12).
	setsTimes bool
}

// destinationRules say how a child may use its remote destination: whether
// it may delete there at all, and whether it may set an object's time.
type destinationRules struct {
	keepsObjects, setsTimes bool
}

func destinationScope(remote, destination string, rules destinationRules) (*gatewayScope, error) {
	path, matched := strings.CutPrefix(destination, remote+":")
	if !matched {
		return nil, nil
	}
	container, object, _ := strings.Cut(path, "/")
	object = strings.TrimSuffix(object, "/")
	if !gatewayPathSafe(container) || (object != "" && !gatewayPathSafe(object)) {
		return nil, fmt.Errorf("gateway destination needs a safe container and object path")
	}
	if err := checkRemoteDest(destination); err != nil {
		return nil, err
	}
	return &gatewayScope{container: container, object: object, keepsObjects: rules.keepsObjects, setsTimes: rules.setsTimes}, nil
}

// gatewayReadSafe is the rule for a path the child only reads. An empty
// segment is allowed there: it is an ordinary part of an object's name, and
// the engine, replacing a large object whose manifest names its segments
// with a trailing slash, as python-swiftclient writes it, asks for a name
// under that prefix with two slashes in a row. Refusing the read made the
// engine leave that object's old segments without saying so.
func gatewayReadSafe(path string) bool {
	if !utf8.ValidString(path) || strings.ContainsRune(path, 0) {
		return false
	}
	for segment := range strings.SplitSeq(path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func gatewayPathSafe(path string) bool {
	if !utf8.ValidString(path) || strings.ContainsRune(path, 0) {
		return false
	}
	for segment := range strings.SplitSeq(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func (s *gatewayScope) allowsObject(container, object string) bool {
	if s == nil || !gatewayPathSafe(object) {
		return false
	}
	under := s.object + "/"
	if s.object == "" {
		under = ""
	}
	if container == s.container {
		return strings.HasPrefix(object, under)
	}
	return container == s.container+"_segments" && strings.HasPrefix(object, under)
}

func (s *gatewayScope) allowsWrite(req *http.Request, relative string) bool {
	if s == nil || req.URL.RawQuery != "" || req.URL.ForceQuery {
		return false
	}
	container, object, hasObject := strings.Cut(relative, "/")
	if !hasObject {
		if req.Method != http.MethodPut || container != s.container+"_segments" || req.ContentLength != 0 {
			return false
		}
		// The one policy exception is checked against a fresh native HEAD
		// after these structural checks pass.
		for name := range req.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-") && !strings.EqualFold(name, "X-Auth-Token") && !strings.EqualFold(name, "X-Storage-Policy") {
				return false
			}
		}
		if values, present := req.Header["X-Storage-Policy"]; present && (len(values) != 1 || values[0] == "") {
			return false
		}
		return true
	}
	if !s.allowsObject(container, object) {
		return false
	}
	for _, name := range []string{"X-Copy-From", "X-Copy-From-Account", "Destination", "Destination-Account", "X-Static-Large-Object", "X-Storage-Policy"} {
		if _, present := req.Header[http.CanonicalHeaderKey(name)]; present {
			return false
		}
	}
	manifests, present := req.Header["X-Object-Manifest"]
	if !present {
		return true
	}
	if req.Method != http.MethodPut || container != s.container || len(manifests) != 1 {
		return false
	}
	// Swift unquotes this header once when assembling a DLO. Comparing
	// its wire spelling would permit an encoded escape from the scope.
	manifest, err := url.PathUnescape(manifests[0])
	if err != nil {
		return false
	}
	segmentContainer, prefix, found := strings.Cut(manifest, "/")
	return found && segmentContainer == s.container+"_segments" &&
		gatewayPathSafe(strings.TrimSuffix(prefix, "/")) && strings.HasPrefix(prefix, object+"/") && len(prefix) > len(object)+1
}

func (s *gatewayScope) validBulkBody(req *http.Request, body []byte) bool {
	mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/plain" || s == nil || len(body) == 0 || len(body) > maxGatewayBulkBytes {
		return false
	}
	count := 0
	remaining := string(body)
	for remaining != "" {
		line, rest, _ := strings.Cut(remaining, "\n")
		remaining = rest
		// Match Swift's bytes.strip before unquoting, not Unicode space
		// trimming afterwards: encoded spaces are part of an object name.
		name, err := url.PathUnescape(strings.Trim(line, " \t\r\v\f"))
		if err != nil {
			return false
		}
		name = strings.TrimPrefix(name, "/")
		container, object, found := strings.Cut(name, "/")
		count++
		if !found || !s.allowsObject(container, object) || count > maxGatewayBulkNames {
			return false
		}
	}
	return count > 0
}

func isGatewayBulk(req *http.Request, accountPath string) bool {
	if req.Method != http.MethodDelete || req.URL.Path != accountPath {
		return false
	}
	query, err := url.ParseQuery(req.URL.RawQuery)
	return err == nil && len(query) == 1 && len(query["bulk-delete"]) == 1 && query.Get("bulk-delete") == "1"
}

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

type gatewayScope struct {
	container string
	object    string
	exact     bool
}

func destinationScope(remote, destination string, exact bool) (*gatewayScope, error) {
	path, matched := strings.CutPrefix(destination, remote+":")
	if !matched {
		return nil, nil
	}
	container, object, found := strings.Cut(path, "/")
	if !exact {
		object = strings.TrimSuffix(object, "/")
	}
	if !found || !gatewayPathSafe(container) || !gatewayPathSafe(object) {
		return nil, fmt.Errorf("gateway destination needs a safe container and staged object path")
	}
	if err := checkRemoteDestStaged(destination); err != nil {
		return nil, err
	}
	return &gatewayScope{container: container, object: object, exact: exact}, nil
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
	if container == s.container {
		if s.exact {
			return object == s.object
		}
		return strings.HasPrefix(object, s.object+"/")
	}
	return container == s.container+"_segments" && strings.HasPrefix(object, s.object+"/")
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

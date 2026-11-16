package systemtest

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
)

// Helpers the upload, overwrite and segment tests share. The staged upload
// route they were written for went with ADR 0122.

// appAgent is the user agent the application's own session sends
// (internal/swiftcontrol/session.go). The pinned engine sends its own, so
// this is what separates the application's requests from the engine's.
const appAgent = "aeronautes"

// Raw helpers. Each goes through the account's own storage URL rather
// than the proxied one, so a fixture's read is never mistaken for the
// route's own request.

func rawRequest(a *Account, method, path, query string, body []byte) (int, []byte, error) {
	url := a.storageURL + path
	if query != "" {
		url += "?" + query
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("X-Auth-Token", a.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := a.rawClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	answered, err := io.ReadAll(resp.Body)
	return resp.StatusCode, answered, err
}

func rawGet(a *Account, path, query string) ([]byte, error) {
	status, body, err := rawRequest(a, http.MethodGet, path, query, nil)
	if err != nil {
		return nil, err
	}
	if status/100 != 2 {
		return nil, fmt.Errorf("raw read %s: HTTP %d", path, status)
	}
	return body, nil
}

// inverted returns bytes of the same length that are not the original.
func inverted(body []byte) []byte {
	out := append([]byte(nil), body...)
	for i := range out {
		out[i] ^= 0xff
	}
	return out
}

func md5Hex(body []byte) string {
	sum := md5.Sum(body)
	return hex.EncodeToString(sum[:])
}

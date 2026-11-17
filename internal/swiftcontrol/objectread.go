package swiftcontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ncw/swift/v2"
)

// OpenObjectContent reads an object's stored bytes whole, for a caller that
// needs an independent read of what the store holds, as the service
// journeys do. The caller must consume EOF and check Close before accepting
// the content.
func (s *Session) OpenObjectContent(ctx context.Context, container, name string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, Normalize(err)
	}
	if err := s.AdmitRequest(ctx); err != nil {
		return nil, err
	}
	response, headers, err := s.conn.Call(ctx, "", swift.RequestOpts{
		Container: container, ObjectName: name, Operation: http.MethodGet,
		OnReAuth: s.auth.storageTarget, OwnedRead: true,
	})
	if err != nil {
		if response != nil && response.Body != nil {
			err = errors.Join(err, response.Body.Close())
		}
		return nil, WithTransID(Normalize(err), headers)
	}
	complete := response.StatusCode == http.StatusOK
	for key := range response.Header {
		// Header.Get cannot distinguish an absent field from an empty one.
		if strings.EqualFold(key, "Content-Range") {
			complete = false
		}
	}
	if !complete {
		err = fmt.Errorf("object content requires HTTP 200 without Content-Range (received HTTP %d)", response.StatusCode)
		return nil, WithTransID(Normalize(errors.Join(err, response.Body.Close())), headers)
	}
	return &objectContentReader{body: response.Body, headers: headers}, nil
}

type objectContentReader struct {
	body    io.ReadCloser
	headers swift.Headers
}

func (r *objectContentReader) Read(buffer []byte) (int, error) {
	n, err := r.body.Read(buffer)
	if err == io.EOF {
		return n, err
	}
	return n, WithTransID(Normalize(err), r.headers)
}

func (r *objectContentReader) Close() error {
	return WithTransID(Normalize(r.body.Close()), r.headers)
}

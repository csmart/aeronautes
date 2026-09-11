package copy

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

type stagedContentTestBody struct {
	data         []byte
	terminal     error
	closeErr     error
	separateEOF  bool
	emptyReads   int
	noProgress   bool
	chunk        int
	onRead       func()
	onClose      func()
	reads        int
	closes       int
	largestRead  int
	invalidCount bool
}

func (b *stagedContentTestBody) Read(p []byte) (int, error) {
	b.reads++
	b.largestRead = max(b.largestRead, len(p))
	if b.onRead != nil {
		b.onRead()
	}
	if b.invalidCount {
		return len(p) + 1, b.terminal
	}
	if b.noProgress || b.emptyReads > 0 {
		b.emptyReads--
		return 0, nil
	}
	if b.chunk > 0 {
		p = p[:min(len(p), b.chunk)]
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	if len(b.data) != 0 {
		return n, nil
	}
	if b.terminal != nil {
		return n, b.terminal
	}
	if b.separateEOF && n > 0 {
		return n, nil
	}
	return n, io.EOF
}

func (b *stagedContentTestBody) Close() error {
	b.closes++
	if b.onClose != nil {
		b.onClose()
	}
	return b.closeErr
}

func TestStagedContentComparison(t *testing.T) {
	readFailure := errors.New("content read failed")
	closeFailure := errors.New("content close failed")
	large := bytes.Repeat([]byte("stored opaque bytes\x00\xff"), 100000)
	for _, tc := range []struct {
		name         string
		data         []byte
		want         []byte
		length       int64
		body         stagedContentTestBody
		code         Code
		causes       []error
		cancelBefore bool
		cancelRead   bool
		cancelClose  bool
	}{
		{name: "bytes_with_eof", data: []byte("DATA"), want: []byte("DATA"), length: 4},
		{name: "separate_eof", data: []byte("DATA"), want: []byte("DATA"), length: 4, body: stagedContentTestBody{separateEOF: true}},
		{name: "empty", length: 0},
		{name: "bounded_buffer", data: large, want: large, length: int64(len(large)), body: stagedContentTestBody{chunk: 4093}},
		{name: "occasional_empty_read", data: []byte("DATA"), want: []byte("DATA"), length: 4, body: stagedContentTestBody{emptyReads: 3}},
		{name: "same_size_mismatch", data: []byte("EVIL"), want: []byte("DATA"), length: 4, code: CodeUnverified},
		{name: "truncated", data: []byte("DAT"), want: []byte("DATA"), length: 4, code: CodeIncomplete},
		{name: "truncated_with_matching_md5", data: []byte("DATA"), want: []byte("DATA"), length: 5, code: CodeIncomplete},
		{name: "excess", data: []byte("DATAX"), want: []byte("DATA"), length: 4, code: CodeIncomplete},
		{name: "excess_with_matching_md5", data: []byte("DATA"), want: []byte("DATA"), length: 3, code: CodeIncomplete},
		{name: "excess_on_next_read", data: []byte("DATAX"), want: []byte("DATA"), length: 4, body: stagedContentTestBody{chunk: 4}, code: CodeIncomplete},
		{name: "maximal_length", data: []byte("DATA"), want: []byte("DATA"), length: math.MaxInt64, code: CodeIncomplete},
		{name: "read_error_after_matching_bytes", data: []byte("DATA"), want: []byte("DATA"), length: 4, body: stagedContentTestBody{terminal: readFailure}, code: CodeIO, causes: []error{readFailure}},
		{name: "eof_joined_with_failure", data: []byte("DATA"), want: []byte("DATA"), length: 4, body: stagedContentTestBody{terminal: errors.Join(io.EOF, readFailure)}, code: CodeIO, causes: []error{readFailure}},
		{name: "close_error_after_match", data: []byte("DATA"), want: []byte("DATA"), length: 4, body: stagedContentTestBody{closeErr: closeFailure}, code: CodeIO, causes: []error{closeFailure}},
		{name: "read_and_close_failure", data: []byte("DATA"), want: []byte("DATA"), length: 4, body: stagedContentTestBody{terminal: readFailure, closeErr: closeFailure}, code: CodeIO, causes: []error{readFailure, closeFailure}},
		{name: "mismatch_and_close_failure", data: []byte("EVIL"), want: []byte("DATA"), length: 4, body: stagedContentTestBody{closeErr: closeFailure}, code: CodeUnverified, causes: []error{closeFailure}},
		{name: "excess_and_read_failure", data: []byte("DATAX"), want: []byte("DATA"), length: 4, body: stagedContentTestBody{terminal: readFailure}, code: CodeIncomplete, causes: []error{readFailure}},
		{name: "no_progress", length: 0, body: stagedContentTestBody{noProgress: true}, code: CodeIO, causes: []error{io.ErrNoProgress}},
		{name: "invalid_reader_count", length: 4, body: stagedContentTestBody{invalidCount: true, terminal: readFailure}, code: CodeIO, causes: []error{readFailure}},
		{name: "caller_cancelled", data: []byte("DATA"), want: []byte("DATA"), length: 4, cancelBefore: true, code: CodeCancelled, causes: []error{context.Canceled}},
		{name: "cancelled_during_final_read", data: []byte("DATA"), want: []byte("DATA"), length: 4, cancelRead: true, code: CodeCancelled, causes: []error{context.Canceled}},
		{name: "cancelled_during_close", data: []byte("DATA"), want: []byte("DATA"), length: 4, cancelClose: true, code: CodeCancelled, causes: []error{context.Canceled}},
		{name: "read_deadline", data: []byte("DATA"), want: []byte("DATA"), length: 4, body: stagedContentTestBody{terminal: context.DeadlineExceeded}, code: CodeTimeout, causes: []error{context.DeadlineExceeded}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			body := tc.body
			body.data = tc.data
			if tc.cancelBefore {
				cancel()
			}
			if tc.cancelRead {
				body.onRead = cancel
			}
			if tc.cancelClose {
				body.onClose = cancel
			}
			code, err := compareStagedContent(ctx, &body, tc.length, md5.Sum(tc.want))
			if (err == nil) != (tc.code == "") {
				t.Fatalf("comparison error = %v, want code %q", err, tc.code)
			}
			if diagnostic := ClassifyDiagnostic(StageStaging, code, err); diagnostic.Code != tc.code {
				t.Errorf("diagnostic = %+v, want code %q (error %v)", diagnostic, tc.code, err)
			}
			for _, cause := range tc.causes {
				if !errors.Is(err, cause) {
					t.Errorf("error %v lost cause %v", err, cause)
				}
			}
			if body.closes != 1 || body.largestRead > 64*1024 {
				t.Errorf("content ownership: closes=%d largest read=%d", body.closes, body.largestRead)
			}
			if tc.cancelBefore && body.reads != 0 {
				t.Errorf("cancelled comparison started %d reads", body.reads)
			}
			if tc.body.noProgress && body.reads > 1000 {
				t.Errorf("empty reader did not stop promptly: %d reads", body.reads)
			}
		})
	}
}

func TestStagedContentExpectationBeforeGET(t *testing.T) {
	for _, tc := range []struct {
		name   string
		length int64
		tag    string
		valid  bool
	}{
		{name: "negative_length", length: -1, tag: md5hex("DATA")},
		{name: "empty_tag", length: 4},
		{name: "short_tag", length: 4, tag: "abcd"},
		{name: "long_tag", length: 4, tag: strings.Repeat("a", 1024)},
		{name: "not_hex", length: 4, tag: strings.Repeat("g", 32)},
		{name: "quoted_tag", length: 4, tag: `"` + md5hex("DATA") + `"`},
		{name: "lower_hex", length: 4, tag: md5hex("DATA"), valid: true},
		{name: "upper_hex", length: 4, tag: strings.ToUpper(md5hex("DATA")), valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			seed(w.dstMock, "backup", "stage", []byte("DATA"), nil)
			e := newExecutor(t, w, nil)
			before := w.dstMock.Requests()
			code, err := e.verifyStagedContent(context.Background(), &swiftcontrol.ObjectDetail{ContentLength: tc.length, ETag: tc.tag}, "stage")
			if tc.valid {
				if err != nil || code != "" || w.dstMock.Requests()-before != 1 {
					t.Fatalf("valid expectation: code=%q error=%v requests=%d", code, err, w.dstMock.Requests()-before)
				}
			} else if err == nil || code != CodeIncomplete || w.dstMock.Requests() != before {
				t.Fatalf("invalid expectation: code=%q error=%v requests=%d", code, err, w.dstMock.Requests()-before)
			}
		})
	}
}

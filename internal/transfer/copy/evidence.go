package copy

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
)

// The native copy route publishes unstamped (ADR 0045), so what a successful
// copy proved exists only inside the job that proved it, and a name the route
// recorded as a content failure has to be kept out of that job's verified
// counts, Same verdicts and skips. Both facts are per name, which makes them a
// namespace-sized collection, and K-PERF-001 forbids accumulating one: a job
// over a million names may not hold a million entries.
//
// So they go to the same kind of private spool the refusal decisions use — a
// sorted, length-framed, digested temp file written during execution and read
// forward during verification, one record resident at a time. This deliberately
// mirrors refusalLog rather than generalising it: that log's count is
// cross-checked against a reported counter and its wording is part of an audited
// refusal path, and widening it to carry payloads would put this slice inside it.
//
// Nothing here is durable or shared. A later run opens no spool, which is
// exactly why a later equality or skip decision on an unstamped object stays
// conservative.
type evidenceLog struct {
	file   *os.File
	count  int64
	size   int64
	last   string
	digest hash.Hash
	broken error
	closed bool
}

// evidenceKind is what this job observed about a name. Zero means nothing was
// recorded, which is every name the native route did not decide.
type evidenceKind byte

const (
	evidenceNone           evidenceKind = 0
	evidenceCopied         evidenceKind = 1
	evidenceContentFailure evidenceKind = 2
)

// evidenceNameLimit matches the refusal spool's: past Swift's own name limit,
// and a bound on a corrupt length before anything is allocated for it.
const evidenceNameLimit = 64 << 10

// evidencePayloadLimit bounds the one payload this log carries, a source
// identity and a destination digest joined by a separator, both short and
// fixed-shape.
const evidencePayloadLimit = 1 << 10

// copied records that this job's native route published name and then found the
// live destination holding exactly what it had verified. identity is the source
// observation the comparison was made against — the same value the fingerprint
// used to assert remotely — and digest is what the destination must still report.
func (l *evidenceLog) copied(name, identity, digest string) error {
	if strings.Contains(identity, "\x00") {
		return errors.New("source identity cannot contain the payload separator")
	}
	return l.append(name, evidenceCopied, identity+"\x00"+digest)
}

// contentFailure records that this job observed name's content disagree. ADR
// 0045 excludes such a name from a verified count, a Same verdict and a skip in
// the same job, independently of any stamp.
func (l *evidenceLog) contentFailure(name string) error {
	return l.append(name, evidenceContentFailure, "")
}

func (l *evidenceLog) append(name string, kind evidenceKind, payload string) (err error) {
	defer func() {
		if err != nil && l.broken == nil {
			l.broken = err
		}
	}()
	if l.broken != nil {
		return l.broken
	}
	if l.closed {
		return errors.New("copy evidence record is closed")
	}
	if len(name) > evidenceNameLimit || len(payload) > evidencePayloadLimit {
		return errors.New("copy evidence name or payload exceeds the limit")
	}
	if kind != evidenceCopied && kind != evidenceContentFailure {
		return fmt.Errorf("copy evidence kind %d is not recorded", kind)
	}
	// Execution visits planned names in ascending order, which is what lets
	// verification read this forward instead of holding it. A name out of order
	// means that assumption has broken somewhere, so refuse rather than write a
	// record a forward read would silently skip.
	if l.count != 0 && name <= l.last {
		return errors.New("copy evidence names are not strictly ordered")
	}
	if l.file == nil {
		l.file, err = os.CreateTemp("", "aeronautes-copy-evidence-*")
		if err != nil {
			return fmt.Errorf("create copy evidence record: %w", err)
		}
		l.digest = sha256.New()
	}
	record := make([]byte, 4+len(name)+1+4+len(payload))
	binary.BigEndian.PutUint32(record, uint32(len(name)))
	n := copy(record[4:], name) + 4
	record[n] = byte(kind)
	n++
	binary.BigEndian.PutUint32(record[n:], uint32(len(payload)))
	n += 4
	copy(record[n:], payload)
	written, err := l.file.Write(record)
	if err != nil {
		return fmt.Errorf("write copy evidence record: %w", err)
	}
	if written != len(record) {
		return io.ErrShortWrite
	}
	_, _ = l.digest.Write(record)
	l.count++
	l.size += int64(written)
	l.last = name
	return nil
}

func (l *evidenceLog) close() error {
	if l.closed {
		return nil
	}
	l.closed = true
	if l.file == nil {
		return nil
	}
	return errors.Join(l.file.Close(), os.Remove(l.file.Name()))
}

func (l *evidenceLog) checkSize() error {
	if l.file == nil {
		return errors.New("copy evidence record is unavailable")
	}
	info, err := l.file.Stat()
	if err != nil {
		return fmt.Errorf("read copy evidence record: %w", err)
	}
	if info.Size() != l.size {
		return errors.New("copy evidence record size changed")
	}
	return nil
}

type evidenceReader struct {
	log     *evidenceLog
	input   io.Reader
	digest  hash.Hash
	count   int64
	name    string
	kind    evidenceKind
	payload string
	has     bool
	done    bool
}

// reader opens the spool for the verification pass. A write that failed is
// returned here rather than swallowed: evidence this job could not store is not
// evidence, and the pass that would have used it has to say so.
func (l *evidenceLog) reader() (*evidenceReader, error) {
	if l.broken != nil {
		return nil, fmt.Errorf("incomplete copy evidence record: %w", l.broken)
	}
	if l.closed {
		return nil, errors.New("copy evidence record is unavailable")
	}
	if l.count == 0 {
		return &evidenceReader{done: true}, nil
	}
	if err := l.checkSize(); err != nil {
		return nil, err
	}
	// Integrity BEFORE any decision rests on it. A forward read answers "this
	// name is absent" from the records it has passed, and a truncated record or
	// one substituted for another of the same length cannot be noticed by that
	// answer: the digest only settles at the end of the file. An absence is what
	// releases a name to the pass's success paths, so a spool that was not the
	// one written could produce verified credit that is then only contradicted
	// afterwards. The whole spool is therefore validated here first, in one
	// sequential pass holding one record at a time, and the forward read that
	// follows decides against a file already known to be the one that was
	// written.
	if err := l.validate(); err != nil {
		return nil, err
	}
	return &evidenceReader{log: l, input: io.NewSectionReader(l.file, 0, l.size), digest: sha256.New()}, nil
}

// validate streams the whole spool and settles its count, digest and size. It
// allocates one record at a time, like the read it precedes.
func (l *evidenceLog) validate() error {
	scan := &evidenceReader{log: l, input: io.NewSectionReader(l.file, 0, l.size), digest: sha256.New()}
	return scan.finish()
}

func (r *evidenceReader) advance() error {
	var length [4]byte
	_, err := io.ReadFull(r.input, length[:])
	if errors.Is(err, io.EOF) {
		if r.count != r.log.count || !bytes.Equal(r.digest.Sum(nil), r.log.digest.Sum(nil)) {
			return errors.New("copy evidence record is incomplete or corrupt")
		}
		if err := r.log.checkSize(); err != nil {
			return err
		}
		r.done, r.has = true, false
		return nil
	}
	if err != nil {
		return fmt.Errorf("read copy evidence record length: %w", err)
	}
	n := binary.BigEndian.Uint32(length[:])
	if n > evidenceNameLimit {
		return errors.New("copy evidence record name exceeds the limit")
	}
	name := make([]byte, int(n))
	if _, err := io.ReadFull(r.input, name); err != nil {
		return fmt.Errorf("read copy evidence record name: %w", err)
	}
	var framing [5]byte
	if _, err := io.ReadFull(r.input, framing[:]); err != nil {
		return fmt.Errorf("read copy evidence record kind: %w", err)
	}
	kind := evidenceKind(framing[0])
	if kind != evidenceCopied && kind != evidenceContentFailure {
		return fmt.Errorf("copy evidence record states kind %d", kind)
	}
	size := binary.BigEndian.Uint32(framing[1:])
	if size > evidencePayloadLimit {
		return errors.New("copy evidence record payload exceeds the limit")
	}
	payload := make([]byte, int(size))
	if _, err := io.ReadFull(r.input, payload); err != nil {
		return fmt.Errorf("read copy evidence record payload: %w", err)
	}
	if r.count != 0 && string(name) <= r.name {
		return errors.New("copy evidence record names are not strictly ordered")
	}
	_, _ = r.digest.Write(length[:])
	_, _ = r.digest.Write(name)
	_, _ = r.digest.Write(framing[:])
	_, _ = r.digest.Write(payload)
	r.count++
	r.name, r.kind, r.payload, r.has = string(name), kind, string(payload), true
	return nil
}

// at answers what this job recorded about one name, reading forward only. The
// verification pass visits names in the same ascending order execution wrote
// them, so a name the spool does not hold is passed over without the spool ever
// being searched or held.
func (r *evidenceReader) at(name string) (evidenceKind, string, error) {
	for !r.done {
		if r.has && r.name >= name {
			if r.name == name {
				return r.kind, r.payload, nil
			}
			return evidenceNone, "", nil
		}
		if err := r.advance(); err != nil {
			return evidenceNone, "", err
		}
	}
	return evidenceNone, "", nil
}

// finish consumes the tail, so a truncated or substituted record cannot escape
// validation by sitting past the last name the pass happened to ask about.
func (r *evidenceReader) finish() error {
	for !r.done {
		if err := r.advance(); err != nil {
			return err
		}
	}
	return nil
}

// sourceIdentityAndDigest splits a recorded copied payload.
func sourceIdentityAndDigest(payload string) (identity, digest string, ok bool) {
	identity, digest, ok = strings.Cut(payload, "\x00")
	return identity, digest, ok
}

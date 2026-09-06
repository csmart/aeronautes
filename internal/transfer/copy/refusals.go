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
)

// Refusals cannot occupy the bounded failure examples: enough refused
// names would hide a copied object's later mismatch. A sorted, private
// spool preserves the execution decision without retaining a namespace
// in memory. Its count and digest detect missing or substituted records.
type refusalLog struct {
	file   *os.File
	count  int64
	size   int64
	last   string
	digest hash.Hash
	broken error
	closed bool
}

// This exceeds Swift's object-name limit while bounding corrupt lengths
// before allocation. Length framing also preserves newlines and empty
// relative names without escaping or normalising their identities.
const refusalNameLimit = 64 << 10

func (l *refusalLog) append(name string) (err error) {
	defer func() {
		if err != nil && l.broken == nil {
			l.broken = err
		}
	}()
	if l.broken != nil {
		return l.broken
	}
	if l.closed {
		return errors.New("refusal record is closed")
	}
	if len(name) > refusalNameLimit || (l.count != 0 && name <= l.last) {
		return errors.New("refusal names exceed the limit or are not strictly ordered")
	}
	if l.file == nil {
		l.file, err = os.CreateTemp("", "aeronautes-copy-refusals-*")
		if err != nil {
			return fmt.Errorf("create refusal record: %w", err)
		}
		l.digest = sha256.New()
	}
	record := make([]byte, 4+len(name))
	binary.BigEndian.PutUint32(record, uint32(len(name)))
	copy(record[4:], name)
	n, err := l.file.Write(record)
	if err != nil {
		return fmt.Errorf("write refusal record: %w", err)
	}
	if n != len(record) {
		return io.ErrShortWrite
	}
	_, _ = l.digest.Write(record)
	l.count++
	l.size += int64(n)
	l.last = name
	return nil
}

func (l *refusalLog) close() error {
	if l.closed {
		return nil
	}
	l.closed = true
	if l.file == nil {
		return nil
	}
	return errors.Join(l.file.Close(), os.Remove(l.file.Name()))
}

type refusalReader struct {
	log    *refusalLog
	input  io.Reader
	digest hash.Hash
	count  int64
	name   string
	has    bool
	done   bool
}

func (l *refusalLog) reader(expected int64) (*refusalReader, error) {
	if l.broken != nil {
		return nil, fmt.Errorf("incomplete refusal record: %w", l.broken)
	}
	if l.count != expected || l.closed {
		return nil, errors.New("refusal record is incomplete or unavailable")
	}
	if expected == 0 {
		return &refusalReader{done: true}, nil
	}
	if err := l.checkSize(); err != nil {
		return nil, err
	}
	return &refusalReader{log: l, input: io.NewSectionReader(l.file, 0, l.size), digest: sha256.New()}, nil
}

func (l *refusalLog) checkSize() error {
	if l.file == nil {
		return errors.New("refusal record is unavailable")
	}
	info, err := l.file.Stat()
	if err != nil {
		return fmt.Errorf("read refusal record: %w", err)
	}
	if info.Size() != l.size {
		return errors.New("refusal record size changed")
	}
	return nil
}

func (r *refusalReader) advance() error {
	var length [4]byte
	_, err := io.ReadFull(r.input, length[:])
	if errors.Is(err, io.EOF) {
		if r.count != r.log.count || !bytes.Equal(r.digest.Sum(nil), r.log.digest.Sum(nil)) {
			return errors.New("refusal record is incomplete or corrupt")
		}
		if err := r.log.checkSize(); err != nil {
			return err
		}
		r.done, r.has = true, false
		return nil
	}
	if err != nil {
		return fmt.Errorf("read refusal record length: %w", err)
	}
	n := binary.BigEndian.Uint32(length[:])
	if n > refusalNameLimit {
		return errors.New("refusal record name exceeds the limit")
	}
	name := make([]byte, int(n))
	if _, err := io.ReadFull(r.input, name); err != nil {
		return fmt.Errorf("read refusal record name: %w", err)
	}
	if r.count != 0 && string(name) <= r.name {
		return errors.New("refusal record names are not strictly ordered")
	}
	_, _ = r.digest.Write(length[:])
	_, _ = r.digest.Write(name)
	r.count++
	r.name, r.has = string(name), true
	return nil
}

func (r *refusalReader) contains(name string) (bool, error) {
	for !r.done {
		if r.has && r.name >= name {
			return r.name == name, nil
		}
		if err := r.advance(); err != nil {
			return false, err
		}
	}
	return false, nil
}

func (r *refusalReader) finish() error {
	// Refused objects may disappear from fresh listings. Consume all
	// original decisions so a missing tail cannot escape validation.
	for !r.done {
		if err := r.advance(); err != nil {
			return err
		}
	}
	return nil
}

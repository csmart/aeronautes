package app

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/csmart/aeronautes/internal/transfer/plan"
)

// copyPlanSpool holds an acknowledged copy's admitted plan on disk, so the job
// can read the whole plan once, refuse a changed collision set before it
// dispatches anything, and then execute THAT plan — without retaining the
// namespace in memory (K-PERF-001). It follows the executor's private-spool
// approach: a lazily created temp file removed on close. The count catches a
// truncated spool on replay rather than silently executing a short plan.
type copyPlanSpool struct {
	file   *os.File
	enc    *gob.Encoder
	count  int64
	err    error
	closed bool
}

// add appends one admitted item. A write failure latches, so the first error
// stops the run rather than executing a partial plan.
func (s *copyPlanSpool) add(item plan.Item) error {
	if s.err != nil {
		return s.err
	}
	if s.file == nil {
		file, err := os.CreateTemp("", "aeronautes-copy-plan-*")
		if err != nil {
			s.err = fmt.Errorf("create copy plan spool: %w", err)
			return s.err
		}
		s.file, s.enc = file, gob.NewEncoder(file)
	}
	if err := s.enc.Encode(&item); err != nil {
		s.err = fmt.Errorf("spool copy plan item: %w", err)
		return s.err
	}
	s.count++
	return nil
}

// replay reads the admitted plan back in the order it was written and calls fn
// for each item. A short read, or fewer items than were spooled, is an error
// rather than a silently truncated execution.
func (s *copyPlanSpool) replay(fn func(plan.Item) error) error {
	if s.err != nil {
		return s.err
	}
	if s.file == nil {
		return nil
	}
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind copy plan spool: %w", err)
	}
	decoder := gob.NewDecoder(s.file)
	var seen int64
	for {
		var item plan.Item
		err := decoder.Decode(&item)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read copy plan spool: %w", err)
		}
		seen++
		if err := fn(item); err != nil {
			return err
		}
	}
	if seen != s.count {
		return fmt.Errorf("copy plan spool is incomplete: read %d of %d items", seen, s.count)
	}
	return nil
}

func (s *copyPlanSpool) close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.file == nil {
		return nil
	}
	return errors.Join(s.file.Close(), os.Remove(s.file.Name()))
}

package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/csmart/aeronautes/internal/applog"
)

type shellShutdown struct {
	drain         func(context.Context) error
	closeDatabase func() error
	releaseLock   func()
	drainOnce     sync.Once
	closeOnce     sync.Once
	closeErr      error
}

func (s *shellShutdown) wait() {
	s.drainOnce.Do(func() {
		if s.drain == nil {
			return
		}
		if err := s.drain(context.Background()); err != nil {
			// The background waiter has no deadline. A future error class
			// must not turn an unproved drain into permission to quit.
			applog.Errorf("owned work did not drain: %v", err)
			select {}
		}
	})
}

func (s *shellShutdown) close() error {
	s.wait()
	s.closeOnce.Do(func() {
		if s.closeDatabase != nil {
			if err := s.closeDatabase(); err != nil {
				// A retry may return nil just because the handle already
				// rejected further calls. It cannot certify this failure.
				s.closeErr = fmt.Errorf("close state database: %w", err)
				return
			}
		}
		if s.releaseLock != nil {
			s.releaseLock()
		}
	})
	return s.closeErr
}

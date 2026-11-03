package app

import "errors"

// ErrInstanceAlreadyRunning is wrapped by AcquireInstanceLock when the
// state-directory lock is already held by another live process, as opposed
// to a lock that could not be taken for some other reason — a missing
// directory, or a filesystem that does not support the lock. The shell tells
// these apart so it can show an operator who launched a second copy that one
// is already running, rather than failing with a bare error nobody sees.
var ErrInstanceAlreadyRunning = errors.New("another aeronautes instance is already running")

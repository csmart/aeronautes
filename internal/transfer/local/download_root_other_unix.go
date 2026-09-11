//go:build unix && !linux

package local

import "os"

// Only the device comparison distinguishes mounts here.
func refuseMountRoot(*os.Root, string) error { return nil }

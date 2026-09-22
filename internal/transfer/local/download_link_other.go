//go:build !linux

package local

import (
	"errors"
	"os"
)

// The engine route is refused before placement wherever the child cannot
// be anchored to the held stage, which is every platform but Linux, so
// descriptor publication has no caller here.
func linkDescriptorAt(*os.File, *os.Root, string, string, string) error {
	return errors.New("descriptor publication is only implemented on Linux")
}

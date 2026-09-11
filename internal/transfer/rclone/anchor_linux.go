//go:build linux

package rclone

// AnchorSupported reports that CopyInto can hand the child a destination
// resolved through an inherited directory descriptor.
const AnchorSupported = true

// anchoredDestination is the first inherited descriptor as the kernel
// exposes it to the child; every path beneath it resolves through the
// held directory at each open.
const anchoredDestination = "/proc/self/fd/3"

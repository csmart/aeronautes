//go:build !linux

package rclone

// Only Linux exposes an inherited directory descriptor as a path the
// engine resolves through at every open.
const AnchorSupported = false

const anchoredDestination = ""

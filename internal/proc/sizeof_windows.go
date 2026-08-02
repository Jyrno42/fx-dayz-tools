//go:build windows

package proc

import "unsafe"

// unsafeSizeof is a tiny helper so kill_windows.go does not import unsafe
// directly for a single struct-size query.
func unsafeSizeof[T any](v T) uintptr { return unsafe.Sizeof(v) }

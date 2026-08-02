//go:build !windows

package netwait

// portBound falls back to a bind probe on platforms without the Windows UDP
// table. Good enough for tests, and the DayZ server only runs on Windows.
func portBound(port int) bool { return !udpPortFree(port) }

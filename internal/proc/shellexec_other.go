//go:build !windows

package proc

import "context"

// runViaShellExecute means nothing off Windows. The one tool that needs it is
// Windows-only, so fall back to an ordinary run.
func runViaShellExecute(ctx context.Context, c Cmd) (Result, error) {
	return (&Exec{}).runDirect(ctx, c)
}

//go:build !windows

package proc

import "context"

// runViaConsole means nothing off Windows. The one tool that needs it is
// Windows-only, so fall back to an ordinary run.
func runViaConsole(ctx context.Context, c Cmd) (Result, error) {
	return (&Exec{}).runDirect(ctx, c)
}

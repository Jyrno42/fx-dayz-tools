package proc

import (
	"context"
	"time"
)

// WaitGone blocks until none of the pids is running or the timeout passes. It
// returns the pids still alive, so an empty result means they all exited.
func WaitGone(ctx context.Context, pids []int, timeout time.Duration) []int {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()

	for {
		var alive []int
		for _, pid := range pids {
			if Alive(pid) {
				alive = append(alive, pid)
			}
		}
		if len(alive) == 0 || time.Now().After(deadline) {
			return alive
		}
		select {
		case <-ctx.Done():
			return alive
		case <-tick.C:
		}
	}
}

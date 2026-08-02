// Package netwait waits for the DayZ server to start listening.
//
// It replaces two things. First, a fixed `Start-Sleep 25`, which came out either
// too slow or too short depending on the machine. Second, a `netstat -ano` poll,
// which meant parsing localised console output.
package netwait

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"
)

// DefaultInterval is how often to re-check.
const DefaultInterval = 500 * time.Millisecond

// Options tunes a wait.
type Options struct {
	Timeout  time.Duration
	Interval time.Duration
	// OnTick is called before each attempt with the elapsed time, for progress.
	OnTick func(elapsed time.Duration)
	// Abort gets checked before each attempt. Returning true stops the wait with
	// the given reason. That is how a server that died, or was dismissed from an
	// error dialog, saves the caller from sitting out the full timeout.
	Abort func() (bool, string)
}

func (o Options) interval() time.Duration {
	if o.Interval > 0 {
		return o.Interval
	}
	return DefaultInterval
}

// ForUDPPort blocks until port is bound by another process, or the timeout
// expires.
//
// Detection works by trying to bind the port ourselves. If the bind fails, some
// other process holds it. That is a deliberate inversion of the obvious "can I
// connect" test, which does not work for UDP, since an unbound UDP port accepts
// datagrams silently and reports nothing.
//
// It follows that this cannot tell the DayZ server apart from anything else
// occupying the port, exactly as the old netstat poll could not.
func ForUDPPort(ctx context.Context, port int, opts Options) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("netwait: port %d is out of range", port)
	}

	deadline := time.Now().Add(opts.Timeout)
	if opts.Timeout <= 0 {
		deadline = time.Now().Add(180 * time.Second)
	}
	start := time.Now()

	ticker := time.NewTicker(opts.interval())
	defer ticker.Stop()

	for {
		if portBound(port) {
			return nil
		}
		// Checked after the port, so a server that binds and exits in the same
		// instant still counts as having come up at all.
		if opts.Abort != nil {
			if stop, reason := opts.Abort(); stop {
				return &AbortedError{Reason: reason, Elapsed: time.Since(start)}
			}
		}
		if opts.OnTick != nil {
			opts.OnTick(time.Since(start))
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("netwait: nothing bound UDP port %d within %s; the server may have failed during config validation -- check the server profile logs",
				port, time.Since(start).Round(time.Second))
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// udpPortFree reports whether the port can still be bound, i.e. nothing is
// listening yet.
func udpPortFree(port int) bool {
	conn, err := net.ListenPacket("udp", net.JoinHostPort("", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// IsBound reports whether something currently holds the UDP port.
func IsBound(port int) bool { return portBound(port) }

// AbortedError means Abort cut the wait short instead of it timing out. Callers
// check for it because it usually means the thing being waited for has already
// failed, and there is something more useful to report than a timeout.
type AbortedError struct {
	Reason  string
	Elapsed time.Duration
}

func (e *AbortedError) Error() string {
	return fmt.Sprintf("%s after %s", e.Reason, e.Elapsed.Round(time.Millisecond))
}

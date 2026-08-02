package netwait

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// listen grabs a free UDP port and returns it with a closer.
func listen(t *testing.T) (int, func()) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portStr, err := net.SplitHostPort(conn.LocalAddr().String())
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	return port, func() { conn.Close() }
}

func TestForUDPPortReturnsWhenBound(t *testing.T) {
	port, closer := listen(t)
	defer closer()

	err := ForUDPPort(t.Context(), port, Options{Timeout: 2 * time.Second, Interval: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("an already-bound port should return immediately, got %v", err)
	}
}

// The timeout message has to point somewhere useful. A server that dies during
// config validation never binds, and that is the common failure.
func TestForUDPPortTimesOut(t *testing.T) {
	port, closer := listen(t)
	closer() // free it again

	start := time.Now()
	err := ForUDPPort(t.Context(), port, Options{Timeout: 150 * time.Millisecond, Interval: 10 * time.Millisecond})
	if err == nil {
		t.Fatal("expected a timeout for a port nothing is bound to")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("timeout took %s, far longer than asked", elapsed)
	}
}

func TestForUDPPortDetectsALateBind(t *testing.T) {
	port, closer := listen(t)
	closer()

	go func() {
		time.Sleep(80 * time.Millisecond)
		conn, err := net.ListenPacket("udp", net.JoinHostPort("", strconv.Itoa(port)))
		if err != nil {
			return
		}
		time.Sleep(3 * time.Second)
		conn.Close()
	}()

	if err := ForUDPPort(t.Context(), port, Options{Timeout: 5 * time.Second, Interval: 10 * time.Millisecond}); err != nil {
		t.Fatalf("should have detected the late bind, got %v", err)
	}
}

func TestForUDPPortHonoursContext(t *testing.T) {
	port, closer := listen(t)
	closer()

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := ForUDPPort(ctx, port, Options{Timeout: 10 * time.Second, Interval: 10 * time.Millisecond})
	if err == nil {
		t.Fatal("expected cancellation to stop the wait")
	}
}

func TestForUDPPortRejectsBadPort(t *testing.T) {
	for _, p := range []int{0, -1, 70000} {
		if err := ForUDPPort(t.Context(), p, Options{Timeout: time.Millisecond}); err == nil {
			t.Errorf("port %d should be rejected", p)
		}
	}
}

// A server that dies before binding should stop the wait immediately, instead
// of leaving the caller to sit out the full timeout for a port that is never
// going to appear.
func TestForUDPPortAborts(t *testing.T) {
	port, closer := listen(t)
	closer()

	calls := 0
	start := time.Now()
	err := ForUDPPort(t.Context(), port, Options{
		Timeout:  10 * time.Second,
		Interval: 10 * time.Millisecond,
		Abort: func() (bool, string) {
			calls++
			return calls > 2, "the server process exited"
		},
	})

	if err == nil {
		t.Fatal("expected the wait to abort")
	}
	var aborted *AbortedError
	if !errors.As(err, &aborted) {
		t.Fatalf("expected an AbortedError so callers can tell it from a timeout, got %T: %v", err, err)
	}
	if !strings.Contains(aborted.Reason, "exited") {
		t.Errorf("reason = %q", aborted.Reason)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("aborting took %s; it should be immediate", elapsed)
	}
}

// Abort should not fire when the port is already bound. A server that comes up
// and gets stopped in the same instant still came up.
func TestAbortDoesNotBeatASuccessfulBind(t *testing.T) {
	port, closer := listen(t)
	defer closer()

	err := ForUDPPort(t.Context(), port, Options{
		Timeout:  2 * time.Second,
		Interval: 10 * time.Millisecond,
		Abort:    func() (bool, string) { return true, "should not be consulted" },
	})
	if err != nil {
		t.Fatalf("a bound port should win over Abort, got %v", err)
	}
}

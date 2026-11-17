package ports

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/JorMath/mc-tui-server/internal/config"
)

// listenerPort returns the port a test listener is bound to.
func listenerPort(t *testing.T, ln net.Listener) int {
	t.Helper()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %T is not TCP", ln.Addr())
	}
	return addr.Port
}

// freePort returns a port that was just released and is expected to stay
// free for the rest of the test.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listenerPort(t, ln)
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return port
}

// testInstance builds an instance backed by a temp dir. An empty props value
// leaves server.properties missing.
func testInstance(t *testing.T, name, props string) config.Instance {
	t.Helper()
	dir := t.TempDir()
	if props != "" {
		if err := os.WriteFile(filepath.Join(dir, "server.properties"), []byte(props), 0o644); err != nil {
			t.Fatalf("writing properties: %v", err)
		}
	}
	return config.Instance{Name: name, Dir: dir}
}

func TestInUseWhileBoundAndAfterClose(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listenerPort(t, ln)
	if !InUse(port) {
		t.Fatalf("InUse(%d) = false, want true while a listener is bound", port)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if InUse(port) {
		t.Fatalf("InUse(%d) = true, want false after the listener closed", port)
	}
}

func TestInUseRejectsOutOfRange(t *testing.T) {
	for _, port := range []int{-1, 0, 65536} {
		if !InUse(port) {
			t.Fatalf("InUse(%d) = false, want true for an out-of-range port", port)
		}
	}
}

func TestNextFreeReturnsABindablePort(t *testing.T) {
	base := freePort(t)
	got, err := NextFree(base)
	if err != nil {
		t.Fatalf("NextFree(%d): %v", base, err)
	}
	if got < base || got > base+scanLimit {
		t.Fatalf("NextFree(%d) = %d, outside %d-%d", base, got, base, base+scanLimit)
	}
	if InUse(got) {
		t.Fatalf("NextFree(%d) returned %d, which is in use", base, got)
	}
}

func TestNextFreeRejectsOutOfRange(t *testing.T) {
	for _, from := range []int{-5, 0, 65536} {
		if _, err := NextFree(from); err == nil {
			t.Fatalf("NextFree(%d) = nil error, want out-of-range error", from)
		}
	}
}

func TestNextFreeSkipsBusyCandidates(t *testing.T) {
	busy := map[int]bool{7000: true, 7001: true, 7002: true}
	got, err := nextFree(7000, func(port int) bool { return busy[port] })
	if err != nil {
		t.Fatalf("nextFree: %v", err)
	}
	if got != 7003 {
		t.Fatalf("nextFree = %d, want 7003", got)
	}
}

func TestNextFreeUsesTheLastCandidate(t *testing.T) {
	first := 20000
	got, err := nextFree(first, func(port int) bool { return port != first+scanLimit })
	if err != nil {
		t.Fatalf("nextFree: %v", err)
	}
	if got != first+scanLimit {
		t.Fatalf("nextFree = %d, want %d", got, first+scanLimit)
	}
}

func TestNextFreeExhausted(t *testing.T) {
	got, err := nextFree(20000, func(int) bool { return true })
	if err == nil {
		t.Fatal("nextFree = nil error, want exhaustion error")
	}
	if got != 0 {
		t.Fatalf("nextFree = %d, want 0 on exhaustion", got)
	}
	if !strings.Contains(err.Error(), "20000-20100") {
		t.Fatalf("error %q does not name the scanned range", err)
	}
}

func TestNextFreeStopsAtTheTopPort(t *testing.T) {
	got, err := nextFree(maxPort, func(int) bool { return false })
	if err != nil {
		t.Fatalf("nextFree: %v", err)
	}
	if got != maxPort {
		t.Fatalf("nextFree(%d) = %d", maxPort, got)
	}
}

func TestReserved(t *testing.T) {
	alpha := testInstance(t, "alpha", "server-port= 25565 \n")
	beta := testInstance(t, "beta", "server-port=25566\n")
	noFile := testInstance(t, "gamma", "")
	invalid := testInstance(t, "delta", "server-port=lots\n")
	unreadable := testInstance(t, "epsilon", "")
	if err := os.Mkdir(filepath.Join(unreadable.Dir, propertiesFile), 0o755); err != nil {
		t.Fatalf("creating directory: %v", err)
	}
	instances := []config.Instance{alpha, beta, noFile, invalid, unreadable}

	if name, ok := Reserved(instances, 25565); !ok || name != "alpha" {
		t.Fatalf("Reserved(25565) = %q, %v, want alpha (values are compared numerically and trimmed)", name, ok)
	}
	if name, ok := Reserved(instances, 25566); !ok || name != "beta" {
		t.Fatalf("Reserved(25566) = %q, %v, want beta", name, ok)
	}
	for _, port := range []int{25567, 0} {
		if name, ok := Reserved(instances, port); ok {
			t.Fatalf("Reserved(%d) = %q, true, want no reservation", port, name)
		}
	}
}

func TestReservedFirstMatchWins(t *testing.T) {
	first := testInstance(t, "first", "server-port=25565\n")
	second := testInstance(t, "second", "server-port=25565\n")
	name, ok := Reserved([]config.Instance{first, second}, 25565)
	if !ok || name != "first" {
		t.Fatalf("Reserved = %q, %v, want first", name, ok)
	}
}

func TestValidateRejectsInvalidPorts(t *testing.T) {
	for _, port := range []int{-1, 0, 65536} {
		if err := Validate(nil, "", port); err == nil {
			t.Fatalf("Validate(%d) = nil, want an out-of-range error", port)
		}
	}
}

func TestValidateRejectsPortReservedByAnotherInstance(t *testing.T) {
	port := freePort(t)
	taken := testInstance(t, "alpha", "server-port="+strconv.Itoa(port)+"\n")
	instances := []config.Instance{taken}

	err := Validate(instances, "", port)
	if err == nil || !strings.Contains(err.Error(), `"alpha"`) {
		t.Fatalf("Validate = %v, want an error naming alpha", err)
	}
	if err := Validate(instances, "beta", port); err == nil {
		t.Fatal("Validate for another instance = nil, want a reservation error")
	}
	if err := Validate(instances, "alpha", port); err != nil {
		t.Fatalf("Validate for the reserving instance = %v, want nil", err)
	}
}

func TestValidateRejectsBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := listenerPort(t, ln)

	err = Validate(nil, "", port)
	if err == nil {
		t.Fatalf("Validate(%d) = nil, want an in-use error", port)
	}
	if !strings.Contains(err.Error(), "in use") {
		t.Fatalf("error %q does not mention the port being in use", err)
	}
	// self is not registered: its own declaration cannot be checked.
	if err := Validate(nil, "ghost", port); err == nil {
		t.Fatal("Validate with an unknown self = nil, want an in-use error")
	}
}

func TestValidateAllowsOwnBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := listenerPort(t, ln)

	self := testInstance(t, "chone", "server-port="+strconv.Itoa(port)+"\n")
	if err := Validate([]config.Instance{self}, "chone", port); err != nil {
		t.Fatalf("Validate for the running instance's own port = %v, want nil", err)
	}
}

func TestValidateAcceptsFreePort(t *testing.T) {
	port := freePort(t)
	if err := Validate(nil, "", port); err != nil {
		t.Fatalf("Validate(%d) = %v, want nil", port, err)
	}
}

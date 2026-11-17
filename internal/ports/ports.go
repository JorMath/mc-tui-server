// Package ports finds and validates the TCP ports Minecraft instances bind.
package ports

import (
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/properties"
)

const (
	minPort = 1
	maxPort = 65535
	// scanLimit is how many ports NextFree probes after its starting point.
	scanLimit = 100
	// propertiesFile is the file Minecraft reads its settings from.
	propertiesFile = "server.properties"
	// serverPortKey is the property that holds the bound TCP port.
	serverPortKey = "server-port"
)

// InUse reports whether the port cannot be bound on this machine right now.
// Out-of-range values are unusable, so they count as in use.
func InUse(port int) bool {
	if port < minPort || port > maxPort {
		return true
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

// NextFree returns the first port that can be bound, scanning from `from`
// through from+100 inclusive. It returns an error naming the range when
// every candidate is busy.
func NextFree(from int) (int, error) {
	return nextFree(from, InUse)
}

// nextFree is the testable core of NextFree: inUse decides whether a
// candidate is taken, so callers can exhaust the scan without binding.
func nextFree(from int, inUse func(int) bool) (int, error) {
	if from < minPort || from > maxPort {
		return 0, fmt.Errorf("start port %d is outside the valid range %d-%d", from, minPort, maxPort)
	}
	for port := from; port <= from+scanLimit && port <= maxPort; port++ {
		if !inUse(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free TCP port in the range %d-%d", from, from+scanLimit)
}

// Reserved returns the name of the instance whose server.properties declares
// server-port == port. found is false when no instance reserves it; when
// several do, the first one in the list wins.
func Reserved(instances []config.Instance, port int) (string, bool) {
	for _, inst := range instances {
		if declared, ok := declaredPort(inst); ok && declared == port {
			return inst.Name, true
		}
	}
	return "", false
}

// Validate checks that port is a valid TCP port and that it is either free or
// already assigned to self. It returns a human-readable error otherwise.
func Validate(instances []config.Instance, self string, port int) error {
	if port < minPort || port > maxPort {
		return fmt.Errorf("port %d is outside the valid range %d-%d", port, minPort, maxPort)
	}
	if name, found := Reserved(instances, port); found && name != self {
		return fmt.Errorf("port %d is assigned to instance %q", port, name)
	}
	if !InUse(port) {
		return nil
	}
	// A running server binds its own port, so saving the value it already
	// declares must not be rejected.
	if inst, ok := instanceByName(instances, self); ok {
		if own, ok := declaredPort(inst); ok && own == port {
			return nil
		}
	}
	return fmt.Errorf("port %d is already in use on this machine", port)
}

// declaredPort reads the port an instance declares in its server.properties.
// A missing, unreadable or invalid value reports no declaration.
func declaredPort(inst config.Instance) (int, bool) {
	props, err := properties.Load(filepath.Join(inst.Dir, propertiesFile))
	if err != nil {
		return 0, false
	}
	raw, ok := props.Get(serverPortKey)
	if !ok {
		return 0, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, false
	}
	return port, true
}

// instanceByName returns the instance registered under name.
func instanceByName(instances []config.Instance, name string) (config.Instance, bool) {
	for _, inst := range instances {
		if inst.Name == name {
			return inst, true
		}
	}
	return config.Instance{}, false
}

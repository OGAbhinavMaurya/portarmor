package procnet

import (
	"os"
	"strconv"
	"strings"
)

// Default ephemeral port range in Linux (28,231 ports)
const (
	DefaultPortMin = 32768
	DefaultPortMax = 60999
)

// PortRange represents the local ephemeral port boundaries.
type PortRange struct {
	Min      int `json:"min"`
	Max      int `json:"max"`
	Capacity int `json:"capacity"`
}

// ReadPortRange reads /proc/sys/net/ipv4/ip_local_port_range or returns defaults.
func ReadPortRange(path string) (*PortRange, error) {
	if path == "" {
		path = "/proc/sys/net/ipv4/ip_local_port_range"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		// Fallback to standard Linux defaults
		return &PortRange{
			Min:      DefaultPortMin,
			Max:      DefaultPortMax,
			Capacity: DefaultPortMax - DefaultPortMin + 1,
		}, nil
	}

	fields := strings.Fields(string(data))
	if len(fields) >= 2 {
		minPort, err1 := strconv.Atoi(fields[0])
		maxPort, err2 := strconv.Atoi(fields[1])
		if err1 == nil && err2 == nil && maxPort >= minPort {
			return &PortRange{
				Min:      minPort,
				Max:      maxPort,
				Capacity: maxPort - minPort + 1,
			}, nil
		}
	}

	return &PortRange{
		Min:      DefaultPortMin,
		Max:      DefaultPortMax,
		Capacity: DefaultPortMax - DefaultPortMin + 1,
	}, nil
}

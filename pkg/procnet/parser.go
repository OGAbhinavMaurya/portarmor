package procnet

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

// TCP States as represented in /proc/net/tcp
const (
	StateEstablished = "01"
	StateSynSent     = "02"
	StateSynRecv     = "03"
	StateFinWait1    = "04"
	StateFinWait2    = "05"
	StateTimeWait    = "06"
	StateClose       = "07"
	StateCloseWait   = "08"
	StateLastAck     = "09"
	StateListen      = "0A"
	StateClosing     = "0B"
)

// TCPSocket represents a single socket entry from /proc/net/tcp.
type TCPSocket struct {
	LocalIP    string `json:"local_ip"`
	LocalPort  int    `json:"local_port"`
	RemoteIP   string `json:"remote_ip"`
	RemotePort int    `json:"remote_port"`
	State      string `json:"state"`
	StateName  string `json:"state_name"`
	Inode      uint64 `json:"inode"`
}

// StateToString converts a /proc/net/tcp hex state code to its standard TCP name.
func StateToString(stateHex string) string {
	switch strings.ToUpper(stateHex) {
	case StateEstablished:
		return "ESTABLISHED"
	case StateSynSent:
		return "SYN_SENT"
	case StateSynRecv:
		return "SYN_RECV"
	case StateFinWait1:
		return "FIN_WAIT1"
	case StateFinWait2:
		return "FIN_WAIT2"
	case StateTimeWait:
		return "TIME_WAIT"
	case StateClose:
		return "CLOSE"
	case StateCloseWait:
		return "CLOSE_WAIT"
	case StateLastAck:
		return "LAST_ACK"
	case StateListen:
		return "LISTEN"
	case StateClosing:
		return "CLOSING"
	default:
		return "UNKNOWN"
	}
}

// ParseHexIPv4 converts an 8-character little-endian hex string to an IPv4 string.
func ParseHexIPv4(hexStr string) (string, error) {
	b, err := hex.DecodeString(hexStr)
	if err != nil || len(b) != 4 {
		return "", fmt.Errorf("invalid hex IPv4: %s", hexStr)
	}
	// /proc/net/tcp stores IPv4 addresses in little-endian order
	ip := net.IPv4(b[3], b[2], b[1], b[0])
	return ip.String(), nil
}

// ParseTCPFile reads and parses a /proc/net/tcp file path.
func ParseTCPFile(path string) ([]TCPSocket, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return ParseTCPTable(f)
}

// ParseTCPTable parses lines from a /proc/net/tcp reader.
func ParseTCPTable(r io.Reader) ([]TCPSocket, error) {
	var sockets []TCPSocket
	scanner := bufio.NewScanner(r)

	// First line is table header: "sl local_address rem_address st tx_queue rx_queue..."
	if scanner.Scan() {
		// skip header
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}

		localAddr := fields[1]
		remoteAddr := fields[2]
		state := strings.ToUpper(fields[3])
		inodeStr := fields[9]

		localParts := strings.Split(localAddr, ":")
		remoteParts := strings.Split(remoteAddr, ":")
		if len(localParts) != 2 || len(remoteParts) != 2 {
			continue
		}

		localIP, err1 := ParseHexIPv4(localParts[0])
		localPort64, err2 := strconv.ParseUint(localParts[1], 16, 16)
		remoteIP, err3 := ParseHexIPv4(remoteParts[0])
		remotePort64, err4 := strconv.ParseUint(remoteParts[1], 16, 16)
		inode, _ := strconv.ParseUint(inodeStr, 10, 64)

		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}

		sockets = append(sockets, TCPSocket{
			LocalIP:    localIP,
			LocalPort:  int(localPort64),
			RemoteIP:   remoteIP,
			RemotePort: int(remotePort64),
			State:      state,
			StateName:  StateToString(state),
			Inode:      inode,
		})
	}

	return sockets, scanner.Err()
}

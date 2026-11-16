package procnet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleTCPTable = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1
   1: 3201A8C0:AC46 6401A8C0:01BB 01 00000000:00000000 02:00001000 00000000     0        0 67890 2
   2: 3201A8C0:AC47 6401A8C0:01BB 06 00000000:00000000 00:00000000 00000000     0        0 67891 1
   3: 3201A8C0:AC48 6401A8C0:01BB 06 00000000:00000000 00:00000000 00000000     0        0 67892 1
`

func TestParseTCPTable(t *testing.T) {
	sockets, err := ParseTCPTable(strings.NewReader(sampleTCPTable))
	if err != nil {
		t.Fatalf("ParseTCPTable failed: %v", err)
	}

	if len(sockets) != 4 {
		t.Fatalf("expected 4 sockets, got %d", len(sockets))
	}

	// Socket 0: Listen on 127.0.0.1:8080 (0x1F90 = 8080)
	s0 := sockets[0]
	if s0.LocalIP != "127.0.0.1" || s0.LocalPort != 8080 || s0.State != StateListen {
		t.Errorf("unexpected s0: %+v", s0)
	}

	// Socket 1: Established to 192.168.1.100:443 (0x01BB = 443)
	s1 := sockets[1]
	if s1.RemoteIP != "192.168.1.100" || s1.RemotePort != 443 || s1.State != StateEstablished {
		t.Errorf("unexpected s1: %+v", s1)
	}

	// Socket 2: TIME_WAIT
	s2 := sockets[2]
	if s2.State != StateTimeWait || s2.StateName != "TIME_WAIT" {
		t.Errorf("unexpected s2: %+v", s2)
	}
}

func TestReadPortRange(t *testing.T) {
	tempDir := t.TempDir()
	rangeFile := filepath.Join(tempDir, "ip_local_port_range")
	_ = os.WriteFile(rangeFile, []byte("32768\t60999\n"), 0644)

	pr, err := ReadPortRange(rangeFile)
	if err != nil {
		t.Fatalf("ReadPortRange failed: %v", err)
	}
	if pr.Min != 32768 || pr.Max != 60999 || pr.Capacity != 28232 {
		t.Errorf("unexpected port range: %+v", pr)
	}

	// Test fallback for non-existent file
	prDefault, err := ReadPortRange(filepath.Join(tempDir, "nonexistent"))
	if err != nil || prDefault.Capacity != 28232 {
		t.Errorf("unexpected fallback: %+v", prDefault)
	}
}

func TestParseHexIPv4(t *testing.T) {
	ip, err := ParseHexIPv4("0100007F")
	if err != nil || ip != "127.0.0.1" {
		t.Errorf("expected 127.0.0.1, got %s, err: %v", ip, err)
	}
}

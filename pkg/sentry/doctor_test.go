package sentry

import (
	"os"
	"path/filepath"
	"testing"
)

const mockTCPContent = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1
   1: 3201A8C0:AC46 6401A8C0:01BB 06 00000000:00000000 00:00000000 00000000     0        0 67890 1
   2: 3201A8C0:AC47 6401A8C0:01BB 06 00000000:00000000 00:00000000 00000000     0        0 67891 1
   3: 3201A8C0:AC48 0A01000A:0050 01 00000000:00000000 00:00000000 00000000     0        0 67892 1
`

func setupMockEnvironment(t *testing.T) (string, string) {
	tempDir := t.TempDir()

	tcpFile := filepath.Join(tempDir, "tcp")
	if err := os.WriteFile(tcpFile, []byte(mockTCPContent), 0644); err != nil {
		t.Fatal(err)
	}

	rangeFile := filepath.Join(tempDir, "range")
	// 100 ports capacity for easy percentage calculation
	if err := os.WriteFile(rangeFile, []byte("50000\t50099\n"), 0644); err != nil {
		t.Fatal(err)
	}

	return tcpFile, rangeFile
}

func TestDoctor_Inspect(t *testing.T) {
	tcpFile, rangeFile := setupMockEnvironment(t)

	doc := New()
	rep, err := doc.Inspect(DoctorConfig{
		TCPPath:       tcpFile,
		PortRangePath: rangeFile,
		ThresholdPct:  50.0,
	})
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}

	if rep.PortRange.Capacity != 100 {
		t.Errorf("expected 100 capacity, got %d", rep.PortRange.Capacity)
	}
	if len(rep.TopSinks) != 2 {
		t.Fatalf("expected 2 sinks, got %d", len(rep.TopSinks))
	}

	// Sink 1: 192.168.1.100:443 (2 TIME_WAIT sockets -> 2%)
	s0 := rep.TopSinks[0]
	if s0.Destination != "192.168.1.100:443" {
		t.Errorf("expected top sink 192.168.1.100:443, got %s", s0.Destination)
	}
	if s0.TimeWaitCount != 2 || s0.TotalSockets != 2 {
		t.Errorf("unexpected counts: %+v", s0)
	}
	if s0.SaturationPct != 2.0 {
		t.Errorf("expected 2.0%% saturation, got %f", s0.SaturationPct)
	}

	if rep.Status != "HEALTHY" {
		t.Errorf("expected HEALTHY status for 2%% saturation, got %s", rep.Status)
	}
}

func TestDoctor_CriticalThreshold(t *testing.T) {
	tempDir := t.TempDir()

	tcpFile := filepath.Join(tempDir, "tcp")
	_ = os.WriteFile(tcpFile, []byte(mockTCPContent), 0644)

	rangeFile := filepath.Join(tempDir, "range")
	// Set capacity to 2 ports so 2 sockets = 100% saturation!
	_ = os.WriteFile(rangeFile, []byte("50000\t50001\n"), 0644)

	doc := New()
	rep, err := doc.Inspect(DoctorConfig{
		TCPPath:       tcpFile,
		PortRangePath: rangeFile,
		ThresholdPct:  70.0,
	})
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}

	if rep.Status != "CRITICAL" {
		t.Errorf("expected CRITICAL status, got %s", rep.Status)
	}
}

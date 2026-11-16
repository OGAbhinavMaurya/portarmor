package sentry

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/OGAbhinavMaurya/portarmor/pkg/procnet"
)

// EndpointSink groups active sockets by their destination IP and port.
type EndpointSink struct {
	Destination       string  `json:"destination"`
	RemoteIP          string  `json:"remote_ip"`
	RemotePort        int     `json:"remote_port"`
	TimeWaitCount     int     `json:"time_wait_count"`
	EstablishedCount  int     `json:"established_count"`
	TotalSockets      int     `json:"total_sockets"`
	SaturationPct     float64 `json:"saturation_pct"`
	FillVelocity      float64 `json:"fill_velocity_ports_per_sec"`
	TimeToExhaustion  float64 `json:"time_to_exhaustion_seconds"` // -1 if not exhausting
}

// DoctorConfig configures the sentry inspection.
type DoctorConfig struct {
	ProcRoot      string
	TCPPath       string
	PortRangePath string
	ThresholdPct  float64
	SampleWindow  time.Duration
}

// DoctorReport captures port saturation, velocity, and exhaustion risk.
type DoctorReport struct {
	PortRange           procnet.PortRange `json:"port_range"`
	TotalSockets        int               `json:"total_sockets"`
	TotalTimeWait       int               `json:"total_time_wait"`
	TotalEstablished    int               `json:"total_established"`
	OverallSaturationPct float64          `json:"overall_saturation_pct"`
	Status              string            `json:"status"` // HEALTHY, WARNING, CRITICAL
	Summary             string            `json:"summary"`
	Recommendation      string            `json:"recommendation"`
	TopSinks            []EndpointSink    `json:"top_sinks"`
}

// Doctor diagnoses ephemeral port exhaustion and TIME_WAIT saturation.
type Doctor struct{}

// New creates a new Doctor instance.
func New() *Doctor {
	return &Doctor{}
}

// Inspect analyzes active sockets and computes saturation per destination endpoint.
func (d *Doctor) Inspect(cfg DoctorConfig) (*DoctorReport, error) {
	if cfg.ProcRoot == "" {
		cfg.ProcRoot = "/proc"
	}
	if cfg.TCPPath == "" {
		cfg.TCPPath = filepath.Join(cfg.ProcRoot, "net/tcp")
	}
	if cfg.PortRangePath == "" {
		cfg.PortRangePath = filepath.Join(cfg.ProcRoot, "sys/net/ipv4/ip_local_port_range")
	}
	if cfg.ThresholdPct <= 0 {
		cfg.ThresholdPct = 70.0
	}

	pr, err := procnet.ReadPortRange(cfg.PortRangePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read port range: %w", err)
	}

	sockets, err := procnet.ParseTCPFile(cfg.TCPPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse TCP table: %w", err)
	}

	// Group sockets by destination endpoint
	sinkMap := make(map[string]*EndpointSink)
	var totalTW, totalEst int

	for _, s := range sockets {
		// Ignore listen sockets or zero destination
		if s.RemotePort == 0 || s.RemoteIP == "0.0.0.0" {
			continue
		}

		key := fmt.Sprintf("%s:%d", s.RemoteIP, s.RemotePort)
		sink, exists := sinkMap[key]
		if !exists {
			sink = &EndpointSink{
				Destination:      key,
				RemoteIP:         s.RemoteIP,
				RemotePort:       s.RemotePort,
				TimeToExhaustion: -1,
			}
			sinkMap[key] = sink
		}

		sink.TotalSockets++
		if s.State == procnet.StateTimeWait {
			sink.TimeWaitCount++
			totalTW++
		} else if s.State == procnet.StateEstablished {
			sink.EstablishedCount++
			totalEst++
		}
	}

	var sinks []EndpointSink
	for _, s := range sinkMap {
		s.SaturationPct = (float64(s.TotalSockets) / float64(pr.Capacity)) * 100.0
		sinks = append(sinks, *s)
	}

	// Sort sinks by total sockets descending
	sort.Slice(sinks, func(i, j int) bool {
		return sinks[i].TotalSockets > sinks[j].TotalSockets
	})

	overallSat := (float64(len(sockets)) / float64(pr.Capacity)) * 100.0

	// Determine status
	status := "HEALTHY"
	summary := fmt.Sprintf("Ephemeral port usage is healthy. %d total sockets across %d capacity (%.1f%% saturation).",
		len(sockets), pr.Capacity, overallSat)
	recommendation := "System is operating within safe outbound limits."

	var maxSinkSat float64
	var worstSink string
	if len(sinks) > 0 {
		maxSinkSat = sinks[0].SaturationPct
		worstSink = sinks[0].Destination
	}

	if maxSinkSat >= 80.0 || overallSat >= 80.0 {
		status = "CRITICAL"
		summary = fmt.Sprintf("Critical ephemeral port exhaustion risk. Destination %s consumes %.1f%% of local port capacity (%d sockets). EADDRNOTAVAIL (errno 99) failures imminent.",
			worstSink, maxSinkSat, sinks[0].TotalSockets)
		recommendation = fmt.Sprintf("Deploy 'portarmor shield' to pool connections to %s and eliminate TIME_WAIT accumulation.", worstSink)
	} else if maxSinkSat >= cfg.ThresholdPct || overallSat >= cfg.ThresholdPct {
		status = "WARNING"
		summary = fmt.Sprintf("High ephemeral port pressure on %s (%.1f%% saturation). Burst traffic may cause port burnout.",
			worstSink, maxSinkSat)
		recommendation = "Enable HTTP keep-alive connection pooling in outbound clients or wrap with portarmor shield."
	}

	return &DoctorReport{
		PortRange:            *pr,
		TotalSockets:         len(sockets),
		TotalTimeWait:        totalTW,
		TotalEstablished:     totalEst,
		OverallSaturationPct: overallSat,
		Status:               status,
		Summary:              summary,
		Recommendation:       recommendation,
		TopSinks:             sinks,
	}, nil
}

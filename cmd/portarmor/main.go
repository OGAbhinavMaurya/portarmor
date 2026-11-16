package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/OGAbhinavMaurya/portarmor/pkg/sentry"
	"github.com/OGAbhinavMaurya/portarmor/pkg/shield"
)

const version = "1.0.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	var err error
	switch command {
	case "doctor":
		err = runDoctor(args)
	case "shield":
		err = runShield(args)
	case "version", "-v", "--version":
		fmt.Printf("portarmor version %s\n", version)
		return
	case "help", "-h", "--help":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	usage := `portarmor - Linux ephemeral port exhaustion sentry and outbound connection pool shield

Usage:
  portarmor <command> [options]

Commands:
  doctor     Inspect TCP socket table, compute endpoint saturation, and diagnose port exhaustion
  shield     Run outbound connection pool proxy to multiplex traffic and eliminate TIME_WAIT accumulation
  version    Show portarmor version
  help       Show this help message

Doctor Options:
  --proc-root      Path to proc filesystem [default: /proc]
  --threshold      Warning threshold for endpoint saturation percentage [default: 70.0]
  --json           Output results in JSON format

Shield Options:
  --listen         Local bind address [default: 127.0.0.1:8080]
  --target         Upstream destination URL [required, e.g. https://api.stripe.com]
  --pool-size      Maximum idle keep-alive connections per host [default: 100]
  --json           Output operational metrics in JSON upon exit

Examples:
  portarmor doctor
  portarmor doctor --threshold 50.0 --json
  portarmor shield --listen 127.0.0.1:8080 --target https://api.stripe.com --pool-size 100
`
	fmt.Print(usage)
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	procRoot := fs.String("proc-root", "/proc", "Path to proc filesystem")
	threshold := fs.Float64("threshold", 70.0, "Warning threshold percentage")
	jsonOutput := fs.Bool("json", false, "Output in JSON format")

	if err := fs.Parse(args); err != nil {
		return err
	}

	doc := sentry.New()
	report, err := doc.Inspect(sentry.DoctorConfig{
		ProcRoot:     *procRoot,
		ThresholdPct: *threshold,
	})
	if err != nil {
		return fmt.Errorf("doctor inspection failed: %w", err)
	}

	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	fmt.Println("================================================================================")
	fmt.Println("                            PORTARMOR DOCTOR REPORT                             ")
	fmt.Println("================================================================================")
	fmt.Printf(" Ephemeral Port Range:   %d - %d (Capacity: %d ports)\n",
		report.PortRange.Min, report.PortRange.Max, report.PortRange.Capacity)
	fmt.Printf(" Total Active Sockets:   %d\n", report.TotalSockets)
	fmt.Printf(" TIME_WAIT Sockets:      %d\n", report.TotalTimeWait)
	fmt.Printf(" ESTABLISHED Sockets:    %d\n", report.TotalEstablished)
	fmt.Printf(" Overall Saturation:     %.2f%%\n", report.OverallSaturationPct)
	fmt.Printf(" Status:                 [%s]\n", report.Status)
	fmt.Printf(" Summary:                %s\n", report.Summary)
	fmt.Printf(" Recommendation:         %s\n", report.Recommendation)
	fmt.Println("--------------------------------------------------------------------------------")

	if len(report.TopSinks) > 0 {
		fmt.Println(" Top Destination Sinks:")
		limit := 5
		if len(report.TopSinks) < limit {
			limit = len(report.TopSinks)
		}
		for i := 0; i < limit; i++ {
			s := report.TopSinks[i]
			fmt.Printf("   [%d] %-24s Total: %-5d (TIME_WAIT: %-5d, ESTAB: %-5d) Saturation: %5.2f%%\n",
				i+1, s.Destination, s.TotalSockets, s.TimeWaitCount, s.EstablishedCount, s.SaturationPct)
		}
	} else {
		fmt.Println(" No active outbound TCP sockets detected.")
	}
	fmt.Println("================================================================================")

	return nil
}

func runShield(args []string) error {
	fs := flag.NewFlagSet("shield", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:8080", "Local listen address")
	target := fs.String("target", "", "Upstream target URL")
	poolSize := fs.Int("pool-size", 100, "Maximum pooled connections")
	jsonOutput := fs.Bool("json", false, "Output metrics in JSON upon exit")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *target == "" {
		return fmt.Errorf("--target is required (e.g. --target https://api.stripe.com)")
	}

	p, err := shield.New(shield.Config{
		ListenAddr: *listen,
		TargetURL:  *target,
		PoolSize:   *poolSize,
	})
	if err != nil {
		return fmt.Errorf("failed to create shield proxy: %w", err)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() {
		fmt.Printf("portarmor shield listening on %s -> pooling outbound connections to %s (pool size: %d)\n",
			*listen, *target, *poolSize)
		serverErr <- p.Start()
	}()

	select {
	case sig := <-sigChan:
		fmt.Fprintf(os.Stderr, "\nReceived signal %v, shutting down portarmor shield...\n", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	case err := <-serverErr:
		if err != nil && !strings.Contains(err.Error(), "Server closed") {
			return fmt.Errorf("shield server error: %w", err)
		}
	}

	metrics := p.GetMetrics()
	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(metrics)
	}

	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Printf("Shield Summary:\n  Total Requests Pooled: %d\n", metrics.RequestsTotal)
	fmt.Println("--------------------------------------------------------------------------------")

	return nil
}

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestMain_Usage(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printUsage()

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "doctor") || !strings.Contains(out, "shield") {
		t.Errorf("expected usage to contain doctor and shield, got: %s", out)
	}
}

func TestRunDoctor_CLI(t *testing.T) {
	// Runs against local host /proc (or falls back safely)
	err := runDoctor([]string{"--threshold", "50.0"})
	if err != nil {
		t.Errorf("expected runDoctor to succeed on local /proc, got: %v", err)
	}
}

func TestRunShield_Validation(t *testing.T) {
	// Missing --target flag
	err := runShield([]string{})
	if err == nil {
		t.Errorf("expected error when --target flag is omitted")
	}
}

func TestVersionOutput(t *testing.T) {
	if version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", version)
	}
}

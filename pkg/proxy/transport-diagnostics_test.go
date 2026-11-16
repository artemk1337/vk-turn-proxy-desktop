package proxy

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestTransportDiagnosticErrorKeepsCauseAndRedactsCredentials(t *testing.T) {
	creds := &TURNCreds{Username: "private-username", Password: "private-password"}
	err := fmt.Errorf("TURN allocate: %w", errors.New("timeout private-username private-password\nnext\rline\x00"))
	message := transportDiagnosticError(err, creds)
	if strings.Contains(message, creds.Username) || strings.Contains(message, creds.Password) {
		t.Fatal("transport diagnostic contains credentials")
	}
	if !strings.Contains(message, "TURN allocate: timeout") || !strings.Contains(message, "[redacted]") {
		t.Fatalf("diagnostic lost the wrapped failure cause: %q", message)
	}
	if strings.ContainsAny(message, "\r\n\x00") {
		t.Fatalf("diagnostic contains a log line separator: %q", message)
	}
}

func TestTransportDiagnosticErrorAcceptsEmptyCredentialFields(t *testing.T) {
	message := transportDiagnosticError(errors.New("request timed out"), &TURNCreds{})
	if message != "request timed out" {
		t.Fatalf("diagnostic = %q", message)
	}
}

func TestTransportDiagnosticRedactsOverlappingCredentials(t *testing.T) {
	creds := &TURNCreds{Username: "prefix", Password: "prefix-private-suffix"}
	message := transportDiagnosticError(errors.New("failure prefix-private-suffix prefix"), creds)
	if message != "failure [redacted] [redacted]" {
		t.Fatalf("diagnostic = %q", message)
	}
}

package proxy

import (
	"context"
	"errors"
	"log"
	"net"
	"strings"
	"time"
)

// Keep successful setup detail on the bootstrap connection and failures on
// every connection. The trace records no credentials or packet contents.
func (p *Proxy) traceTransportStage(ctx context.Context, connIdx int, relay, stage string, creds *TURNCreds) func(error) {
	started := time.Now()
	transport := "TCP"
	if p.config.UseUDP {
		transport = "UDP"
	}
	if connIdx == 0 {
		log.Printf("proxy: [conn %d] %s %s starting (relay=%s)", connIdx, transport, stage, relay)
	}
	return func(err error) {
		elapsed := time.Since(started).Round(time.Millisecond)
		if err == nil {
			if connIdx == 0 {
				log.Printf("proxy: [conn %d] %s %s ready in %s (relay=%s)", connIdx, transport, stage, elapsed, relay)
			}
			return
		}
		result := "failed"
		var networkError net.Error
		switch {
		case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
			result = "cancelled"
		case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded), errors.As(err, &networkError) && networkError.Timeout():
			result = "timed out"
		}
		log.Printf("proxy: [conn %d] %s %s %s after %s (relay=%s): %s", connIdx, transport, stage, result, elapsed, relay, transportDiagnosticError(err, creds))
	}
}

func transportDiagnosticError(err error, creds *TURNCreds) string {
	message := err.Error()
	secrets := []string{creds.Username, creds.Password}
	if len(secrets[0]) < len(secrets[1]) {
		secrets[0], secrets[1] = secrets[1], secrets[0]
	}
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	return strings.NewReplacer("\x00", "", "\r", " ", "\n", " ").Replace(message)
}

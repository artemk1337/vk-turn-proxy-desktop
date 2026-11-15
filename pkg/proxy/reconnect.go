package proxy

import (
	"context"
	"math/rand"
	"time"
)

// Quota refusals are checked once a minute. Other failures use jittered retries.
func reconnectDelayForError(err error, attempt int) time.Duration {
	if isQuotaError(err) {
		return time.Minute
	}
	return reconnectDelay(attempt)
}

// Equal jitter keeps every retry delayed while spreading simultaneous clients.
func reconnectDelay(attempt int) time.Duration {
	ceiling := 2 * time.Second
	for i := 1; i < attempt && ceiling < 30*time.Second; i++ {
		ceiling *= 2
		if ceiling > 30*time.Second {
			ceiling = 30 * time.Second
		}
	}
	floor := ceiling / 2
	return floor + time.Duration(rand.Int63n(int64(ceiling-floor)))
}

// reportRetry returns whether the shared quota gate owns the next wait.
func (p *Proxy) reportRetry(err error, delay time.Duration) bool {
	p.retryMu.Lock()
	defer p.retryMu.Unlock()
	if err == nil {
		p.retryAt = time.Time{}
		p.quotaRetryAt = time.Time{}
		p.quotaError = nil
		if p.quotaWake != nil {
			close(p.quotaWake)
			p.quotaWake = nil
		}
	} else {
		if p.activeConns.Load() > 0 {
			return false
		}
		now := time.Now()
		if isQuotaError(err) {
			p.quotaError = err
			if !p.quotaRetryAt.After(now) {
				p.quotaRetryAt = now.Add(time.Minute)
			}
		}
		next := now.Add(delay)
		if p.quotaError != nil && p.quotaRetryAt.After(now) {
			err, next = p.quotaError, p.quotaRetryAt
			delay = next.Sub(now)
			if p.retryAt.Equal(next) {
				return true
			}
		} else if p.retryAt.After(now) && !next.Before(p.retryAt) {
			return false
		}
		p.retryAt = next
	}
	if p.config.OnRetry != nil {
		p.config.OnRetry(err, delay)
	}
	return p.quotaError != nil
}

// quotaProbeWaitLocked admits one recovery probe per minute after a real
// quota refusal while the tunnel has no working sessions. retryMu is held.
func (p *Proxy) quotaProbeWaitLocked(now time.Time) (time.Duration, <-chan struct{}) {
	if p.activeConns.Load() > 0 || p.quotaRetryAt.IsZero() {
		return 0, nil
	}
	if !p.quotaRetryAt.After(now) {
		p.quotaRetryAt = now.Add(time.Minute)
		return 0, nil
	}
	if p.quotaWake == nil {
		p.quotaWake = make(chan struct{})
	}
	return p.quotaRetryAt.Sub(now), p.quotaWake
}

func (p *Proxy) waitQuotaProbe(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.ctx.Err(); err != nil {
			return err
		}
		p.retryMu.Lock()
		delay, wake := p.quotaProbeWaitLocked(time.Now())
		p.retryMu.Unlock()
		if delay == 0 {
			return nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-wake:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-p.ctx.Done():
			timer.Stop()
			return p.ctx.Err()
		}
		timer.Stop()
	}
}

func (p *Proxy) waitReconnect(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ctx.Done():
		return p.ctx.Err()
	}
}

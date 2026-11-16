package proxy

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReconnectDelayBounds(t *testing.T) {
	for _, tc := range []struct {
		attempt        int
		floor, ceiling time.Duration
	}{
		{1, time.Second, 2 * time.Second},
		{2, 2 * time.Second, 4 * time.Second},
		{3, 4 * time.Second, 8 * time.Second},
		{4, 8 * time.Second, 16 * time.Second},
		{5, 15 * time.Second, 30 * time.Second},
		{1000, 15 * time.Second, 30 * time.Second},
	} {
		values := map[time.Duration]bool{}
		for range 100 {
			delay := reconnectDelay(tc.attempt)
			if delay < tc.floor || delay >= tc.ceiling {
				t.Fatalf("attempt %d delay %s outside [%s, %s)", tc.attempt, delay, tc.floor, tc.ceiling)
			}
			values[delay] = true
		}
		if len(values) < 2 {
			t.Fatalf("attempt %d has no jitter", tc.attempt)
		}
	}
}

func TestReconnectWaitCancellation(t *testing.T) {
	for _, cancelProxy := range []bool{false, true} {
		proxyCtx, stopProxy := context.WithCancel(context.Background())
		sessionCtx, stopSession := context.WithCancel(context.Background())
		p := &Proxy{ctx: proxyCtx}
		if cancelProxy {
			stopProxy()
		} else {
			stopSession()
		}
		start := time.Now()
		err := p.waitReconnect(sessionCtx, time.Hour)
		stopProxy()
		stopSession()
		if !errors.Is(err, context.Canceled) || time.Since(start) > time.Second {
			t.Fatalf("cancelProxy %v: wait returned %v after %s", cancelProxy, err, time.Since(start))
		}
	}
}

func TestRetryReportClearsOnConnectionRecovery(t *testing.T) {
	failure := errors.New("TURN allocation quota reached")
	var reported error
	var delay time.Duration
	p := &Proxy{config: Config{OnRetry: func(err error, next time.Duration) { reported, delay = err, next }}}
	p.reportRetry(failure, time.Second)
	if !errors.Is(reported, failure) || delay != time.Second {
		t.Fatal("retry report missing")
	}
	p.activeConns.Store(1)
	p.noteSessionUp(1)
	p.reportRetry(failure, time.Second)
	if reported != nil || delay != 0 {
		t.Fatal("successful connection did not clear retry report")
	}
}

func TestQuotaRetryWaitsOneMinuteDespitePoolBroadcasts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	cp := newCredPool(ctx, 1, 0, "", func(bool, int) (string, *TURNCreds, error) {
		return "", nil, errors.New("unexpected fetch")
	})
	var delay time.Duration
	p := &Proxy{ctx: ctx, credPool: cp, config: Config{OnRetry: func(_ error, next time.Duration) { delay = next }}}
	attempts := 0
	p.sessionHook = func(context.Context, int) error {
		attempts++
		return errors.New("TURN allocate: error 486: Allocation Quota Reached")
	}
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				cp.broadcastSlotAvailable()
			case <-ctx.Done():
				return
			}
		}
	}()
	if err := p.runConnection(ctx, "", nil, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runConnection returned %v", err)
	}
	if attempts != 1 || delay != time.Minute {
		t.Fatalf("quota retry attempts=%d delay=%s, want one immediate attempt and a one-minute wait", attempts, delay)
	}
}

func TestQuotaReconnectDelayIsOneMinute(t *testing.T) {
	quota := errors.New("SRTP setup: TURN allocate: error 486: Allocation Quota Reached")
	for _, attempt := range []int{1, 2, 20, 1000} {
		for range 100 {
			if got := reconnectDelayForError(quota, attempt); got != time.Minute {
				t.Fatalf("attempt %d quota delay=%s, want exactly one minute", attempt, got)
			}
		}
	}
	delay := reconnectDelayForError(errors.New("dial tcp: connection refused"), 1)
	if delay < time.Second || delay >= 2*time.Second {
		t.Fatalf("network retry delay=%s, want 1–2 s jitter", delay)
	}
}

func TestRetryReportKeepsTheEarliestPendingAttempt(t *testing.T) {
	failure := errors.New("TURN allocation refused")
	reports := 0
	var delay time.Duration
	p := &Proxy{config: Config{OnRetry: func(_ error, next time.Duration) {
		reports++
		delay = next
	}}}
	p.reportRetry(failure, 10*time.Second)
	first := p.retryAt
	p.reportRetry(failure, 20*time.Second)
	if reports != 1 || !p.retryAt.Equal(first) {
		t.Fatal("a later worker overwrote the earliest retry deadline")
	}
	p.reportRetry(failure, time.Second)
	if reports != 2 || delay != time.Second || !p.retryAt.Before(first) {
		t.Fatal("an earlier worker did not update the retry deadline")
	}
	p.retryAt = time.Now().Add(-time.Second)
	p.reportRetry(failure, 20*time.Second)
	if reports != 3 || delay != 20*time.Second {
		t.Fatal("an expired deadline suppressed the next retry")
	}
	p.noteSessionUp(2)
	if !p.retryAt.IsZero() || delay != 0 {
		t.Fatal("connection recovery did not clear the retry deadline")
	}
}

func TestLastConnectionFailureReportsReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reported := false
	p := &Proxy{ctx: ctx, config: Config{OnRetry: func(err error, delay time.Duration) {
		if err != nil && delay >= time.Second {
			reported = true
		}
		cancel()
	}}}
	p.sessionHook = func(context.Context, int) error {
		return errors.New("TURN allocation refused (error 486: Allocation Quota Reached)")
	}
	err := p.runConnection(ctx, "", nil, 3)
	if !errors.Is(err, context.Canceled) || !reported {
		t.Fatalf("last connection failure report=%v, result=%v", reported, err)
	}
}

func TestQuotaProbeGateAdmitsOnlyOneWorkerPerMinute(t *testing.T) {
	now := time.Unix(1800000000, 0)
	p := &Proxy{}
	if delay, _ := p.quotaProbeWaitLocked(now); delay != 0 {
		t.Fatal("the first attempt was gated without a server refusal")
	}
	p.quotaRetryAt = now.Add(time.Minute)
	for range 2 {
		if delay, _ := p.quotaProbeWaitLocked(now); delay != time.Minute {
			t.Fatalf("worker got delay=%s before the quota deadline", delay)
		}
	}
	if delay, _ := p.quotaProbeWaitLocked(now.Add(time.Minute)); delay != 0 {
		t.Fatal("the first worker could not probe at the deadline")
	}
	if delay, _ := p.quotaProbeWaitLocked(now.Add(time.Minute)); delay != time.Minute {
		t.Fatal("a second worker bypassed the reserved probe")
	}
	p.activeConns.Store(1)
	if delay, _ := p.quotaProbeWaitLocked(now.Add(time.Minute)); delay != 0 {
		t.Fatal("the quota gate blocked a tunnel with a working session")
	}
}

func TestQuotaProbeGateStopsBothWorkersPromptly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	refused := make(chan struct{}, 1)
	p := &Proxy{ctx: ctx, config: Config{OnRetry: func(err error, _ time.Duration) {
		if isQuotaError(err) {
			select {
			case refused <- struct{}{}:
			default:
			}
		}
	}}}
	var attempts atomic.Int64
	p.sessionHook = func(context.Context, int) error {
		attempts.Add(1)
		return errors.New("TURN allocate: error 486: Allocation Quota Reached")
	}
	var workers sync.WaitGroup
	workers.Go(func() { _ = p.runConnection(ctx, "", nil, 0) })
	select {
	case <-refused:
	case <-ctx.Done():
		t.Fatal("the initial attempt did not reach the server")
	}
	workers.Go(func() { _ = p.runConnection(ctx, "", nil, 1) })
	workers.Wait()
	if got := attempts.Load(); got != 1 {
		t.Fatalf("allocation attempts=%d, want only the immediate attempt", got)
	}
}

func TestQuotaRefusalOverridesEarlierGenericRetryReport(t *testing.T) {
	var reported error
	var delay time.Duration
	p := &Proxy{config: Config{OnRetry: func(err error, next time.Duration) { reported, delay = err, next }}}
	generic := errors.New("network unavailable")
	quota := errors.New("TURN allocate: error 486: Allocation Quota Reached")
	p.reportRetry(generic, time.Second)
	p.reportRetry(quota, time.Minute)
	if !errors.Is(reported, quota) || delay != time.Minute {
		t.Fatal("the actual quota refusal did not replace the earlier generic retry")
	}
	p.reportRetry(generic, time.Millisecond)
	if !errors.Is(reported, quota) {
		t.Fatal("a generic worker error replaced the quota waiting state")
	}
	_, wake := p.quotaProbeWaitLocked(time.Now())
	p.noteSessionUp(2)
	select {
	case <-wake:
	default:
		t.Fatal("successful recovery did not wake the quota waiters")
	}
	if !p.quotaRetryAt.IsZero() || p.quotaError != nil {
		t.Fatal("successful recovery retained the quota gate")
	}
}

func TestCachedQuotaCooldownUsesItsDeadline(t *testing.T) {
	until := time.Now().Add(9 * time.Minute)
	failure := &QuotaCooldownError{Until: until}
	delay := reconnectDelayForError(failure, 1)
	if delay < 8*time.Minute || delay > 9*time.Minute {
		t.Fatalf("delay = %s", delay)
	}
	var reported error
	var reportedDelay time.Duration
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &Proxy{ctx: ctx, config: Config{OnRetry: func(err error, delay time.Duration) { reported = err; reportedDelay = delay }}}
	if !p.reportRetry(failure, delay) || reported != failure || reportedDelay < 8*time.Minute {
		t.Fatalf("report = %v, %s", reported, reportedDelay)
	}
	cancel()
	if err := p.waitQuotaProbe(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
}

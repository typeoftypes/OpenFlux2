package transport

import (
	"testing"
	"time"
)

func TestStealthConfigJitter(t *testing.T) {
	config := DefaultStealthConfig()

	// Test jitter is within range
	for i := 0; i < 100; i++ {
		jitter := config.ApplyJitter()
		if jitter < config.JitterMin || jitter > config.JitterMax {
			t.Errorf("jitter %v outside range [%v, %v]", jitter, config.JitterMin, config.JitterMax)
		}
	}
}

func TestStealthConfigPadding(t *testing.T) {
	config := DefaultStealthConfig()

	testData := []byte("test payload data")

	// Add padding
	padded := config.AddPadding(testData)
	if len(padded) <= len(testData) {
		t.Error("padded data should be larger than original")
	}

	// Remove padding
	extracted, err := config.RemovePadding(padded)
	if err != nil {
		t.Errorf("RemovePadding failed: %v", err)
	}

	if string(extracted) != string(testData) {
		t.Errorf("extracted data mismatch: got %q, want %q", extracted, testData)
	}
}

func TestStealthConfigTrafficShaping(t *testing.T) {
	config := DefaultStealthConfig()
	config.BurstSize = 3

	// First 3 packets should not throttle
	for i := 1; i <= 2; i++ {
		shouldThrottle, _ := config.ShouldThrottle(i)
		if shouldThrottle {
			t.Errorf("packet %d should not throttle", i)
		}
	}

	// 3rd packet should throttle
	shouldThrottle, delay := config.ShouldThrottle(3)
	if !shouldThrottle {
		t.Error("burst size reached, should throttle")
	}
	if delay <= 0 {
		t.Error("throttle delay should be positive")
	}
}

func TestCircuitBreaker(t *testing.T) {
	config := DefaultResilienceConfig()
	config.FailureThreshold = 3
	config.SuccessThreshold = 2

	cb := NewCircuitBreaker(config)

	// Initially closed
	if !cb.CanAttempt() {
		t.Error("circuit should start closed")
	}

	// Record failures to open circuit
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	if cb.GetState() != CircuitOpen {
		t.Errorf("circuit should be open, got %v", cb.GetState())
	}

	if cb.CanAttempt() {
		t.Error("should not attempt when circuit is open")
	}
}

func TestHealthMonitor(t *testing.T) {
	config := DefaultResilienceConfig()
	config.UnhealthyThreshold = 3
	config.HealthyThreshold = 2

	hm := NewHealthMonitor(config)

	// Initially healthy
	if !hm.IsHealthy() {
		t.Error("should start healthy")
	}

	// Record failures
	for i := 0; i < 3; i++ {
		hm.RecordCheck(false)
	}

	if hm.IsHealthy() {
		t.Error("should be unhealthy after threshold")
	}

	// Record successes to become healthy again
	for i := 0; i < 2; i++ {
		hm.RecordCheck(true)
	}

	if !hm.IsHealthy() {
		t.Error("should be healthy after success threshold")
	}

	hm.Stop()
}

func TestAdaptiveTimeout(t *testing.T) {
	config := DefaultResilienceConfig()
	config.MinTimeout = 5 * time.Second
	config.MaxTimeout = 60 * time.Second
	config.TimeoutMultiplier = 1.5

	at := NewAdaptiveTimeout(config)

	// Initial timeout should be minimum
	if at.GetTimeout() != config.MinTimeout {
		t.Errorf("initial timeout should be %v, got %v", config.MinTimeout, at.GetTimeout())
	}

	// Record some latencies
	at.RecordLatency(10 * time.Second)
	at.RecordLatency(12 * time.Second)
	at.RecordLatency(11 * time.Second)

	timeout := at.GetTimeout()

	// Should be adjusted based on latency
	expectedMin := time.Duration(float64(11*time.Second) * config.TimeoutMultiplier)
	if timeout < expectedMin {
		t.Errorf("timeout too low: got %v, expected at least %v", timeout, expectedMin)
	}

	// Should not exceed maximum
	if timeout > config.MaxTimeout {
		t.Errorf("timeout exceeds maximum: got %v, max %v", timeout, config.MaxTimeout)
	}
}

func TestRandomBackoff(t *testing.T) {
	min := 100 * time.Millisecond
	max := 10 * time.Second
	multiplier := 2.0

	// Test increasing backoff
	var prev time.Duration
	for attempt := 0; attempt < 5; attempt++ {
		delay := RandomBackoff(attempt, min, max, multiplier)

		if delay < min {
			t.Errorf("delay %v below minimum %v", delay, min)
		}
		if delay > max {
			t.Errorf("delay %v exceeds maximum %v", delay, max)
		}

		// Later attempts should generally have longer delays
		// (accounting for jitter, this isn't always true)
		if attempt > 0 && delay < prev/2 {
			t.Logf("warning: delay decreased significantly: %v -> %v", prev, delay)
		}

		prev = delay
	}
}

func TestBrowserProfiles(t *testing.T) {
	// Test multiple random profiles
	profiles := make(map[string]bool)

	for i := 0; i < 20; i++ {
		profile := GetRandomBrowserProfile()

		if profile.UserAgent == "" {
			t.Error("UserAgent should not be empty")
		}
		if profile.Accept == "" {
			t.Error("Accept should not be empty")
		}
		if profile.AcceptLanguage == "" {
			t.Error("AcceptLanguage should not be empty")
		}

		profiles[profile.UserAgent] = true
	}

	// Should get at least 2 different profiles in 20 tries
	if len(profiles) < 2 {
		t.Error("should get variety of browser profiles")
	}
}

func TestConnectionPool(t *testing.T) {
	config := DefaultEnhancedDirectConfig()
	config.PoolSize = 3

	// Note: This is a basic structure test
	// Real connection pool needs actual network for full testing
	pool := NewConnectionPool("localhost:8080", config)

	if pool == nil {
		t.Fatal("pool should not be nil")
	}

	pool.Close()
}

func TestSmartReconnector(t *testing.T) {
	cb := NewCircuitBreaker(DefaultResilienceConfig())
	reconnector := NewSmartReconnector(
		100*time.Millisecond,
		5*time.Second,
		2.0,
		cb,
	)

	// Should allow first attempt
	canReconnect, _ := reconnector.ShouldReconnect()
	if !canReconnect {
		t.Error("should allow first reconnect")
	}

	reconnector.RecordAttempt()

	// Immediately after attempt, should wait
	canReconnect, _ = reconnector.ShouldReconnect()
	if canReconnect {
		t.Error("should wait after recent attempt")
	}

	// After success, should reset
	reconnector.RecordSuccess()
	time.Sleep(150 * time.Millisecond)

	canReconnect, _ = reconnector.ShouldReconnect()
	if !canReconnect {
		t.Error("should allow reconnect after success and delay")
	}
}

func TestEnhancedHTTPTransport(t *testing.T) {
	config := DefaultConfig()
	eht := NewEnhancedHTTPTransport(config)

	if eht == nil {
		t.Fatal("enhanced HTTP transport should not be nil")
	}

	// Test health
	if !eht.IsHealthy() {
		t.Error("should start healthy")
	}

	// Record success
	eht.RecordSuccess(100 * time.Millisecond)

	// Test timeout
	timeout := eht.GetTimeout()
	if timeout <= 0 {
		t.Error("timeout should be positive")
	}

	// Get stats
	stats := eht.GetStats()
	if stats == nil {
		t.Error("stats should not be nil")
	}
}

func TestConnectionMonitor(t *testing.T) {
	recoveryTriggered := make(chan struct{})

	monitor := NewConnectionMonitor(3, 30*time.Second, func() {
		close(recoveryTriggered)
	})

	// Record activity
	monitor.RecordActivity()

	if monitor.IsSilent() {
		t.Error("should not be silent immediately after activity")
	}

	// Record errors
	monitor.RecordError()
	monitor.RecordError()

	triggered := monitor.RecordError()
	if !triggered {
		t.Error("should trigger recovery after threshold")
	}

	select {
	case <-recoveryTriggered:
	case <-time.After(1 * time.Second):
		t.Fatal("recovery callback should have been triggered")
	}

	// Check health
	health := monitor.GetHealth()
	if health < 0 || health > 1 {
		t.Errorf("health should be 0-1, got %f", health)
	}
}

func TestPacketRateLimiter(t *testing.T) {
	limiter := NewPacketRateLimiter(10*time.Millisecond, 5)

	start := time.Now()

	// Send burst
	for i := 0; i < 10; i++ {
		limiter.Wait()
	}

	elapsed := time.Since(start)

	// Should take at least one interval (after 5 packets)
	minExpected := 10 * time.Millisecond
	if elapsed < minExpected {
		t.Errorf("rate limiter too fast: %v < %v", elapsed, minExpected)
	}
}

func BenchmarkJitter(b *testing.B) {
	config := DefaultStealthConfig()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		config.ApplyJitter()
	}
}

func BenchmarkPadding(b *testing.B) {
	config := DefaultStealthConfig()
	data := []byte("benchmark test data payload")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		padded := config.AddPadding(data)
		config.RemovePadding(padded)
	}
}

func BenchmarkCircuitBreaker(b *testing.B) {
	config := DefaultResilienceConfig()
	cb := NewCircuitBreaker(config)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cb.CanAttempt()
		if i%2 == 0 {
			cb.RecordSuccess()
		} else {
			cb.RecordFailure()
		}
	}
}

func BenchmarkBrowserProfile(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GetRandomBrowserProfile()
	}
}

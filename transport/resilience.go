package transport

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// ResilienceConfig contains advanced connection stability parameters
type ResilienceConfig struct {
	// Health checking
	EnableHealthCheck     bool
	HealthCheckInterval   time.Duration
	UnhealthyThreshold    int
	HealthyThreshold      int
	
	// Automatic recovery
	EnableAutoRecovery    bool
	RecoveryCheckInterval time.Duration
	
	// Connection pooling for reliability
	EnablePooling         bool
	PoolSize              int
	
	// Adaptive timeout
	EnableAdaptiveTimeout bool
	MinTimeout            time.Duration
	MaxTimeout            time.Duration
	TimeoutMultiplier     float64
	
	// Circuit breaker
	EnableCircuitBreaker  bool
	FailureThreshold      int
	SuccessThreshold      int
	HalfOpenTimeout       time.Duration
	
	// Dead connection detection
	DeadConnectionTimeout time.Duration
	EnableDeadDetection   bool
}

// DefaultResilienceConfig returns production resilience settings
func DefaultResilienceConfig() ResilienceConfig {
	return ResilienceConfig{
		EnableHealthCheck:     true,
		HealthCheckInterval:   15 * time.Second,
		UnhealthyThreshold:    3,
		HealthyThreshold:      2,
		EnableAutoRecovery:    true,
		RecoveryCheckInterval: 5 * time.Second,
		EnablePooling:         false,
		PoolSize:              3,
		EnableAdaptiveTimeout: true,
		MinTimeout:            5 * time.Second,
		MaxTimeout:            60 * time.Second,
		TimeoutMultiplier:     1.5,
		EnableCircuitBreaker:  true,
		FailureThreshold:      5,
		SuccessThreshold:      2,
		HalfOpenTimeout:       30 * time.Second,
		DeadConnectionTimeout: 90 * time.Second,
		EnableDeadDetection:   true,
	}
}

// CircuitState represents the state of a circuit breaker
type CircuitState int32

const (
	CircuitClosed CircuitState = iota
	CircuitOpen
	CircuitHalfOpen
)

// CircuitBreaker implements the circuit breaker pattern for fault tolerance
type CircuitBreaker struct {
	mu sync.RWMutex
	
	state         atomic.Int32 // CircuitState
	failures      atomic.Int32
	successes     atomic.Int32
	lastFailure   time.Time
	lastStateChange time.Time
	
	config ResilienceConfig
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(config ResilienceConfig) *CircuitBreaker {
	cb := &CircuitBreaker{
		config: config,
	}
	cb.state.Store(int32(CircuitClosed))
	return cb
}

// CanAttempt checks if an operation should be attempted
func (cb *CircuitBreaker) CanAttempt() bool {
	if !cb.config.EnableCircuitBreaker {
		return true
	}
	
	state := CircuitState(cb.state.Load())
	
	switch state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		// Check if we should try half-open
		cb.mu.RLock()
		elapsed := time.Since(cb.lastStateChange)
		cb.mu.RUnlock()
		
		if elapsed >= cb.config.HalfOpenTimeout {
			cb.state.Store(int32(CircuitHalfOpen))
			return true
		}
		return false
	case CircuitHalfOpen:
		return true
	}
	
	return false
}

// RecordSuccess records a successful operation
func (cb *CircuitBreaker) RecordSuccess() {
	if !cb.config.EnableCircuitBreaker {
		return
	}
	
	state := CircuitState(cb.state.Load())
	
	if state == CircuitHalfOpen {
		successes := cb.successes.Add(1)
		if int(successes) >= cb.config.SuccessThreshold {
			cb.mu.Lock()
			cb.state.Store(int32(CircuitClosed))
			cb.failures.Store(0)
			cb.successes.Store(0)
			cb.lastStateChange = time.Now()
			cb.mu.Unlock()
		}
	} else if state == CircuitClosed {
		cb.failures.Store(0)
	}
}

// RecordFailure records a failed operation
func (cb *CircuitBreaker) RecordFailure() {
	if !cb.config.EnableCircuitBreaker {
		return
	}
	
	cb.mu.Lock()
	cb.lastFailure = time.Now()
	cb.mu.Unlock()
	
	state := CircuitState(cb.state.Load())
	
	if state == CircuitHalfOpen {
		cb.state.Store(int32(CircuitOpen))
		cb.mu.Lock()
		cb.failures.Store(0)
		cb.successes.Store(0)
		cb.lastStateChange = time.Now()
		cb.mu.Unlock()
		return
	}
	
	if state == CircuitClosed {
		failures := cb.failures.Add(1)
		if int(failures) >= cb.config.FailureThreshold {
			cb.state.Store(int32(CircuitOpen))
			cb.mu.Lock()
			cb.lastStateChange = time.Now()
			cb.mu.Unlock()
		}
	}
}

// GetState returns current circuit breaker state
func (cb *CircuitBreaker) GetState() CircuitState {
	return CircuitState(cb.state.Load())
}

// HealthMonitor tracks connection health
type HealthMonitor struct {
	mu sync.RWMutex
	
	consecutiveFailures atomic.Int32
	consecutiveSuccesses atomic.Int32
	isHealthy           atomic.Int32 // 0 = unhealthy, 1 = healthy
	lastCheck           time.Time
	
	config ResilienceConfig
	
	ctx    context.Context
	cancel context.CancelFunc
}

// NewHealthMonitor creates a new health monitor
func NewHealthMonitor(config ResilienceConfig) *HealthMonitor {
	ctx, cancel := context.WithCancel(context.Background())
	hm := &HealthMonitor{
		config: config,
		ctx:    ctx,
		cancel: cancel,
	}
	hm.isHealthy.Store(1)
	return hm
}

// Start begins health monitoring
func (hm *HealthMonitor) Start(checkFunc func() bool) {
	if !hm.config.EnableHealthCheck {
		return
	}
	
	go hm.monitorLoop(checkFunc)
}

// Stop stops health monitoring
func (hm *HealthMonitor) Stop() {
	hm.cancel()
}

func (hm *HealthMonitor) monitorLoop(checkFunc func() bool) {
	ticker := time.NewTicker(hm.config.HealthCheckInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-hm.ctx.Done():
			return
		case <-ticker.C:
			healthy := checkFunc()
			hm.RecordCheck(healthy)
		}
	}
}

// RecordCheck records a health check result
func (hm *HealthMonitor) RecordCheck(success bool) {
	hm.mu.Lock()
	hm.lastCheck = time.Now()
	hm.mu.Unlock()
	
	if success {
		hm.consecutiveFailures.Store(0)
		successes := hm.consecutiveSuccesses.Add(1)
		
		if int(successes) >= hm.config.HealthyThreshold {
			hm.isHealthy.Store(1)
		}
	} else {
		hm.consecutiveSuccesses.Store(0)
		failures := hm.consecutiveFailures.Add(1)
		
		if int(failures) >= hm.config.UnhealthyThreshold {
			hm.isHealthy.Store(0)
		}
	}
}

// IsHealthy returns current health status
func (hm *HealthMonitor) IsHealthy() bool {
	return hm.isHealthy.Load() == 1
}

// GetStats returns health statistics
func (hm *HealthMonitor) GetStats() (healthy bool, consecutiveFailures, consecutiveSuccesses int) {
	return hm.IsHealthy(), 
		int(hm.consecutiveFailures.Load()), 
		int(hm.consecutiveSuccesses.Load())
}

// AdaptiveTimeout manages dynamic timeout adjustment
type AdaptiveTimeout struct {
	mu sync.RWMutex
	
	current       time.Duration
	config        ResilienceConfig
	recentLatencies []time.Duration
	maxSamples    int
}

// NewAdaptiveTimeout creates a new adaptive timeout manager
func NewAdaptiveTimeout(config ResilienceConfig) *AdaptiveTimeout {
	return &AdaptiveTimeout{
		current:       config.MinTimeout,
		config:        config,
		recentLatencies: make([]time.Duration, 0, 20),
		maxSamples:    20,
	}
}

// RecordLatency records an operation latency
func (at *AdaptiveTimeout) RecordLatency(latency time.Duration) {
	if !at.config.EnableAdaptiveTimeout {
		return
	}
	
	at.mu.Lock()
	defer at.mu.Unlock()
	
	at.recentLatencies = append(at.recentLatencies, latency)
	if len(at.recentLatencies) > at.maxSamples {
		at.recentLatencies = at.recentLatencies[1:]
	}
	
	// Calculate average latency
	var sum time.Duration
	for _, l := range at.recentLatencies {
		sum += l
	}
	avg := sum / time.Duration(len(at.recentLatencies))
	
	// Set timeout to avg * multiplier
	newTimeout := time.Duration(float64(avg) * at.config.TimeoutMultiplier)
	
	// Clamp to min/max
	if newTimeout < at.config.MinTimeout {
		newTimeout = at.config.MinTimeout
	}
	if newTimeout > at.config.MaxTimeout {
		newTimeout = at.config.MaxTimeout
	}
	
	at.current = newTimeout
}

// GetTimeout returns current adaptive timeout
func (at *AdaptiveTimeout) GetTimeout() time.Duration {
	if !at.config.EnableAdaptiveTimeout {
		return at.config.MinTimeout
	}
	
	at.mu.RLock()
	defer at.mu.RUnlock()
	return at.current
}

// Reset resets adaptive timeout to minimum
func (at *AdaptiveTimeout) Reset() {
	at.mu.Lock()
	defer at.mu.Unlock()
	at.current = at.config.MinTimeout
	at.recentLatencies = at.recentLatencies[:0]
}

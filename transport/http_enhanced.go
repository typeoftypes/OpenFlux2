package transport

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/p1neappleXpress/OpenFlux/utils"
)

// EnhancedHTTPTransport wraps HTTP transports with stability and masking
// so requests look like a person using a website (browse phases + headers).
type EnhancedHTTPTransport struct {
	mu sync.RWMutex

	// Browser profile for masking (fixed for the "tab" lifetime)
	profile BrowserProfile
	browse  *BrowseSession

	// Stability components
	circuitBreaker  *CircuitBreaker
	healthMonitor   *HealthMonitor
	adaptiveTimeout *AdaptiveTimeout

	// Stealth features
	stealthConfig StealthConfig

	// Request counter for throttling
	requestCount    int
	lastRequestTime time.Time

	// Connection statistics
	successCount int
	failureCount int
}

// NewEnhancedHTTPTransport creates an enhanced HTTP transport wrapper
func NewEnhancedHTTPTransport(config TransportConfig) *EnhancedHTTPTransport {
	profile := GetRandomBrowserProfile()
	if config.BrowserProfile != nil {
		profile = *config.BrowserProfile
	}
	
	bs := NewBrowseSession()
	// Keep profile consistent with the browse session fingerprint.
	if config.BrowserProfile == nil {
		profile = bs.Profile()
	}
	return &EnhancedHTTPTransport{
		profile:         profile,
		browse:          bs,
		circuitBreaker:  NewCircuitBreaker(config.ResilienceConfig),
		healthMonitor:   NewHealthMonitor(config.ResilienceConfig),
		adaptiveTimeout: NewAdaptiveTimeout(config.ResilienceConfig),
		stealthConfig:   config.StealthConfig,
		lastRequestTime: time.Now(),
	}
}

// PrepareRequest applies masking and stability features to HTTP request.
// Timing and Sec-Fetch-* headers follow a BrowseSession so the stream looks
// like page load → reading → click XHR, not a fixed-rate tunnel.
func (eht *EnhancedHTTPTransport) PrepareRequest(req *http.Request) error {
	eht.mu.Lock()
	defer eht.mu.Unlock()

	if !eht.circuitBreaker.CanAttempt() {
		return fmt.Errorf("circuit breaker open")
	}

	kind := KindXHR
	if eht.browse != nil {
		kind = eht.browse.NextKind()
	}
	ApplyKindHeaders(req, eht.profile, kind)

	if req.Header.Get("Referer") == "" {
		referer := GetRealisticReferer(req.URL.String())
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
	}

	// Human-like inter-request gap (browse phases) or classic jitter.
	if eht.stealthConfig.BrowseMode && eht.browse != nil {
		delay := eht.browse.NextDelay()
		if eht.stealthConfig.EnableJitter {
			delay += eht.stealthConfig.ApplyJitter() / 4
		}
		time.Sleep(delay)
	} else if eht.stealthConfig.EnableJitter {
		time.Sleep(eht.stealthConfig.ApplyJitter())
		if shouldThrottle, delay := eht.stealthConfig.ShouldThrottle(eht.requestCount); shouldThrottle {
			utils.Debugf("[HTTP_ENHANCED] throttling for %v", delay)
			time.Sleep(delay)
		}
		elapsed := time.Since(eht.lastRequestTime)
		minDelay := eht.stealthConfig.GetInterPacketDelay()
		if elapsed < minDelay {
			time.Sleep(minDelay - elapsed)
		}
	}

	eht.requestCount++
	eht.lastRequestTime = time.Now()
	return nil
}

// RecordSuccess records a successful request
func (eht *EnhancedHTTPTransport) RecordSuccess(latency time.Duration) {
	eht.mu.Lock()
	defer eht.mu.Unlock()
	
	eht.successCount++
	eht.circuitBreaker.RecordSuccess()
	eht.adaptiveTimeout.RecordLatency(latency)
	
	utils.Debugf("[HTTP_ENHANCED] success, total: %d, latency: %v", eht.successCount, latency)
}

// RecordFailure records a failed request
func (eht *EnhancedHTTPTransport) RecordFailure(err error) {
	eht.mu.Lock()
	defer eht.mu.Unlock()
	
	eht.failureCount++
	eht.circuitBreaker.RecordFailure()
	
	utils.Debugf("[HTTP_ENHANCED] failure: %v, total failures: %d", err, eht.failureCount)
}

// GetTimeout returns adaptive timeout for requests
func (eht *EnhancedHTTPTransport) GetTimeout() time.Duration {
	return eht.adaptiveTimeout.GetTimeout()
}

// IsHealthy checks if transport is healthy
func (eht *EnhancedHTTPTransport) IsHealthy() bool {
	eht.mu.RLock()
	defer eht.mu.RUnlock()
	
	// Simple health check based on success/failure ratio
	total := eht.successCount + eht.failureCount
	if total < 10 {
		return true // Not enough data
	}
	
	successRate := float64(eht.successCount) / float64(total)
	return successRate > 0.5
}

// GetStats returns transport statistics
func (eht *EnhancedHTTPTransport) GetStats() map[string]interface{} {
	eht.mu.RLock()
	defer eht.mu.RUnlock()
	
	return map[string]interface{}{
		"success_count":  eht.successCount,
		"failure_count":  eht.failureCount,
		"request_count":  eht.requestCount,
		"circuit_state":  eht.circuitBreaker.GetState(),
		"timeout":        eht.adaptiveTimeout.GetTimeout(),
		"browser_agent":  eht.profile.UserAgent[:50] + "...",
	}
}

// RefreshProfile changes browser profile for next requests
func (eht *EnhancedHTTPTransport) RefreshProfile() {
	eht.mu.Lock()
	defer eht.mu.Unlock()
	
	eht.profile = GetRandomBrowserProfile()
	utils.Debugf("[HTTP_ENHANCED] refreshed browser profile: %s", eht.profile.String())
}

// Reset resets internal counters and state
func (eht *EnhancedHTTPTransport) Reset() {
	eht.mu.Lock()
	defer eht.mu.Unlock()
	
	eht.requestCount = 0
	eht.lastRequestTime = time.Now()
	eht.adaptiveTimeout.Reset()
	
	utils.Debugf("[HTTP_ENHANCED] reset counters and state")
}

// WebSocketDialerConfig holds WebSocket connection parameters
type WebSocketDialerConfig struct {
	HandshakeTimeout  time.Duration
	EnableCompression bool
	ReadBufferSize    int
	WriteBufferSize   int
	EnableMasking     bool
	
	// Stability features
	EnableKeepalive   bool
	KeepaliveInterval time.Duration
	PingTimeout       time.Duration
	
	// Retry configuration
	MaxRetries        int
	RetryMinDelay     time.Duration
	RetryMaxDelay     time.Duration
	RetryMultiplier   float64
}

// DefaultWebSocketDialerConfig returns optimized WebSocket config
func DefaultWebSocketDialerConfig() WebSocketDialerConfig {
	return WebSocketDialerConfig{
		HandshakeTimeout:  15 * time.Second,
		EnableCompression: true,
		ReadBufferSize:    64 * 1024,
		WriteBufferSize:   64 * 1024,
		EnableMasking:     true,
		EnableKeepalive:   true,
		KeepaliveInterval: 20 * time.Second,
		PingTimeout:       10 * time.Second,
		MaxRetries:        5,
		RetryMinDelay:     500 * time.Millisecond,
		RetryMaxDelay:     30 * time.Second,
		RetryMultiplier:   2.0,
	}
}

// EnhancedWebSocketConn wraps WebSocket with stability features
type EnhancedWebSocketConn struct {
	mu sync.RWMutex
	
	config            WebSocketDialerConfig
	lastPingTime      time.Time
	lastPongTime      time.Time
	consecutiveFails  int
	
	// Message statistics
	messagesSent      int
	messagesReceived  int
	bytesSent         uint64
	bytesReceived     uint64
}

// NewEnhancedWebSocketConn creates enhanced WebSocket wrapper
func NewEnhancedWebSocketConn(config WebSocketDialerConfig) *EnhancedWebSocketConn {
	now := time.Now()
	return &EnhancedWebSocketConn{
		config:       config,
		lastPingTime: now,
		lastPongTime: now,
	}
}

// ShouldSendPing checks if keepalive ping should be sent
func (ewsc *EnhancedWebSocketConn) ShouldSendPing() bool {
	if !ewsc.config.EnableKeepalive {
		return false
	}
	
	ewsc.mu.RLock()
	defer ewsc.mu.RUnlock()
	
	return time.Since(ewsc.lastPingTime) >= ewsc.config.KeepaliveInterval
}

// RecordPingSent records ping sent timestamp
func (ewsc *EnhancedWebSocketConn) RecordPingSent() {
	ewsc.mu.Lock()
	defer ewsc.mu.Unlock()
	ewsc.lastPingTime = time.Now()
}

// RecordPongReceived records pong received timestamp
func (ewsc *EnhancedWebSocketConn) RecordPongReceived() {
	ewsc.mu.Lock()
	defer ewsc.mu.Unlock()
	ewsc.lastPongTime = time.Now()
	ewsc.consecutiveFails = 0
}

// IsConnectionAlive checks if connection appears alive
func (ewsc *EnhancedWebSocketConn) IsConnectionAlive() bool {
	ewsc.mu.RLock()
	defer ewsc.mu.RUnlock()
	
	// If we haven't received pong in 3x the keepalive interval, consider dead
	maxSilence := ewsc.config.KeepaliveInterval * 3
	return time.Since(ewsc.lastPongTime) < maxSilence
}

// RecordMessageSent records outbound message statistics
func (ewsc *EnhancedWebSocketConn) RecordMessageSent(size int) {
	ewsc.mu.Lock()
	defer ewsc.mu.Unlock()
	
	ewsc.messagesSent++
	ewsc.bytesSent += uint64(size)
}

// RecordMessageReceived records inbound message statistics
func (ewsc *EnhancedWebSocketConn) RecordMessageReceived(size int) {
	ewsc.mu.Lock()
	defer ewsc.mu.Unlock()
	
	ewsc.messagesReceived++
	ewsc.bytesReceived += uint64(size)
}

// GetStats returns connection statistics
func (ewsc *EnhancedWebSocketConn) GetStats() map[string]interface{} {
	ewsc.mu.RLock()
	defer ewsc.mu.RUnlock()
	
	return map[string]interface{}{
		"messages_sent":     ewsc.messagesSent,
		"messages_received": ewsc.messagesReceived,
		"bytes_sent":        ewsc.bytesSent,
		"bytes_received":    ewsc.bytesReceived,
		"last_pong":         time.Since(ewsc.lastPongTime),
		"consecutive_fails": ewsc.consecutiveFails,
		"alive":             ewsc.IsConnectionAlive(),
	}
}

// ConnectionMonitor monitors connection health and triggers recovery
type ConnectionMonitor struct {
	mu sync.RWMutex
	
	lastActivity      time.Time
	consecutiveErrors int
	totalErrors       int
	totalSuccess      int
	
	onRecovery func()
	
	errorThreshold int
	silenceTimeout time.Duration
}

// NewConnectionMonitor creates a connection monitor
func NewConnectionMonitor(errorThreshold int, silenceTimeout time.Duration, onRecovery func()) *ConnectionMonitor {
	return &ConnectionMonitor{
		lastActivity:   time.Now(),
		errorThreshold: errorThreshold,
		silenceTimeout: silenceTimeout,
		onRecovery:     onRecovery,
	}
}

// RecordActivity updates last activity timestamp
func (cm *ConnectionMonitor) RecordActivity() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	cm.lastActivity = time.Now()
	cm.consecutiveErrors = 0
	cm.totalSuccess++
}

// RecordError records an error occurrence
func (cm *ConnectionMonitor) RecordError() bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	cm.consecutiveErrors++
	cm.totalErrors++
	
	// Trigger recovery if threshold exceeded
	if cm.consecutiveErrors >= cm.errorThreshold {
		utils.Debugf("[CONN_MONITOR] error threshold reached: %d/%d", cm.consecutiveErrors, cm.errorThreshold)
		if cm.onRecovery != nil {
			go cm.onRecovery()
		}
		cm.consecutiveErrors = 0
		return true
	}
	
	return false
}

// IsSilent checks if connection has been silent too long
func (cm *ConnectionMonitor) IsSilent() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	
	return time.Since(cm.lastActivity) > cm.silenceTimeout
}

// GetHealth returns health ratio (0.0 to 1.0)
func (cm *ConnectionMonitor) GetHealth() float64 {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	
	total := cm.totalSuccess + cm.totalErrors
	if total == 0 {
		return 1.0
	}
	
	return float64(cm.totalSuccess) / float64(total)
}

// Reset resets the monitor state
func (cm *ConnectionMonitor) Reset() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	cm.lastActivity = time.Now()
	cm.consecutiveErrors = 0
}

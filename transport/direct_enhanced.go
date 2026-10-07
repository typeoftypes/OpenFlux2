package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/p1neappleXpress/OpenFlux/utils"
)

// EnhancedDirectConfig extends DirectConfig with stability improvements
type EnhancedDirectConfig struct {
	DirectConfig

	// Connection pooling
	EnableConnectionPool bool
	PoolSize             int
	PoolIdleTimeout      time.Duration

	// TCP tuning
	TCPNoDelay     bool
	SendBufferSize int
	RecvBufferSize int

	// Keepalive tuning
	TCPKeepAlive       bool
	TCPKeepAlivePeriod time.Duration
	TCPKeepAliveCount  int
	TCPKeepAliveIdle   time.Duration

	// Stability features
	EnableCircuitBreaker bool
	EnableHealthCheck    bool
	DeadConnDetection    bool
	AutoReconnect        bool
}

// DefaultEnhancedDirectConfig returns optimized configuration
func DefaultEnhancedDirectConfig() EnhancedDirectConfig {
	return EnhancedDirectConfig{
		DirectConfig:         DefaultDirectConfig(),
		EnableConnectionPool: false,
		PoolSize:             3,
		PoolIdleTimeout:      5 * time.Minute,
		TCPNoDelay:           true,
		SendBufferSize:       1024 * 1024,
		RecvBufferSize:       1024 * 1024,
		TCPKeepAlive:         true,
		TCPKeepAlivePeriod:   30 * time.Second,
		TCPKeepAliveCount:    9,
		TCPKeepAliveIdle:     30 * time.Second,
		EnableCircuitBreaker: true,
		EnableHealthCheck:    true,
		DeadConnDetection:    true,
		AutoReconnect:        true,
	}
}

// ConnectionPool manages a pool of connections for reliability
type ConnectionPool struct {
	mu          sync.RWMutex
	connections []net.Conn
	available   []bool
	config      EnhancedDirectConfig
	dialAddr    string
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewConnectionPool creates a new connection pool
func NewConnectionPool(dialAddr string, config EnhancedDirectConfig) *ConnectionPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &ConnectionPool{
		connections: make([]net.Conn, 0, config.PoolSize),
		available:   make([]bool, 0, config.PoolSize),
		config:      config,
		dialAddr:    dialAddr,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Get retrieves an available connection from the pool
func (cp *ConnectionPool) Get() (net.Conn, error) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	// Try to find available connection
	for i, avail := range cp.available {
		if avail && cp.connections[i] != nil {
			// Test if connection is alive
			if cp.isConnAlive(cp.connections[i]) {
				cp.available[i] = false
				return cp.connections[i], nil
			}
			// Dead connection, remove it
			cp.connections[i].Close()
			cp.connections[i] = nil
		}
	}

	// Create new connection if pool not full
	if len(cp.connections) < cp.config.PoolSize {
		conn, err := cp.dialWithRetry()
		if err != nil {
			return nil, err
		}
		cp.connections = append(cp.connections, conn)
		cp.available = append(cp.available, false)
		return conn, nil
	}

	// Pool is full and no available connections
	return nil, fmt.Errorf("connection pool exhausted")
}

// Release returns a connection to the pool
func (cp *ConnectionPool) Release(conn net.Conn) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	for i, c := range cp.connections {
		if c == conn {
			cp.available[i] = true
			return
		}
	}
}

// Close closes all connections in the pool
func (cp *ConnectionPool) Close() {
	cp.cancel()
	cp.mu.Lock()
	defer cp.mu.Unlock()

	for _, conn := range cp.connections {
		if conn != nil {
			conn.Close()
		}
	}
	cp.connections = cp.connections[:0]
	cp.available = cp.available[:0]
}

func (cp *ConnectionPool) dialWithRetry() (net.Conn, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		dialer := &net.Dialer{
			Timeout:   cp.config.HandshakeTimeout,
			KeepAlive: cp.config.TCPKeepAlivePeriod,
		}

		conn, err := dialer.DialContext(cp.ctx, "tcp", cp.dialAddr)
		if err != nil {
			lastErr = err
			backoff := RandomBackoff(attempt, 100*time.Millisecond, 2*time.Second, 2.0)
			select {
			case <-cp.ctx.Done():
				return nil, cp.ctx.Err()
			case <-time.After(backoff):
				continue
			}
		}

		if cp.config.TCPNoDelay {
			if tcpConn, ok := conn.(*net.TCPConn); ok {
				tcpConn.SetNoDelay(true)
			}
		}

		return conn, nil
	}

	return nil, lastErr
}

func (cp *ConnectionPool) isConnAlive(conn net.Conn) bool {
	if conn == nil {
		return false
	}

	// Set a very short deadline to test connection
	conn.SetReadDeadline(time.Now().Add(1 * time.Millisecond))
	one := make([]byte, 1)
	_, err := conn.Read(one)
	conn.SetReadDeadline(time.Time{}) // Clear deadline

	// If we got EAGAIN/EWOULDBLOCK, connection is alive
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}

	// EOF or other errors mean connection is dead
	return err == nil
}

// DeadConnectionDetector monitors for dead connections
type DeadConnectionDetector struct {
	mu              sync.RWMutex
	lastActivity    time.Time
	activityTimeout time.Duration
	checkInterval   time.Duration
	onDeadCallback  func()
	ctx             context.Context
	cancel          context.CancelFunc
}

// NewDeadConnectionDetector creates a detector
func NewDeadConnectionDetector(timeout time.Duration, callback func()) *DeadConnectionDetector {
	ctx, cancel := context.WithCancel(context.Background())
	dcd := &DeadConnectionDetector{
		lastActivity:    time.Now(),
		activityTimeout: timeout,
		checkInterval:   timeout / 3,
		onDeadCallback:  callback,
		ctx:             ctx,
		cancel:          cancel,
	}
	return dcd
}

// Start begins monitoring
func (dcd *DeadConnectionDetector) Start() {
	go dcd.monitorLoop()
}

// Stop stops monitoring
func (dcd *DeadConnectionDetector) Stop() {
	dcd.cancel()
}

// RecordActivity updates last activity timestamp
func (dcd *DeadConnectionDetector) RecordActivity() {
	dcd.mu.Lock()
	dcd.lastActivity = time.Now()
	dcd.mu.Unlock()
}

func (dcd *DeadConnectionDetector) monitorLoop() {
	ticker := time.NewTicker(dcd.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-dcd.ctx.Done():
			return
		case <-ticker.C:
			dcd.mu.RLock()
			elapsed := time.Since(dcd.lastActivity)
			dcd.mu.RUnlock()

			if elapsed > dcd.activityTimeout {
				utils.Debugf("[DEAD_CONN] connection appears dead, elapsed: %v", elapsed)
				if dcd.onDeadCallback != nil {
					dcd.onDeadCallback()
				}
				return
			}
		}
	}
}

// SmartReconnector handles intelligent reconnection with backoff
type SmartReconnector struct {
	mu                  sync.Mutex
	attempt             int
	minDelay            time.Duration
	maxDelay            time.Duration
	multiplier          float64
	jitterPercent       int
	lastAttempt         time.Time
	consecutiveFails    int
	maxConsecutiveFails int
	circuitBreaker      *CircuitBreaker
}

// NewSmartReconnector creates a smart reconnector
func NewSmartReconnector(minDelay, maxDelay time.Duration, multiplier float64, cb *CircuitBreaker) *SmartReconnector {
	return &SmartReconnector{
		minDelay:            minDelay,
		maxDelay:            maxDelay,
		multiplier:          multiplier,
		jitterPercent:       25,
		maxConsecutiveFails: 10,
		circuitBreaker:      cb,
	}
}

// ShouldReconnect determines if reconnection should be attempted
func (sr *SmartReconnector) ShouldReconnect() (bool, time.Duration) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	// Check circuit breaker
	if sr.circuitBreaker != nil && !sr.circuitBreaker.CanAttempt() {
		return false, sr.maxDelay
	}

	// Too many consecutive failures, back off more
	if sr.consecutiveFails >= sr.maxConsecutiveFails {
		return false, sr.maxDelay * 2
	}

	delay := RandomBackoff(sr.attempt, sr.minDelay, sr.maxDelay, sr.multiplier)

	// Respect minimum time between attempts
	if time.Since(sr.lastAttempt) < delay {
		return false, delay
	}

	return true, delay
}

// RecordAttempt records a reconnection attempt
func (sr *SmartReconnector) RecordAttempt() {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	sr.attempt++
	sr.lastAttempt = time.Now()
	sr.consecutiveFails++
}

// RecordSuccess resets the reconnection state
func (sr *SmartReconnector) RecordSuccess() {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	sr.attempt = 0
	sr.consecutiveFails = 0

	if sr.circuitBreaker != nil {
		sr.circuitBreaker.RecordSuccess()
	}
}

// RecordFailure records a connection failure
func (sr *SmartReconnector) RecordFailure() {
	if sr.circuitBreaker != nil {
		sr.circuitBreaker.RecordFailure()
	}
}

// EnhancedTCPDialer provides advanced TCP dialing with options
type EnhancedTCPDialer struct {
	config EnhancedDirectConfig
}

// NewEnhancedTCPDialer creates an enhanced dialer
func NewEnhancedTCPDialer(config EnhancedDirectConfig) *EnhancedTCPDialer {
	return &EnhancedTCPDialer{config: config}
}

// Dial creates an optimized TCP connection
func (etd *EnhancedTCPDialer) Dial(ctx context.Context, address string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   etd.config.HandshakeTimeout,
		KeepAlive: etd.config.TCPKeepAlivePeriod,
	}

	// No artificial dial jitter: minimize connection setup latency.
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}

	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return conn, nil
	}

	// Apply TCP optimizations
	if etd.config.TCPNoDelay {
		if err := tcpConn.SetNoDelay(true); err != nil {
			utils.Debugf("[TCP] SetNoDelay failed: %v", err)
		}
	}

	if etd.config.SendBufferSize > 0 {
		if err := tcpConn.SetWriteBuffer(etd.config.SendBufferSize); err != nil {
			utils.Debugf("[TCP] SetWriteBuffer failed: %v", err)
		}
	}

	if etd.config.RecvBufferSize > 0 {
		if err := tcpConn.SetReadBuffer(etd.config.RecvBufferSize); err != nil {
			utils.Debugf("[TCP] SetReadBuffer failed: %v", err)
		}
	}

	if etd.config.TCPKeepAlive {
		if err := tcpConn.SetKeepAlive(true); err != nil {
			utils.Debugf("[TCP] SetKeepAlive failed: %v", err)
		}
		if err := tcpConn.SetKeepAlivePeriod(etd.config.TCPKeepAlivePeriod); err != nil {
			utils.Debugf("[TCP] SetKeepAlivePeriod failed: %v", err)
		}
	}

	return tcpConn, nil
}

// SafeReader wraps a reader with timeout and dead connection detection
type SafeReader struct {
	reader   io.Reader
	timeout  time.Duration
	conn     net.Conn
	detector *DeadConnectionDetector
}

// NewSafeReader creates a safe reader
func NewSafeReader(reader io.Reader, conn net.Conn, timeout time.Duration, detector *DeadConnectionDetector) *SafeReader {
	return &SafeReader{
		reader:   reader,
		timeout:  timeout,
		conn:     conn,
		detector: detector,
	}
}

// Read implements io.Reader with safety checks
func (sr *SafeReader) Read(p []byte) (int, error) {
	if sr.timeout > 0 && sr.conn != nil {
		sr.conn.SetReadDeadline(time.Now().Add(sr.timeout))
	}

	n, err := sr.reader.Read(p)

	if n > 0 && sr.detector != nil {
		sr.detector.RecordActivity()
	}

	return n, err
}

// PacketRateLimiter controls packet send rate to avoid bursts
type PacketRateLimiter struct {
	mu           sync.Mutex
	lastSend     time.Time
	minInterval  time.Duration
	burstSize    int
	burstCounter int
}

// NewPacketRateLimiter creates a rate limiter
func NewPacketRateLimiter(minInterval time.Duration, burstSize int) *PacketRateLimiter {
	return &PacketRateLimiter{
		lastSend:    time.Now(),
		minInterval: minInterval,
		burstSize:   burstSize,
	}
}

// Wait blocks until it's safe to send the next packet
func (prl *PacketRateLimiter) Wait() {
	prl.mu.Lock()
	defer prl.mu.Unlock()

	prl.burstCounter++
	if prl.burstCounter >= prl.burstSize {
		prl.burstCounter = 0
		elapsed := time.Since(prl.lastSend)
		if elapsed < prl.minInterval {
			time.Sleep(prl.minInterval - elapsed)
		}
	}

	prl.lastSend = time.Now()
}

// Reset resets the rate limiter
func (prl *PacketRateLimiter) Reset() {
	prl.mu.Lock()
	defer prl.mu.Unlock()
	prl.burstCounter = 0
	prl.lastSend = time.Now()
}

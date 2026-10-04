package transport

import (
	"crypto/rand"
	"math"
	"math/big"
	"time"
)

// StealthConfig contains advanced stealth and masking parameters
type StealthConfig struct {
	// Timing randomization to avoid traffic analysis
	JitterMin        time.Duration
	JitterMax        time.Duration
	EnableJitter     bool
	
	// Packet size padding to hide actual payload sizes
	EnablePadding    bool
	MinPaddingBytes  int
	MaxPaddingBytes  int
	
	// Traffic shaping to mimic normal browsing patterns
	EnableShaping    bool
	BurstSize        int
	BurstInterval    time.Duration
	
	// Randomize packet intervals
	InterPacketDelay time.Duration
	RandomizeDelay   bool
	
	// Connection fingerprint randomization
	RandomizeFingerprint bool
}

// DefaultStealthConfig returns production stealth settings
func DefaultStealthConfig() StealthConfig {
	return StealthConfig{
		JitterMin:            5 * time.Millisecond,
		JitterMax:            50 * time.Millisecond,
		EnableJitter:         true,
		EnablePadding:        true,
		MinPaddingBytes:      8,
		MaxPaddingBytes:      256,
		EnableShaping:        true,
		BurstSize:            5,
		BurstInterval:        200 * time.Millisecond,
		InterPacketDelay:     10 * time.Millisecond,
		RandomizeDelay:       true,
		RandomizeFingerprint: true,
	}
}

// ApplyJitter adds random timing jitter to avoid pattern detection
func (sc *StealthConfig) ApplyJitter() time.Duration {
	if !sc.EnableJitter {
		return 0
	}
	
	minNs := sc.JitterMin.Nanoseconds()
	maxNs := sc.JitterMax.Nanoseconds()
	if maxNs <= minNs {
		return sc.JitterMin
	}
	
	diff := maxNs - minNs
	n, err := rand.Int(rand.Reader, big.NewInt(diff))
	if err != nil {
		return sc.JitterMin
	}
	
	return time.Duration(minNs + n.Int64())
}

// CalculatePadding returns random padding size to obscure actual packet size
func (sc *StealthConfig) CalculatePadding(actualSize int) int {
	if !sc.EnablePadding {
		return 0
	}
	
	diff := sc.MaxPaddingBytes - sc.MinPaddingBytes
	if diff <= 0 {
		return sc.MinPaddingBytes
	}
	
	n, err := rand.Int(rand.Reader, big.NewInt(int64(diff)))
	if err != nil {
		return sc.MinPaddingBytes
	}
	
	return sc.MinPaddingBytes + int(n.Int64())
}

// GetInterPacketDelay returns randomized delay between packets
func (sc *StealthConfig) GetInterPacketDelay() time.Duration {
	if !sc.RandomizeDelay {
		return sc.InterPacketDelay
	}
	
	baseNs := sc.InterPacketDelay.Nanoseconds()
	variation := baseNs / 2 // ±50% variation
	
	n, err := rand.Int(rand.Reader, big.NewInt(variation*2))
	if err != nil {
		return sc.InterPacketDelay
	}
	
	jitter := n.Int64() - variation
	return time.Duration(baseNs + jitter)
}

// ShouldThrottle determines if traffic shaping should apply
func (sc *StealthConfig) ShouldThrottle(packetsSent int) (bool, time.Duration) {
	if !sc.EnableShaping {
		return false, 0
	}
	
	if packetsSent > 0 && packetsSent%sc.BurstSize == 0 {
		return true, sc.BurstInterval + sc.ApplyJitter()
	}
	
	return false, 0
}

// AddPadding adds random padding to data to obscure size patterns
func (sc *StealthConfig) AddPadding(data []byte) []byte {
	if !sc.EnablePadding {
		return data
	}
	
	paddingSize := sc.CalculatePadding(len(data))
	if paddingSize == 0 {
		return data
	}
	
	// Create padded buffer with length prefix
	padded := make([]byte, len(data)+paddingSize+2)
	
	// Store original length (big-endian)
	padded[0] = byte(len(data) >> 8)
	padded[1] = byte(len(data))
	
	// Copy actual data
	copy(padded[2:], data)
	
	// Fill padding with random bytes
	if paddingSize > 0 {
		padding := make([]byte, paddingSize)
		rand.Read(padding)
		copy(padded[2+len(data):], padding)
	}
	
	return padded
}

// RemovePadding extracts original data from padded packet
func (sc *StealthConfig) RemovePadding(padded []byte) ([]byte, error) {
	if !sc.EnablePadding || len(padded) < 2 {
		return padded, nil
	}
	
	// Extract original length
	origLen := int(padded[0])<<8 | int(padded[1])
	
	if origLen+2 > len(padded) {
		// Not a padded packet, return as-is
		return padded, nil
	}
	
	return padded[2 : 2+origLen], nil
}

// RandomBackoff calculates exponential backoff with jitter
func RandomBackoff(attempt int, min, max time.Duration, multiplier float64) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	
	// Calculate exponential delay: min * multiplier^attempt
	delay := float64(min) * math.Pow(multiplier, float64(attempt))
	
	// Cap at max
	if delay > float64(max) {
		delay = float64(max)
	}
	
	// Add ±25% random jitter
	jitter := delay * 0.25
	n, err := rand.Int(rand.Reader, big.NewInt(int64(jitter*2)))
	if err != nil {
		return time.Duration(delay)
	}
	
	finalDelay := delay + float64(n.Int64()) - jitter
	if finalDelay < float64(min) {
		finalDelay = float64(min)
	}
	
	return time.Duration(finalDelay)
}

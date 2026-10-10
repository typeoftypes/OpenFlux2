package transport

import (
	"crypto/rand"
	"math"
	"math/big"
	"time"
)

// StealthConfig contains advanced stealth and masking parameters aimed at
// making carrier traffic resemble a person using a website (page loads,
// XHR, idle reading) rather than a constant-bitrate tunnel.
type StealthConfig struct {
	EnableJitter     bool
	EnablePadding    bool
	EnableShaping    bool
	MinJitter        time.Duration
	MaxJitter        time.Duration
	MinPadding       int
	MaxPadding       int
	// PadToBuckets aligns sizes to common web-resource sizes (HTML/JSON/img).
	PadToBuckets     bool
	InterPacketDelay time.Duration
	BurstSize        int
	BurstInterval    time.Duration
	// BrowseMode enables human-like phase delays (preferred over fixed InterPacketDelay).
	BrowseMode bool
}

// DefaultStealthConfig returns production stealth settings tuned for
// "looks like browsing", not max throughput.
func DefaultStealthConfig() StealthConfig {
	return StealthConfig{
		EnableJitter:     true,
		EnablePadding:    true,
		EnableShaping:    true,
		MinJitter:        8 * time.Millisecond,
		MaxJitter:        120 * time.Millisecond,
		MinPadding:       16,
		MaxPadding:       512,
		PadToBuckets:     true,
		InterPacketDelay: 40 * time.Millisecond,
		BurstSize:        7, // small "asset waterfall" then pause
		BurstInterval:    600 * time.Millisecond,
		BrowseMode:       true,
	}
}

// LowMemoryStealthConfig is tighter for phones (less padding, shorter pauses).
func LowMemoryStealthConfig() StealthConfig {
	sc := DefaultStealthConfig()
	sc.MaxPadding = 128
	sc.MaxJitter = 60 * time.Millisecond
	sc.BurstInterval = 350 * time.Millisecond
	return sc
}

// ApplyJitter returns a random sleep in [MinJitter, MaxJitter].
func (sc *StealthConfig) ApplyJitter() time.Duration {
	if !sc.EnableJitter {
		return 0
	}
	min := sc.MinJitter
	max := sc.MaxJitter
	if max <= min {
		return min
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max-min)))
	if err != nil {
		return min
	}
	return min + time.Duration(n.Int64())
}

// webSizeBuckets are typical uncompressed body sizes for web traffic so
// packet length histograms do not show a flat tunnel MTU.
var webSizeBuckets = []int{
	128, 256, 384, 512, 768, 1024, 1536, 2048, 3072, 4096,
	6144, 8192, 12288, 16384,
}

// CalculatePadding returns how many padding bytes to append.
func (sc *StealthConfig) CalculatePadding(actualSize int) int {
	if !sc.EnablePadding {
		return 0
	}
	if sc.PadToBuckets {
		for _, b := range webSizeBuckets {
			if b > actualSize {
				pad := b - actualSize
				if pad > sc.MaxPadding {
					return sc.MaxPadding
				}
				if pad < sc.MinPadding && b != webSizeBuckets[0] {
					continue
				}
				return pad
			}
		}
	}
	span := sc.MaxPadding - sc.MinPadding
	if span <= 0 {
		return sc.MinPadding
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(span+1)))
	if err != nil {
		return sc.MinPadding
	}
	return sc.MinPadding + int(n.Int64())
}

// GetInterPacketDelay returns a delay between sends (±50% around InterPacketDelay).
func (sc *StealthConfig) GetInterPacketDelay() time.Duration {
	if sc.InterPacketDelay <= 0 {
		return sc.ApplyJitter()
	}
	baseNs := sc.InterPacketDelay.Nanoseconds()
	variation := baseNs / 2
	if variation <= 0 {
		return sc.InterPacketDelay
	}
	n, err := rand.Int(rand.Reader, big.NewInt(variation*2))
	if err != nil {
		return sc.InterPacketDelay
	}
	jitter := n.Int64() - variation
	return time.Duration(baseNs + jitter)
}

// ShouldThrottle returns true after every BurstSize packets (page-load style pause).
func (sc *StealthConfig) ShouldThrottle(packetsSent int) (bool, time.Duration) {
	if !sc.EnableShaping || sc.BurstSize <= 0 {
		return false, 0
	}
	if packetsSent > 0 && packetsSent%sc.BurstSize == 0 {
		return true, sc.BurstInterval + sc.ApplyJitter()
	}
	return false, 0
}

// AddPadding adds random padding to data to obscure size patterns.
// Layout: 2-byte big-endian original length + data + random pad.
func (sc *StealthConfig) AddPadding(data []byte) []byte {
	if !sc.EnablePadding {
		return data
	}
	paddingSize := sc.CalculatePadding(len(data))
	if paddingSize == 0 {
		return data
	}
	padded := make([]byte, len(data)+paddingSize+2)
	padded[0] = byte(len(data) >> 8)
	padded[1] = byte(len(data))
	copy(padded[2:], data)
	if paddingSize > 0 {
		_, _ = rand.Read(padded[2+len(data):])
	}
	return padded
}

// RemovePadding extracts original data from a padded packet.
func (sc *StealthConfig) RemovePadding(padded []byte) ([]byte, error) {
	if !sc.EnablePadding || len(padded) < 2 {
		return padded, nil
	}
	origLen := int(padded[0])<<8 | int(padded[1])
	if origLen+2 > len(padded) {
		return padded, nil
	}
	return padded[2 : 2+origLen], nil
}

// RandomBackoff calculates exponential backoff with jitter.
func RandomBackoff(attempt int, min, max time.Duration, multiplier float64) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	delay := float64(min) * math.Pow(multiplier, float64(attempt))
	if delay > float64(max) {
		delay = float64(max)
	}
	jitter := delay * 0.25
	n, err := rand.Int(rand.Reader, big.NewInt(int64(jitter*2)+1))
	if err != nil {
		return time.Duration(delay)
	}
	finalDelay := delay + float64(n.Int64()) - jitter
	if finalDelay < float64(min) {
		finalDelay = float64(min)
	}
	return time.Duration(finalDelay)
}

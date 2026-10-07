package transport

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"time"
)

// BrowserProfile contains realistic browser fingerprint data
type BrowserProfile struct {
	UserAgent      string
	AcceptLanguage string
	Accept         string
	AcceptEncoding string
	SecChUa        string
	SecChUaMobile  string
	SecChUaPlatform string
	SecFetchDest   string
	SecFetchMode   string
	SecFetchSite   string
	DNT            string
	UpgradeInsecureRequests string
}

// GetRandomBrowserProfile returns a random realistic browser profile
func GetRandomBrowserProfile() BrowserProfile {
	profiles := []BrowserProfile{
		// Chrome 131 on Windows 11 (2026)
		{
			UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
			AcceptLanguage: "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7",
			Accept:         "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
			AcceptEncoding: "gzip, deflate, br, zstd",
			SecChUa:        `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
			SecChUaMobile:  "?0",
			SecChUaPlatform: `"Windows"`,
			SecFetchDest:   "document",
			SecFetchMode:   "navigate",
			SecFetchSite:   "none",
			DNT:            "1",
			UpgradeInsecureRequests: "1",
		},
		// Chrome 131 on Android
		{
			UserAgent:      "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36",
			AcceptLanguage: "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7",
			Accept:         "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
			AcceptEncoding: "gzip, deflate, br, zstd",
			SecChUa:        `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
			SecChUaMobile:  "?1",
			SecChUaPlatform: `"Android"`,
			SecFetchDest:   "document",
			SecFetchMode:   "navigate",
			SecFetchSite:   "none",
			DNT:            "1",
			UpgradeInsecureRequests: "1",
		},
		// Firefox 133 on Linux
		{
			UserAgent:      "Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0",
			AcceptLanguage: "en-US,en;q=0.5",
			Accept:         "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
			AcceptEncoding: "gzip, deflate, br, zstd",
			SecChUa:        "",
			SecChUaMobile:  "",
			SecChUaPlatform: "",
			SecFetchDest:   "document",
			SecFetchMode:   "navigate",
			SecFetchSite:   "none",
			DNT:            "1",
			UpgradeInsecureRequests: "1",
		},
		// Chrome 129 on Windows 11
		{
			UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
			AcceptLanguage: "en-US,en;q=0.9,ru;q=0.8",
			Accept:         "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
			AcceptEncoding: "gzip, deflate, br, zstd",
			SecChUa:        `"Google Chrome";v="129", "Not=A?Brand";v="8", "Chromium";v="129"`,
			SecChUaMobile:  "?0",
			SecChUaPlatform: `"Windows"`,
			SecFetchDest:   "document",
			SecFetchMode:   "navigate",
			SecFetchSite:   "none",
			DNT:            "1",
			UpgradeInsecureRequests: "1",
		},
		// Chrome 128 on macOS
		{
			UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
			AcceptLanguage: "en-US,en;q=0.9",
			Accept:         "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
			AcceptEncoding: "gzip, deflate, br",
			SecChUa:        `"Chromium";v="128", "Not;A=Brand";v="24", "Google Chrome";v="128"`,
			SecChUaMobile:  "?0",
			SecChUaPlatform: `"macOS"`,
			SecFetchDest:   "document",
			SecFetchMode:   "navigate",
			SecFetchSite:   "none",
			DNT:            "1",
			UpgradeInsecureRequests: "1",
		},
		// Firefox 130 on Windows
		{
			UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:130.0) Gecko/20100101 Firefox/130.0",
			AcceptLanguage: "en-US,en;q=0.5",
			Accept:         "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
			AcceptEncoding: "gzip, deflate, br, zstd",
			SecChUa:        "",
			SecChUaMobile:  "",
			SecChUaPlatform: "",
			SecFetchDest:   "document",
			SecFetchMode:   "navigate",
			SecFetchSite:   "none",
			DNT:            "1",
			UpgradeInsecureRequests: "1",
		},
		// Safari 17 on macOS
		{
			UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Safari/605.1.15",
			AcceptLanguage: "en-US,en;q=0.9",
			Accept:         "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			AcceptEncoding: "gzip, deflate, br",
			SecChUa:        "",
			SecChUaMobile:  "",
			SecChUaPlatform: "",
			SecFetchDest:   "document",
			SecFetchMode:   "navigate",
			SecFetchSite:   "none",
			DNT:            "",
			UpgradeInsecureRequests: "1",
		},
		// Edge 129 on Windows
		{
			UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 Edg/129.0.0.0",
			AcceptLanguage: "en-US,en;q=0.9",
			Accept:         "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
			AcceptEncoding: "gzip, deflate, br, zstd",
			SecChUa:        `"Microsoft Edge";v="129", "Not=A?Brand";v="8", "Chromium";v="129"`,
			SecChUaMobile:  "?0",
			SecChUaPlatform: `"Windows"`,
			SecFetchDest:   "document",
			SecFetchMode:   "navigate",
			SecFetchSite:   "none",
			DNT:            "1",
			UpgradeInsecureRequests: "1",
		},
		// Chrome 129 on Linux
		{
			UserAgent:      "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
			AcceptLanguage: "en-US,en;q=0.9",
			Accept:         "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
			AcceptEncoding: "gzip, deflate, br",
			SecChUa:        `"Google Chrome";v="129", "Not=A?Brand";v="8", "Chromium";v="129"`,
			SecChUaMobile:  "?0",
			SecChUaPlatform: `"Linux"`,
			SecFetchDest:   "document",
			SecFetchMode:   "navigate",
			SecFetchSite:   "none",
			DNT:            "1",
			UpgradeInsecureRequests: "1",
		},
	}
	
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(profiles))))
	if err != nil {
		return profiles[0]
	}
	
	return profiles[int(n.Int64())]
}

// ApplyBrowserHeaders applies realistic browser headers to HTTP request
func ApplyBrowserHeaders(req *http.Request, profile BrowserProfile) {
	req.Header.Set("User-Agent", profile.UserAgent)
	req.Header.Set("Accept", profile.Accept)
	req.Header.Set("Accept-Language", profile.AcceptLanguage)
	req.Header.Set("Accept-Encoding", profile.AcceptEncoding)
	
	if profile.SecChUa != "" {
		req.Header.Set("Sec-Ch-Ua", profile.SecChUa)
	}
	if profile.SecChUaMobile != "" {
		req.Header.Set("Sec-Ch-Ua-Mobile", profile.SecChUaMobile)
	}
	if profile.SecChUaPlatform != "" {
		req.Header.Set("Sec-Ch-Ua-Platform", profile.SecChUaPlatform)
	}
	
	req.Header.Set("Sec-Fetch-Dest", profile.SecFetchDest)
	req.Header.Set("Sec-Fetch-Mode", profile.SecFetchMode)
	req.Header.Set("Sec-Fetch-Site", profile.SecFetchSite)
	
	if profile.DNT != "" {
		req.Header.Set("DNT", profile.DNT)
	}
	if profile.UpgradeInsecureRequests != "" {
		req.Header.Set("Upgrade-Insecure-Requests", profile.UpgradeInsecureRequests)
	}
	
	// Add cache control to mimic real browser
	req.Header.Set("Cache-Control", "max-age=0")
	
	// Add realistic connection header
	req.Header.Set("Connection", "keep-alive")
}

// GetRealisticReferer generates a realistic referer based on target URL
func GetRealisticReferer(targetURL string) string {
	referers := []string{
		"https://www.google.com/",
		"https://www.bing.com/",
		"https://yandex.ru/",
		"https://duckduckgo.com/",
		"",
	}
	
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(referers))))
	if err != nil {
		return ""
	}
	
	return referers[int(n.Int64())]
}

// WebSocketUpgradeHeaders returns realistic WebSocket upgrade headers
func WebSocketUpgradeHeaders() http.Header {
	profile := GetRandomBrowserProfile()
	headers := make(http.Header)
	
	headers.Set("User-Agent", profile.UserAgent)
	headers.Set("Accept-Encoding", profile.AcceptEncoding)
	headers.Set("Accept-Language", profile.AcceptLanguage)
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Pragma", "no-cache")
	
	if profile.SecChUa != "" {
		headers.Set("Sec-Ch-Ua", profile.SecChUa)
		headers.Set("Sec-Ch-Ua-Mobile", profile.SecChUaMobile)
		headers.Set("Sec-Ch-Ua-Platform", profile.SecChUaPlatform)
	}
	
	headers.Set("Sec-WebSocket-Extensions", "permessage-deflate; client_max_window_bits")
	headers.Set("Sec-WebSocket-Version", "13")
	
	return headers
}

// TimingProfile simulates realistic user timing behavior
type TimingProfile struct {
	ThinkTime     time.Duration
	TypingSpeed   time.Duration
	MouseMovement time.Duration
	PageLoadWait  time.Duration
}

// GetRealisticTimingProfile returns human-like timing patterns
func GetRealisticTimingProfile() TimingProfile {
	profiles := []TimingProfile{
		// Fast user
		{
			ThinkTime:     500 * time.Millisecond,
			TypingSpeed:   100 * time.Millisecond,
			MouseMovement: 50 * time.Millisecond,
			PageLoadWait:  1 * time.Second,
		},
		// Average user
		{
			ThinkTime:     2 * time.Second,
			TypingSpeed:   200 * time.Millisecond,
			MouseMovement: 150 * time.Millisecond,
			PageLoadWait:  3 * time.Second,
		},
		// Slow user
		{
			ThinkTime:     5 * time.Second,
			TypingSpeed:   300 * time.Millisecond,
			MouseMovement: 250 * time.Millisecond,
			PageLoadWait:  5 * time.Second,
		},
	}
	
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(profiles))))
	if err != nil {
		return profiles[1]
	}
	
	return profiles[int(n.Int64())]
}

// GenerateRandomDelay creates a realistic random delay with jitter
func GenerateRandomDelay(base time.Duration, jitterPercent int) time.Duration {
	if jitterPercent <= 0 {
		return base
	}
	
	maxJitter := int64(base) * int64(jitterPercent) / 100
	n, err := rand.Int(rand.Reader, big.NewInt(maxJitter*2))
	if err != nil {
		return base
	}
	
	jitter := n.Int64() - maxJitter
	result := int64(base) + jitter
	if result < 0 {
		result = int64(base)
	}
	
	return time.Duration(result)
}

// MimicBrowserBehavior adds realistic delays to mimic human behavior
func MimicBrowserBehavior(action string) {
	profile := GetRealisticTimingProfile()
	
	var delay time.Duration
	switch action {
	case "think":
		delay = GenerateRandomDelay(profile.ThinkTime, 50)
	case "type":
		delay = GenerateRandomDelay(profile.TypingSpeed, 30)
	case "mouse":
		delay = GenerateRandomDelay(profile.MouseMovement, 40)
	case "page_load":
		delay = GenerateRandomDelay(profile.PageLoadWait, 60)
	default:
		delay = GenerateRandomDelay(500*time.Millisecond, 50)
	}
	
	time.Sleep(delay)
}

// GetTLSFingerprint returns realistic TLS client hello parameters
func GetTLSFingerprint() map[string]interface{} {
	return map[string]interface{}{
		"min_version": "TLS 1.2",
		"max_version": "TLS 1.3",
		"cipher_suites": []string{
			"TLS_AES_128_GCM_SHA256",
			"TLS_AES_256_GCM_SHA384",
			"TLS_CHACHA20_POLY1305_SHA256",
			"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
			"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
			"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
			"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
		},
		"curves": []string{
			"X25519",
			"P-256",
			"P-384",
		},
		"signature_algorithms": []string{
			"ecdsa_secp256r1_sha256",
			"rsa_pss_rsae_sha256",
			"rsa_pkcs1_sha256",
			"ecdsa_secp384r1_sha384",
			"rsa_pss_rsae_sha384",
			"rsa_pkcs1_sha384",
			"rsa_pss_rsae_sha512",
			"rsa_pkcs1_sha512",
		},
	}
}

// HTTPClientOptions returns realistic HTTP client configuration
func HTTPClientOptions() map[string]interface{} {
	return map[string]interface{}{
		"timeout":                   30 * time.Second,
		"keepalive":                 true,
		"keepalive_timeout":         90 * time.Second,
		"max_idle_conns":            100,
		"max_idle_conns_per_host":   10,
		"idle_conn_timeout":         90 * time.Second,
		"tls_handshake_timeout":     10 * time.Second,
		"expect_continue_timeout":   1 * time.Second,
		"disable_compression":       false,
		"disable_keep_alives":       false,
		"max_response_header_bytes": 10 * 1024 * 1024,
	}
}

// GenerateSessionID creates a realistic session ID
func GenerateSessionID() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 32)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[n.Int64()]
	}
	return string(b)
}

// GetCookieDefaults returns realistic cookie attributes
func GetCookieDefaults() map[string]string {
	return map[string]string{
		"Path":     "/",
		"Domain":   "",
		"SameSite": "Lax",
		"Secure":   "true",
		"HttpOnly": "true",
	}
}

// RandomizeOrder randomizes slice order for fingerprint resistance
func RandomizeOrder(items []string) []string {
	result := make([]string, len(items))
	copy(result, items)
	
	for i := len(result) - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			continue
		}
		j := int(n.Int64())
		result[i], result[j] = result[j], result[i]
	}
	
	return result
}

// ObfuscatePacketSize adds variable padding to hide packet size patterns
func ObfuscatePacketSize(data []byte, targetSize int) []byte {
	if len(data) >= targetSize {
		return data
	}
	
	paddingSize := targetSize - len(data)
	padding := make([]byte, paddingSize)
	rand.Read(padding)
	
	result := make([]byte, 0, targetSize)
	result = append(result, data...)
	result = append(result, padding...)
	
	return result
}

// GenerateTrafficPattern creates realistic traffic bursts
func GenerateTrafficPattern() []time.Duration {
	patterns := [][]time.Duration{
		// Web browsing pattern
		{100 * time.Millisecond, 50 * time.Millisecond, 200 * time.Millisecond, 1 * time.Second, 100 * time.Millisecond},
		// Video streaming pattern
		{50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond},
		// File download pattern
		{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond},
		// Interactive pattern
		{500 * time.Millisecond, 300 * time.Millisecond, 1 * time.Second, 200 * time.Millisecond, 800 * time.Millisecond},
	}
	
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(patterns))))
	if err != nil {
		return patterns[0]
	}
	
	return patterns[int(n.Int64())]
}

// String formats BrowserProfile for logging
func (bp BrowserProfile) String() string {
	return fmt.Sprintf("UA: %s, Platform: %s", bp.UserAgent[:50], bp.SecChUaPlatform)
}


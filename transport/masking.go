package transport

import (
	"crypto/rand"
	"fmt"
	"math/big"
	mrand "math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// BrowserProfile contains realistic browser fingerprint data for one
// "tab session". All HTTP/WS traffic from a carrier should reuse the same
// profile so middleboxes see one consistent browser, not a rotating zoo.
type BrowserProfile struct {
	UserAgent               string
	AcceptLanguage          string
	Accept                  string
	AcceptEncoding          string
	SecChUa                 string
	SecChUaMobile           string
	SecChUaPlatform         string
	SecFetchDest            string
	SecFetchMode            string
	SecFetchSite            string
	DNT                     string
	UpgradeInsecureRequests string
	// Viewport / client hints used by some CDNs.
	SecChUaFullVersionList string
	SecChViewportWidth     string
}

// RequestKind selects header sets that match how a real browser fetches
// different resources while a person sits on a site.
type RequestKind int

const (
	// KindNavigate — top-level document (first open of the page).
	KindNavigate RequestKind = iota
	// KindXHR — same-origin fetch/XHR (collaborative editor, API).
	KindXHR
	// KindAsset — CSS/JS/font-like secondary load.
	KindAsset
	// KindImage — image-like secondary load.
	KindImage
	// KindWS — WebSocket upgrade from an open document.
	KindWS
)

// BrowseSession models a person with a tab open: page load burst, then
// reading/idle, occasional clicks that trigger small request clusters.
// Transports call NextDelay / NextKind instead of fixed 20ms tickers so
// packet timing looks like browsing, not a tunnel.
type BrowseSession struct {
	mu       sync.Mutex
	profile  BrowserProfile
	phase    browsePhase
	phaseEnd time.Time
	reqN     atomic.Uint64
	rng      *mrand.Rand
}

type browsePhase int

const (
	phasePageLoad browsePhase = iota // dense small/medium requests
	phaseReading                     // long quiet gaps
	phaseInteract                    // short burst after a "click"
	phaseIdle                        // background keepalive-like
)

// NewBrowseSession starts a session with a random browser fingerprint.
func NewBrowseSession() *BrowseSession {
	src := mrand.NewSource(time.Now().UnixNano() ^ int64(mrand.Uint64()))
	bs := &BrowseSession{
		profile:  GetRandomBrowserProfile(),
		phase:    phasePageLoad,
		phaseEnd: time.Now().Add(randomDuration(800*time.Millisecond, 2500*time.Millisecond)),
		rng:      mrand.New(src),
	}
	return bs
}

// Profile returns the fixed fingerprint for this tab.
func (bs *BrowseSession) Profile() BrowserProfile {
	return bs.profile
}

// NextKind picks the next request style (document vs XHR vs asset).
func (bs *BrowseSession) NextKind() RequestKind {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.advancePhaseLocked()
	n := bs.reqN.Add(1)
	switch bs.phase {
	case phasePageLoad:
		// First few look like document + parallel assets.
		if n <= 2 {
			return KindNavigate
		}
		switch bs.rng.Intn(10) {
		case 0, 1, 2:
			return KindAsset
		case 3, 4:
			return KindImage
		default:
			return KindXHR
		}
	case phaseInteract:
		if bs.rng.Intn(5) == 0 {
			return KindAsset
		}
		return KindXHR
	case phaseReading, phaseIdle:
		return KindXHR
	default:
		return KindXHR
	}
}

// NextDelay is how long to wait before the next send, human-like.
func (bs *BrowseSession) NextDelay() time.Duration {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.advancePhaseLocked()
	switch bs.phase {
	case phasePageLoad:
		// Parallel asset waterfall: 5–80ms between chunks.
		return randomDurationLocked(bs.rng, 5*time.Millisecond, 80*time.Millisecond)
	case phaseInteract:
		// Click → API round-trips.
		return randomDurationLocked(bs.rng, 30*time.Millisecond, 220*time.Millisecond)
	case phaseReading:
		// Person reading / scrolling: 400ms–3.5s quiet.
		return randomDurationLocked(bs.rng, 400*time.Millisecond, 3500*time.Millisecond)
	case phaseIdle:
		// Tab in background: sparse heartbeats.
		return randomDurationLocked(bs.rng, 2*time.Second, 12*time.Second)
	default:
		return 100 * time.Millisecond
	}
}

// TargetPayloadSize suggests a body size distribution like HTML/JSON/images
// rather than fixed tunnel MTU chunks.
func (bs *BrowseSession) TargetPayloadSize() int {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.advancePhaseLocked()
	switch bs.phase {
	case phasePageLoad:
		// Mix of small JSON and medium "assets".
		buckets := []int{180, 420, 900, 2400, 4800, 12000}
		return buckets[bs.rng.Intn(len(buckets))]
	case phaseInteract:
		buckets := []int{120, 280, 600, 1400, 3200}
		return buckets[bs.rng.Intn(len(buckets))]
	default:
		buckets := []int{80, 200, 450, 1100}
		return buckets[bs.rng.Intn(len(buckets))]
	}
}

func (bs *BrowseSession) advancePhaseLocked() {
	now := time.Now()
	if now.Before(bs.phaseEnd) {
		return
	}
	// Markov-ish transitions: load → reading → interact ↔ reading → idle…
	switch bs.phase {
	case phasePageLoad:
		bs.phase = phaseReading
		bs.phaseEnd = now.Add(randomDurationLocked(bs.rng, 3*time.Second, 15*time.Second))
	case phaseReading:
		if bs.rng.Intn(100) < 35 {
			bs.phase = phaseInteract
			bs.phaseEnd = now.Add(randomDurationLocked(bs.rng, 400*time.Millisecond, 2*time.Second))
		} else if bs.rng.Intn(100) < 20 {
			bs.phase = phaseIdle
			bs.phaseEnd = now.Add(randomDurationLocked(bs.rng, 8*time.Second, 40*time.Second))
		} else {
			bs.phaseEnd = now.Add(randomDurationLocked(bs.rng, 2*time.Second, 12*time.Second))
		}
	case phaseInteract:
		bs.phase = phaseReading
		bs.phaseEnd = now.Add(randomDurationLocked(bs.rng, 2*time.Second, 10*time.Second))
	case phaseIdle:
		if bs.rng.Intn(100) < 70 {
			bs.phase = phaseReading
			bs.phaseEnd = now.Add(randomDurationLocked(bs.rng, 3*time.Second, 12*time.Second))
		} else {
			bs.phase = phaseInteract
			bs.phaseEnd = now.Add(randomDurationLocked(bs.rng, 300*time.Millisecond, 1500*time.Millisecond))
		}
	}
}

// GetRandomBrowserProfile returns a random realistic browser profile (2025–2026).
func GetRandomBrowserProfile() BrowserProfile {
	profiles := []BrowserProfile{
		// Chrome 140 Windows 11
		{
			UserAgent:               "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
			AcceptLanguage:          "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7",
			Accept:                  "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
			AcceptEncoding:          "gzip, deflate, br, zstd",
			SecChUa:                 `"Chromium";v="140", "Not=A?Brand";v="24", "Google Chrome";v="140"`,
			SecChUaMobile:           "?0",
			SecChUaPlatform:         `"Windows"`,
			SecChUaFullVersionList:  `"Chromium";v="140.0.7339.128", "Not=A?Brand";v="10.0.0.0", "Google Chrome";v="140.0.7339.128"`,
			SecChViewportWidth:      "1920",
			SecFetchDest:            "document",
			SecFetchMode:            "navigate",
			SecFetchSite:            "none",
			DNT:                     "1",
			UpgradeInsecureRequests: "1",
		},
		// Chrome 140 macOS
		{
			UserAgent:               "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
			AcceptLanguage:          "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7",
			Accept:                  "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
			AcceptEncoding:          "gzip, deflate, br, zstd",
			SecChUa:                 `"Chromium";v="140", "Not=A?Brand";v="24", "Google Chrome";v="140"`,
			SecChUaMobile:           "?0",
			SecChUaPlatform:         `"macOS"`,
			SecChUaFullVersionList:  `"Chromium";v="140.0.7339.128", "Google Chrome";v="140.0.7339.128"`,
			SecChViewportWidth:      "1440",
			SecFetchDest:            "document",
			SecFetchMode:            "navigate",
			SecFetchSite:            "none",
			DNT:                     "1",
			UpgradeInsecureRequests: "1",
		},
		// Chrome 140 Android
		{
			UserAgent:               "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36",
			AcceptLanguage:          "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7",
			Accept:                  "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
			AcceptEncoding:          "gzip, deflate, br, zstd",
			SecChUa:                 `"Chromium";v="140", "Not=A?Brand";v="24", "Google Chrome";v="140"`,
			SecChUaMobile:           "?1",
			SecChUaPlatform:         `"Android"`,
			SecChViewportWidth:      "412",
			SecFetchDest:            "document",
			SecFetchMode:            "navigate",
			SecFetchSite:            "none",
			DNT:                     "1",
			UpgradeInsecureRequests: "1",
		},
		// Edge 140 Windows
		{
			UserAgent:               "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0",
			AcceptLanguage:          "ru,en;q=0.9",
			Accept:                  "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,image/apng,*/*;q=0.8",
			AcceptEncoding:          "gzip, deflate, br, zstd",
			SecChUa:                 `"Microsoft Edge";v="140", "Chromium";v="140", "Not=A?Brand";v="24"`,
			SecChUaMobile:           "?0",
			SecChUaPlatform:         `"Windows"`,
			SecChViewportWidth:      "1536",
			SecFetchDest:            "document",
			SecFetchMode:            "navigate",
			SecFetchSite:            "none",
			DNT:                     "1",
			UpgradeInsecureRequests: "1",
		},
		// Firefox 143 Windows
		{
			UserAgent:               "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:143.0) Gecko/20100101 Firefox/143.0",
			AcceptLanguage:          "ru-RU,ru;q=0.8,en-US;q=0.5,en;q=0.3",
			Accept:                  "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
			AcceptEncoding:          "gzip, deflate, br, zstd",
			SecFetchDest:            "document",
			SecFetchMode:            "navigate",
			SecFetchSite:            "none",
			DNT:                     "1",
			UpgradeInsecureRequests: "1",
		},
		// Safari 18 iOS
		{
			UserAgent:               "Mozilla/5.0 (iPhone; CPU iPhone OS 18_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Mobile/15E148 Safari/604.1",
			AcceptLanguage:          "ru-RU,ru;q=0.9",
			Accept:                  "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			AcceptEncoding:          "gzip, deflate, br",
			SecFetchDest:            "document",
			SecFetchMode:            "navigate",
			SecFetchSite:            "none",
			DNT:                     "1",
			UpgradeInsecureRequests: "1",
		},
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(profiles))))
	if err != nil {
		return profiles[0]
	}
	return profiles[n.Int64()]
}

// ApplyBrowserHeaders sets navigation-style headers (first page open).
func ApplyBrowserHeaders(req *http.Request, profile BrowserProfile) {
	ApplyKindHeaders(req, profile, KindNavigate)
}

// ApplyKindHeaders applies headers for a specific fetch kind so DPI sees
// Sec-Fetch-* and Accept matching real Chrome/Firefox behaviour.
func ApplyKindHeaders(req *http.Request, profile BrowserProfile, kind RequestKind) {
	req.Header.Set("User-Agent", profile.UserAgent)
	req.Header.Set("Accept-Language", profile.AcceptLanguage)
	req.Header.Set("Accept-Encoding", profile.AcceptEncoding)
	if profile.DNT != "" {
		req.Header.Set("DNT", profile.DNT)
	}
	if profile.SecChUa != "" {
		req.Header.Set("Sec-CH-UA", profile.SecChUa)
		req.Header.Set("Sec-CH-UA-Mobile", profile.SecChUaMobile)
		req.Header.Set("Sec-CH-UA-Platform", profile.SecChUaPlatform)
		if profile.SecChUaFullVersionList != "" {
			req.Header.Set("Sec-CH-UA-Full-Version-List", profile.SecChUaFullVersionList)
		}
		if profile.SecChViewportWidth != "" {
			req.Header.Set("Sec-CH-Viewport-Width", profile.SecChViewportWidth)
		}
	}

	switch kind {
	case KindNavigate:
		req.Header.Set("Accept", profile.Accept)
		req.Header.Set("Sec-Fetch-Dest", "document")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Site", "none")
		req.Header.Set("Sec-Fetch-User", "?1")
		if profile.UpgradeInsecureRequests != "" {
			req.Header.Set("Upgrade-Insecure-Requests", profile.UpgradeInsecureRequests)
		}
	case KindXHR:
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Cache-Control", "no-cache")
	case KindAsset:
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Sec-Fetch-Dest", "script")
		req.Header.Set("Sec-Fetch-Mode", "no-cors")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	case KindImage:
		req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
		req.Header.Set("Sec-Fetch-Dest", "image")
		req.Header.Set("Sec-Fetch-Mode", "no-cors")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	case KindWS:
		// Caller still sets Upgrade/Connection; we only set browser context.
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Sec-Fetch-Dest", "websocket")
		req.Header.Set("Sec-Fetch-Mode", "websocket")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Pragma", "no-cache")
	}

	// Same-origin look: Origin + Referer from the request host.
	if req.URL != nil && req.URL.Host != "" {
		origin := req.URL.Scheme + "://" + req.URL.Host
		if kind != KindNavigate {
			req.Header.Set("Origin", origin)
			req.Header.Set("Referer", origin+"/")
		}
	}
}

// GetRealisticReferer builds a plausible referer for the target URL.
func GetRealisticReferer(targetURL string) string {
	u, err := url.Parse(targetURL)
	if err != nil || u.Host == "" {
		return ""
	}
	// Prefer same-site "app shell" paths that editors use.
	paths := []string{"/", "/edit", "/docs", "/document", "/app", "/workspace"}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(paths))))
	if err != nil {
		return u.Scheme + "://" + u.Host + "/"
	}
	return u.Scheme + "://" + u.Host + paths[n.Int64()]
}

// WebSocketUpgradeHeaders returns headers for a WS upgrade that looks like
// it came from an open browser tab on the same site.
func WebSocketUpgradeHeaders(profile BrowserProfile, pageURL string) http.Header {
	h := make(http.Header)
	req, _ := http.NewRequest(http.MethodGet, pageURL, nil)
	if req != nil {
		ApplyKindHeaders(req, profile, KindWS)
		for k, vv := range req.Header {
			h[k] = vv
		}
	}
	if pageURL != "" {
		if u, err := url.Parse(pageURL); err == nil && u.Host != "" {
			h.Set("Origin", u.Scheme+"://"+u.Host)
			h.Set("Referer", strings.TrimRight(pageURL, "/")+"/")
		}
	}
	return h
}

// TimingProfile is a named human-activity cadence.
type TimingProfile struct {
	Name          string
	BaseDelay     time.Duration
	JitterPercent int
}

// GetRealisticTimingProfile picks a browsing tempo.
func GetRealisticTimingProfile() TimingProfile {
	profiles := []TimingProfile{
		{"focused_editing", 45 * time.Millisecond, 40},
		{"reading", 900 * time.Millisecond, 55},
		{"idle_tab", 4 * time.Second, 40},
		{"page_load", 25 * time.Millisecond, 30},
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(profiles))))
	if err != nil {
		return profiles[0]
	}
	return profiles[n.Int64()]
}

// GenerateRandomDelay returns base ± jitterPercent.
func GenerateRandomDelay(base time.Duration, jitterPercent int) time.Duration {
	if jitterPercent <= 0 {
		return base
	}
	span := base * time.Duration(jitterPercent) / 100
	if span <= 0 {
		return base
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(span)*2))
	if err != nil {
		return base
	}
	return base - span + time.Duration(n.Int64())
}

// MimicBrowserBehavior sleeps as if the user performed action.
func MimicBrowserBehavior(action string) {
	switch action {
	case "click":
		time.Sleep(GenerateRandomDelay(120*time.Millisecond, 50))
	case "type":
		time.Sleep(GenerateRandomDelay(80*time.Millisecond, 40))
	case "scroll":
		time.Sleep(GenerateRandomDelay(200*time.Millisecond, 60))
	case "navigate":
		time.Sleep(GenerateRandomDelay(350*time.Millisecond, 40))
	default:
		time.Sleep(GenerateRandomDelay(50*time.Millisecond, 50))
	}
}

// GenerateSessionID returns a random opaque session token (browser-like).
func GenerateSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x", b[:])
}

// ObfuscatePacketSize pads or notes a target size so successive packets are
// not a flat MTU sawtooth (which screams "VPN"). Prefer BrowseSession.TargetPayloadSize.
func ObfuscatePacketSize(data []byte, targetSize int) []byte {
	if targetSize <= len(data) {
		return data
	}
	out := make([]byte, targetSize)
	copy(out, data)
	_, _ = rand.Read(out[len(data):])
	return out
}

// GenerateTrafficPattern returns a short sequence of inter-send delays for
// one "page interaction" (load or click).
func GenerateTrafficPattern() []time.Duration {
	bs := NewBrowseSession()
	n := 4 + int(bs.rng.Intn(8))
	out := make([]time.Duration, n)
	for i := range out {
		out[i] = bs.NextDelay()
	}
	return out
}

func (bp BrowserProfile) String() string {
	return bp.UserAgent
}

func randomDuration(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max-min)))
	if err != nil {
		return min
	}
	return min + time.Duration(n.Int64())
}

func randomDurationLocked(rng *mrand.Rand, min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + time.Duration(rng.Int63n(int64(max-min)))
}

// Package stealth provides browser fingerprints and traffic-shaping helpers
// shared by all carriers so outbound traffic resembles a normal website visit.
package stealth

import (
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// Profile is one consistent browser identity for the life of a session.
type Profile struct {
	UserAgent      string
	AcceptLanguage string
	Platform       string // desktop_web | mobileweb
	SecCHUA        string
	SecCHUAMobile  string
	SecCHUAPlat    string
}

var (
	mu     sync.Mutex
	rng    = rand.New(rand.NewSource(time.Now().UnixNano()))
	pool   = []Profile{
		{
			UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
			AcceptLanguage: "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7",
			Platform:       "desktop_web",
			SecCHUA:        `"Google Chrome";v="141", "Chromium";v="141", "Not_A Brand";v="24"`,
			SecCHUAMobile:  "?0",
			SecCHUAPlat:    `"Windows"`,
		},
		{
			UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
			AcceptLanguage: "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7",
			Platform:       "desktop_web",
			SecCHUA:        `"Google Chrome";v="141", "Chromium";v="141", "Not_A Brand";v="24"`,
			SecCHUAMobile:  "?0",
			SecCHUAPlat:    `"macOS"`,
		},
		{
			UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:144.0) Gecko/20100101 Firefox/144.0",
			AcceptLanguage: "ru-RU,ru;q=0.8,en-US;q=0.5,en;q=0.3",
			Platform:       "desktop_web",
		},
		{
			UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Safari/605.1.15",
			AcceptLanguage: "ru-RU,ru;q=0.9",
			Platform:       "desktop_web",
		},
		{
			UserAgent:      "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
			AcceptLanguage: "en-US,en;q=0.9,ru;q=0.8",
			Platform:       "desktop_web",
			SecCHUA:        `"Google Chrome";v="141", "Chromium";v="141", "Not_A Brand";v="24"`,
			SecCHUAMobile:  "?0",
			SecCHUAPlat:    `"Linux"`,
		},
		{
			UserAgent:      "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6 Mobile/15E148 Safari/604.1",
			AcceptLanguage: "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7",
			Platform:       "mobileweb",
		},
		{
			UserAgent:      "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36",
			AcceptLanguage: "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7",
			Platform:       "mobileweb",
			SecCHUA:        `"Google Chrome";v="141", "Chromium";v="141", "Not_A Brand";v="24"`,
			SecCHUAMobile:  "?1",
			SecCHUAPlat:    `"Android"`,
		},
	}
)

// Random returns any profile.
func Random() Profile {
	mu.Lock()
	defer mu.Unlock()
	return pool[rng.Intn(len(pool))]
}

// Desktop returns a desktop browser profile.
func Desktop() Profile {
	mu.Lock()
	defer mu.Unlock()
	var d []Profile
	for _, p := range pool {
		if p.Platform == "desktop_web" {
			d = append(d, p)
		}
	}
	return d[rng.Intn(len(d))]
}

// Mobile returns a mobile browser profile.
func Mobile() Profile {
	mu.Lock()
	defer mu.Unlock()
	var m []Profile
	for _, p := range pool {
		if p.Platform == "mobileweb" {
			m = append(m, p)
		}
	}
	return m[rng.Intn(len(m))]
}

// ApplyHTTP sets browser-like headers for XHR/fetch-style requests.
func ApplyHTTP(h http.Header, p Profile, origin, referer string) {
	h.Set("User-Agent", p.UserAgent)
	h.Set("Accept-Language", p.AcceptLanguage)
	if h.Get("Accept") == "" {
		h.Set("Accept", "application/json, text/plain, */*")
	}
	if origin != "" {
		h.Set("Origin", origin)
	}
	if referer != "" {
		h.Set("Referer", referer)
	}
	if p.SecCHUA != "" {
		h.Set("Sec-CH-UA", p.SecCHUA)
		h.Set("Sec-CH-UA-Mobile", p.SecCHUAMobile)
		h.Set("Sec-CH-UA-Platform", p.SecCHUAPlat)
	}
	if h.Get("Sec-Fetch-Dest") == "" {
		h.Set("Sec-Fetch-Dest", "empty")
	}
	if h.Get("Sec-Fetch-Mode") == "" {
		h.Set("Sec-Fetch-Mode", "cors")
	}
	if h.Get("Sec-Fetch-Site") == "" {
		h.Set("Sec-Fetch-Site", "same-origin")
	}
	h.Set("Cache-Control", "no-cache")
	h.Set("Pragma", "no-cache")
}

// ApplyWS sets browser-like headers for WebSocket upgrade.
func ApplyWS(h http.Header, p Profile, origin string) {
	h.Set("User-Agent", p.UserAgent)
	h.Set("Accept-Language", p.AcceptLanguage)
	h.Set("Accept", "*/*")
	h.Set("Cache-Control", "no-cache")
	h.Set("Pragma", "no-cache")
	if origin != "" {
		h.Set("Origin", origin)
	}
	h.Set("Sec-Fetch-Dest", "websocket")
	h.Set("Sec-Fetch-Mode", "websocket")
	h.Set("Sec-Fetch-Site", "cross-site")
}

// Jitter returns base + random[0,span).
func Jitter(base, span time.Duration) time.Duration {
	if span <= 0 {
		return base
	}
	mu.Lock()
	defer mu.Unlock()
	return base + time.Duration(rng.Int63n(int64(span)))
}

// PadPayload appends 0..maxBytes random padding (for length obfuscation on
// carriers that tolerate trailing noise; callers must strip on receive if needed).
func PadPayload(b []byte, maxBytes int) []byte {
	if maxBytes <= 0 {
		return b
	}
	mu.Lock()
	n := rng.Intn(maxBytes + 1)
	mu.Unlock()
	if n == 0 {
		return b
	}
	pad := make([]byte, n)
	mu.Lock()
	rng.Read(pad)
	mu.Unlock()
	return append(b, pad...)
}

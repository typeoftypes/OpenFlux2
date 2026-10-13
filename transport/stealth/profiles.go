// Package stealth provides browser-like HTTP fingerprints shared by carriers
// so traffic looks like a normal user on a site rather than a fixed bot UA.
package stealth

import (
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// Profile is one consistent browser identity (UA + language + sec-fetch style).
type Profile struct {
	UserAgent      string
	AcceptLanguage string
	// Platform hints for JSON APIs that take "platform": desktop_web / mobileweb
	Platform string
	// OriginHost preferred Origin/Referer host when the carrier allows any
	// same-site origin (empty = carrier default).
	SecCHUA string
}

var (
	mu       sync.Mutex
	seeded   = rand.New(rand.NewSource(time.Now().UnixNano()))
	profiles = []Profile{
		// Desktop Chrome (Windows)
		{
			UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
			AcceptLanguage: "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7",
			Platform:       "desktop_web",
			SecCHUA:        `"Google Chrome";v="141", "Not?A_Brand";v="8", "Chromium";v="141"`,
		},
		// Desktop Chrome (macOS)
		{
			UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
			AcceptLanguage: "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7",
			Platform:       "desktop_web",
			SecCHUA:        `"Google Chrome";v="141", "Not?A_Brand";v="8", "Chromium";v="141"`,
		},
		// Desktop Firefox
		{
			UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:144.0) Gecko/20100101 Firefox/144.0",
			AcceptLanguage: "ru-RU,ru;q=0.8,en-US;q=0.5,en;q=0.3",
			Platform:       "desktop_web",
		},
		// Desktop Safari (macOS)
		{
			UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Safari/605.1.15",
			AcceptLanguage: "ru-RU,ru;q=0.9",
			Platform:       "desktop_web",
		},
		// Mobile Safari iPhone
		{
			UserAgent:      "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6 Mobile/15E148 Safari/604.1",
			AcceptLanguage: "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7",
			Platform:       "mobileweb",
		},
		// Mobile Chrome Android
		{
			UserAgent:      "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36",
			AcceptLanguage: "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7",
			Platform:       "mobileweb",
			SecCHUA:        `"Google Chrome";v="141", "Not?A_Brand";v="8", "Chromium";v="141"`,
		},
	}
)

// Random returns a random browser profile (new identity per call).
func Random() Profile {
	mu.Lock()
	defer mu.Unlock()
	return profiles[seeded.Intn(len(profiles))]
}

// PreferDesktop picks a desktop profile (APIs that reject mobileweb).
func PreferDesktop() Profile {
	mu.Lock()
	defer mu.Unlock()
	var desk []Profile
	for _, p := range profiles {
		if p.Platform == "desktop_web" {
			desk = append(desk, p)
		}
	}
	if len(desk) == 0 {
		return profiles[0]
	}
	return desk[seeded.Intn(len(desk))]
}

// PreferMobile picks a mobile profile.
func PreferMobile() Profile {
	mu.Lock()
	defer mu.Unlock()
	var m []Profile
	for _, p := range profiles {
		if p.Platform == "mobileweb" {
			m = append(m, p)
		}
	}
	if len(m) == 0 {
		return profiles[len(profiles)-1]
	}
	return m[seeded.Intn(len(m))]
}

// ApplyBrowserHeaders sets common browser-like headers on an HTTP request.
// origin and referer may be empty.
func ApplyBrowserHeaders(h http.Header, p Profile, origin, referer string) {
	h.Set("User-Agent", p.UserAgent)
	h.Set("Accept-Language", p.AcceptLanguage)
	h.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	if origin != "" {
		h.Set("Origin", origin)
	}
	if referer != "" {
		h.Set("Referer", referer)
	}
	if p.SecCHUA != "" {
		h.Set("Sec-CH-UA", p.SecCHUA)
		h.Set("Sec-CH-UA-Mobile", map[bool]string{true: "?1", false: "?0"}[p.Platform == "mobileweb"])
		h.Set("Sec-CH-UA-Platform", map[bool]string{true: `"Android"`, false: `"Windows"`}[p.Platform == "mobileweb"])
	}
	h.Set("Sec-Fetch-Dest", "empty")
	h.Set("Sec-Fetch-Mode", "cors")
	h.Set("Sec-Fetch-Site", "same-origin")
	h.Set("Cache-Control", "no-cache")
	h.Set("Pragma", "no-cache")
}

// ApplyWebSocketHeaders sets upgrade-style browser headers for WS dial.
func ApplyWebSocketHeaders(h http.Header, p Profile, origin string) {
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

// Jitter returns a random duration in [base, base+span).
func Jitter(base, span time.Duration) time.Duration {
	if span <= 0 {
		return base
	}
	mu.Lock()
	defer mu.Unlock()
	return base + time.Duration(seeded.Int63n(int64(span)))
}

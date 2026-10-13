package stealth

import (
	"encoding/base64"
	"sync"
)

// XORMask applies a rolling XOR with key (obfuscation, not encryption).
// Same function unmasks.
func XORMask(data, key []byte) []byte {
	if len(key) == 0 || len(data) == 0 {
		return data
	}
	out := make([]byte, len(data))
	for i := range data {
		out[i] = data[i] ^ key[i%len(key)]
	}
	return out
}

// SessionKey derives a short obfuscation key from session identity bytes.
func SessionKey(seed string) []byte {
	if seed == "" {
		return []byte{0x5a, 0xa5, 0x3c, 0xc3}
	}
	b := []byte(seed)
	out := make([]byte, 8)
	for i := 0; i < 8; i++ {
		out[i] = b[i%len(b)] ^ byte(i*17+31)
	}
	return out
}

var b64 = base64.RawURLEncoding

// WrapCursor hides payload as a URL-safe token (extra encoding layer for
// cursor/comment carriers).
func WrapCursor(payload []byte) string {
	return b64.EncodeToString(payload)
}

// UnwrapCursor reverses WrapCursor.
func UnwrapCursor(s string) ([]byte, error) {
	return b64.DecodeString(s)
}

// NoiseWords are innocuous tokens mixed into metadata fields where the carrier
// accepts free text (reduces fixed-string fingerprints).
var NoiseWords = []string{
	"edit", "save", "draft", "note", "sync", "view", "share", "comment", "update", "focus",
}

var noiseMu sync.Mutex

// NoiseWord returns a random benign token.
func NoiseWord() string {
	noiseMu.Lock()
	defer noiseMu.Unlock()
	return NoiseWords[rng.Intn(len(NoiseWords))]
}

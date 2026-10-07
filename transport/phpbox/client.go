package phpbox

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/p1neappleXpress/OpenFlux/transport"
)

// Client is a Mux over the direct-HTTP two-channel carrier - for a phpbox
// exit reachable over plain HTTP (no OpenFlux carrier in between). For the
// censored case the mux rides a real transport (e.g. cupsonline) instead;
// see NewMux. The public surface (Dial, Close) comes from the embedded Mux.
type Client struct {
	*Mux
}

// NewClient starts a mux against endpoint (the URL of phpbox.php) over the
// direct-HTTP carrier: a long-poll down GET and short up POSTs.
func NewClient(endpoint, token, session string) *Client {
	hc := newHTTPCarrier(endpoint, token, session)
	m := NewMux(hc)
	hc.onClosed = m.markClosed // the down-poll ending is a dead session
	_ = m.Start()
	return &Client{Mux: m}
}

// NewHTTPCarrier returns the masked direct-HTTP carrier without a Mux.
// streamproxy and tunnel.StreamNet create the mux themselves.
func NewHTTPCarrier(endpoint, token, session string) Carrier {
	if session == "" {
		session = transport.GenerateSessionID()
	}
	return newHTTPCarrier(endpoint, token, session)
}

// ParseEndpoint splits "https://host/path/phpbox.php|token" or
// "https://host/path/phpbox.php?k=token" into endpoint URL and token.
func ParseEndpoint(ref string) (endpoint, token string) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", ""
	}
	if i := strings.Index(ref, "|"); i >= 0 {
		return strings.TrimSpace(ref[:i]), strings.TrimSpace(ref[i+1:])
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ref, ""
	}
	token = u.Query().Get("k")
	if token == "" {
		token = u.Query().Get("token")
	}
	q := u.Query()
	q.Del("k")
	q.Del("token")
	q.Del("s")
	q.Del("r")
	u.RawQuery = q.Encode()
	return u.String(), token
}

// httpCarrier speaks the two-channel HTTP link phpbox.php exposes: it streams
// exit->client bytes from a long-poll GET (down) and batches client->exit
// bytes into short POSTs (up), because such hosts buffer request bodies but
// stream responses.
//
// Each request is shaped like browser XHR/fetch to a PHP site (realistic
// User-Agent, Sec-CH-UA, Origin, Referer, Sec-Fetch-*) so shared hosting and
// middleboxes see ordinary web traffic rather than a bare tunnel client.
type httpCarrier struct {
	endpoint, token, session string
	http                     *http.Client
	profile                  transport.BrowserProfile
	stealth                  transport.StealthConfig

	ctx      context.Context
	cancel   context.CancelFunc
	upCh     chan []byte
	recv     func([]byte)
	onClosed func()
}

func newHTTPCarrier(endpoint, token, session string) *httpCarrier {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := transport.DefaultConfig()
	profile := transport.GetRandomBrowserProfile()
	if cfg.BrowserProfile != nil {
		profile = *cfg.BrowserProfile
	}
	return &httpCarrier{
		endpoint: strings.TrimSpace(endpoint),
		token:    token,
		session:  session,
		profile:  profile,
		stealth:  cfg.StealthConfig,
		http: &http.Client{
			Timeout: 0, // the down-poll is long-lived
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          6,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   15 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				ForceAttemptHTTP2:     true,
			},
		},
		ctx:    ctx,
		cancel: cancel,
		upCh:   make(chan []byte, 256),
	}
}

func (h *httpCarrier) Receive(cb func([]byte)) { h.recv = cb }

func (h *httpCarrier) Start() error {
	go h.downLoop()
	go h.upLoop()
	return nil
}

func (h *httpCarrier) Stop() error {
	h.cancel()
	return nil
}

func (h *httpCarrier) Send(p []byte) error {
	select {
	case h.upCh <- append([]byte(nil), p...):
	case <-h.ctx.Done():
	}
	return nil
}

func (h *httpCarrier) url(role string) string {
	v := url.Values{"k": {h.token}, "s": {h.session}, "r": {role}}
	return h.endpoint + "?" + v.Encode()
}

func originOf(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// maskRequest applies a browser fingerprint so the request looks like a
// same-origin XHR/fetch from a tab that opened the host, not a dedicated client.
func (h *httpCarrier) maskRequest(req *http.Request) {
	transport.ApplyBrowserHeaders(req, h.profile)
	if origin := originOf(h.endpoint); origin != "" {
		req.Header.Set("Origin", origin)
		req.Header.Set("Referer", origin+"/")
	}
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Accept", "application/octet-stream, */*;q=0.8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Cache-Control", "no-cache")
}

// upLoop batches queued bytes and POSTs them to the up route.
func (h *httpCarrier) upLoop() {
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	var buf []byte
	flush := func() {
		if len(buf) == 0 {
			return
		}
		time.Sleep(h.stealth.ApplyJitter())
		body := buf
		buf = buf[:0]
		req, err := http.NewRequestWithContext(h.ctx, http.MethodPost, h.url("up"), bytes.NewReader(body))
		if err != nil {
			return
		}
		h.maskRequest(req)
		req.Header.Set("Content-Type", "application/octet-stream")
		if resp, err := h.http.Do(req); err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
	for {
		select {
		case p := <-h.upCh:
			buf = append(buf, p...)
			for drained := false; !drained; {
				select {
				case p2 := <-h.upCh:
					buf = append(buf, p2...)
				default:
					drained = true
				}
			}
		case <-t.C:
			flush()
		case <-h.ctx.Done():
			flush()
			return
		}
	}
}

// downLoop holds the streaming GET and feeds exit->client bytes to the mux
// until the exit caps it or it errors, then reports the session closed.
func (h *httpCarrier) downLoop() {
	if h.onClosed != nil {
		defer h.onClosed()
	}
	req, err := http.NewRequestWithContext(h.ctx, http.MethodGet, h.url("down"), nil)
	if err != nil {
		return
	}
	h.maskRequest(req)
	req.Header.Set("Accept", "*/*")
	resp, err := h.http.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	tmp := make([]byte, 32768)
	for {
		n, rerr := resp.Body.Read(tmp)
		if n > 0 && h.recv != nil {
			h.recv(tmp[:n])
		}
		if rerr != nil {
			return
		}
	}
}

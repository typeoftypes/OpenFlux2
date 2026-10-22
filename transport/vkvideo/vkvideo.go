package vkvideo

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/p1neappleXpress/OpenFlux/netbind"
	"github.com/p1neappleXpress/OpenFlux/transport"
	"github.com/p1neappleXpress/OpenFlux/utils"
)

// No-auth VK Video transport (rendezvous by video link).
//
// VK itself has no anonymous bidirectional channel for a normal video URL.
// This carrier uses the video link only as a shared secret to derive a topic,
// and exchanges frames over a public MQTT-over-WebSocket broker (no login).
//
//	profile URL  = https://vk.com/video-OWNER_VIDEOID
//	topic        = ofx/vkvid/<sha256(owner_video)[:16]>
//	default WSS  = broker.emqx.io (replace with your own Mosquitto in prod)
//
// Session secrecy: --encryption-key-file (recommended). Without it the topic
// is findable by anyone who knows the video link.

const (
	// Public brokers (no account). Prefer your own in production via params["broker"].
	defaultBrokerWS = "wss://broker.emqx.io:8084/mqtt"
	// MQTT CONNECT / SUBSCRIBE / PUBLISH is done with a minimal v3.1.1 codec below
	// so we don't pull an extra module into mobile builds.

	vkUA           = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:155.0) Gecko/20100101 Firefox/155.0"
	dataTag        = "OFXV1"
	maxMsgRaw      = 48_000
	preferredBatch = 120_000
)

var (
	reVideoPath = regexp.MustCompile(`(?i)video(-?\d+)_(\d+)`)
	reClipPath  = regexp.MustCompile(`(?i)clip(-?\d+)_(\d+)`)
)

// Transport is a no-auth carrier keyed by a VK Video URL.
type Transport struct {
	*transport.BaseTransport

	rawURL  string
	ownerID int64
	videoID int64
	topic   string
	broker  string
	ownID   string

	mu      sync.Mutex
	conn    *websocket.Conn
	writeCh chan []byte
	pktID   atomic.Uint16
}

// New builds a no-auth carrier. link is a VK Video URL.
// brokerWS is optional (params["broker"]); empty → defaultBrokerWS.
func New(link, brokerWS string, cfg transport.TransportConfig) *Transport {
	t := &Transport{
		BaseTransport: transport.NewBaseTransport(cfg),
		rawURL:        strings.TrimSpace(link),
		broker:        strings.TrimSpace(brokerWS),
		ownID:         randomID(),
		writeCh:       make(chan []byte, max(cfg.MaxQueueSize, 1024)),
	}
	if t.broker == "" {
		t.broker = defaultBrokerWS
	}
	if oid, vid, err := ParseVideoURL(t.rawURL); err == nil {
		t.ownerID, t.videoID = oid, vid
		t.topic = deriveTopic(oid, vid)
	}
	return t
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func randomID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ParseVideoURL extracts owner_id and video_id from common VK Video links.
func ParseVideoURL(link string) (ownerID, videoID int64, err error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return 0, 0, fmt.Errorf("empty video URL")
	}
	if strings.Contains(link, "video_ext.php") || strings.Contains(link, "oid=") {
		if u, e := url.Parse(link); e == nil {
			oid, id := u.Query().Get("oid"), u.Query().Get("id")
			if oid != "" && id != "" {
				return atoi64(oid), atoi64(id), nil
			}
		}
	}
	if m := reVideoPath.FindStringSubmatch(link); m != nil {
		return atoi64(m[1]), atoi64(m[2]), nil
	}
	if m := reClipPath.FindStringSubmatch(link); m != nil {
		return atoi64(m[1]), atoi64(m[2]), nil
	}
	if m := regexp.MustCompile(`^(-?\d+)_(\d+)$`).FindStringSubmatch(link); m != nil {
		return atoi64(m[1]), atoi64(m[2]), nil
	}
	return 0, 0, fmt.Errorf("not a VK Video link (want video-<owner>_<id>): %q", link)
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func deriveTopic(ownerID, videoID int64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("openflux-vkvid-v3|%d_%d", ownerID, videoID)))
	return "ofx/vkvid/" + hex.EncodeToString(h[:16])
}

func (t *Transport) PreferredBatchBytes() int { return preferredBatch }

func (t *Transport) Start() error {
	if err := t.BaseTransport.Start(); err != nil {
		return err
	}
	if t.topic == "" {
		oid, vid, err := ParseVideoURL(t.rawURL)
		if err != nil {
			return fmt.Errorf("vkvideo: %w", err)
		}
		t.ownerID, t.videoID = oid, vid
		t.topic = deriveTopic(oid, vid)
	}
	utils.Infof("[VKVIDEO] no-auth rendezvous video=%d_%d topic=%s broker=%s",
		t.ownerID, t.videoID, t.topic, t.broker)
	utils.SafeGo("vkvideo.loop", t.loop)
	utils.SafeGo("vkvideo.writer", t.writer)
	return nil
}

func (t *Transport) Stop() error {
	t.mu.Lock()
	if t.conn != nil {
		_ = t.conn.Close()
		t.conn = nil
	}
	t.mu.Unlock()
	return t.BaseTransport.Stop()
}

func (t *Transport) Send(data []byte) error {
	if !t.IsRunning() {
		return fmt.Errorf("vkvideo: not running")
	}
	select {
	case t.writeCh <- append([]byte(nil), data...):
		return nil
	case <-t.Done():
		return fmt.Errorf("vkvideo: stopped")
	}
}

func (t *Transport) loop() {
	delay := time.Second
	for t.IsRunning() {
		err := t.session()
		t.SetConnected(false)
		if !t.IsRunning() {
			return
		}
		utils.Debugf("[VKVIDEO] session ended: %v; retry in %s", err, delay)
		select {
		case <-t.Done():
			return
		case <-time.After(delay):
		}
		if delay < 20*time.Second {
			delay = time.Duration(float64(delay) * 1.5)
		}
	}
}

func (t *Transport) session() error {
	header := http.Header{}
	header.Set("User-Agent", vkUA)
	header.Set("Sec-WebSocket-Protocol", "mqtt")

	dial := netbind.DialContextFor(t.GetConfig().Network)
	d := websocket.Dialer{
		NetDialContext:   dial,
		HandshakeTimeout: 20 * time.Second,
		Subprotocols:     []string{"mqtt"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	conn, _, err := d.DialContext(ctx, t.broker, header)
	if err != nil {
		// some brokers want no subprotocol
		d.Subprotocols = nil
		header.Del("Sec-WebSocket-Protocol")
		conn, _, err = d.DialContext(ctx, t.broker, header)
		if err != nil {
			return fmt.Errorf("mqtt ws dial: %w", err)
		}
	}
	conn.SetReadLimit(8 << 20)

	t.mu.Lock()
	t.conn = conn
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		if t.conn == conn {
			t.conn = nil
		}
		t.mu.Unlock()
		_ = conn.Close()
	}()

	// MQTT CONNECT
	clientID := "ofx" + t.ownID
	if err := conn.WriteMessage(websocket.BinaryMessage, mqttConnect(clientID)); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, connack, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("connack: %w", err)
	}
	if len(connack) < 4 || connack[0]>>4 != 2 || connack[3] != 0 {
		return fmt.Errorf("mqtt connect refused: %x", connack)
	}

	// SUBSCRIBE topic QoS0
	if err := conn.WriteMessage(websocket.BinaryMessage, mqttSubscribe(1, t.topic)); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		return fmt.Errorf("suback: %w", err)
	}

	t.SetConnected(true)
	utils.Infof("[VKVIDEO] subscribed %s", t.topic)

	// keepalive PINGREQ every 30s
	pingDone := make(chan struct{})
	defer close(pingDone)
	go func() {
		tk := time.NewTicker(30 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-pingDone:
				return
			case <-t.Done():
				return
			case <-tk.C:
				t.mu.Lock()
				c := t.conn
				t.mu.Unlock()
				if c != nil {
					_ = c.WriteMessage(websocket.BinaryMessage, []byte{0xC0, 0x00})
				}
			}
		}
	}()

	for t.IsRunning() {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if len(data) == 0 {
			continue
		}
		switch data[0] >> 4 {
		case 3: // PUBLISH
			payload := mqttPublishPayload(data)
			t.handlePayload(payload)
		case 13: // PINGRESP
			// ok
		default:
			// ignore
		}
	}
	return nil
}

func (t *Transport) handlePayload(payload []byte) {
	text := string(payload)
	if !strings.HasPrefix(text, dataTag+"|") {
		return
	}
	parts := strings.SplitN(text, "|", 3)
	if len(parts) < 3 || parts[1] == t.ownID {
		return
	}
	raw, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil || len(raw) == 0 {
		return
	}
	t.RecordReceive(len(raw))
	t.CallReceive(raw)
}

func (t *Transport) writer() {
	for t.IsRunning() {
		select {
		case <-t.Done():
			return
		case pkt := <-t.writeCh:
			t.sendOne(pkt)
		}
	}
}

func (t *Transport) sendOne(pkt []byte) {
	t.mu.Lock()
	conn := t.conn
	t.mu.Unlock()
	if conn == nil {
		return
	}
	for off := 0; off < len(pkt); {
		end := off + maxMsgRaw
		if end > len(pkt) {
			end = len(pkt)
		}
		chunk := pkt[off:end]
		off = end
		body := dataTag + "|" + t.ownID + "|" + base64.StdEncoding.EncodeToString(chunk)
		msg := mqttPublish(t.topic, []byte(body))
		_ = conn.SetWriteDeadline(time.Now().Add(20 * time.Second))
		if err := conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
			utils.Debugf("[VKVIDEO] publish: %v", err)
			return
		}
		t.RecordSend(len(chunk))
	}
}

// --- minimal MQTT 3.1.1 binary helpers (CONNECT / SUBSCRIBE / PUBLISH QoS0) ---

func mqttEncodeLen(n int) []byte {
	var out []byte
	for {
		b := byte(n % 128)
		n /= 128
		if n > 0 {
			b |= 0x80
		}
		out = append(out, b)
		if n == 0 {
			break
		}
	}
	return out
}

func mqttConnect(clientID string) []byte {
	// variable header + payload
	proto := []byte{0x00, 0x04, 'M', 'Q', 'T', 'T', 0x04, 0x02, 0x00, 0x3C} // clean session, keepalive 60
	id := []byte(clientID)
	payload := append([]byte{byte(len(id) >> 8), byte(len(id))}, id...)
	vh := append(proto, payload...)
	pkt := []byte{0x10}
	pkt = append(pkt, mqttEncodeLen(len(vh))...)
	pkt = append(pkt, vh...)
	return pkt
}

func mqttSubscribe(id uint16, topic string) []byte {
	tb := []byte(topic)
	body := []byte{byte(id >> 8), byte(id)}
	body = append(body, byte(len(tb)>>8), byte(len(tb)))
	body = append(body, tb...)
	body = append(body, 0x00) // QoS0
	pkt := []byte{0x82}
	pkt = append(pkt, mqttEncodeLen(len(body))...)
	pkt = append(pkt, body...)
	return pkt
}

func mqttPublish(topic string, payload []byte) []byte {
	tb := []byte(topic)
	body := []byte{byte(len(tb) >> 8), byte(len(tb))}
	body = append(body, tb...)
	body = append(body, payload...)
	pkt := []byte{0x30} // PUBLISH QoS0
	pkt = append(pkt, mqttEncodeLen(len(body))...)
	pkt = append(pkt, body...)
	return pkt
}

func mqttPublishPayload(pkt []byte) []byte {
	if len(pkt) < 4 || pkt[0]>>4 != 3 {
		return nil
	}
	// skip fixed header length
	i := 1
	for i < len(pkt) && pkt[i]&0x80 != 0 {
		i++
	}
	i++ // last length byte
	if i+2 > len(pkt) {
		return nil
	}
	tlen := int(pkt[i])<<8 | int(pkt[i+1])
	i += 2
	// QoS bits in fixed header
	qos := (pkt[0] >> 1) & 0x3
	i += tlen
	if qos > 0 {
		i += 2 // packet id
	}
	if i > len(pkt) {
		return nil
	}
	return pkt[i:]
}

// TopicForURL is for tests / debugging.
func TopicForURL(link string) (string, error) {
	oid, vid, err := ParseVideoURL(link)
	if err != nil {
		return "", err
	}
	return deriveTopic(oid, vid), nil
}


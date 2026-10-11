// Package mailru implements a transport that tunnels packets through
// Mail.ru's cloud document editor (docs.datacloudmail.ru).
package mailru

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/typeoftypes/OpenFlux2/transport"
	"github.com/typeoftypes/OpenFlux2/utils"
)

// Safari on iPhone — must stay consistent with platform / Origin / Referer below.
const mailruUserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.6 Mobile/15E148 Safari/604.1"

var cursorPayloadRe = regexp.MustCompile(`"cursor":"[^;]+;([^"]+)"`)

var mailruAPIURL = "https://cloud.mail.ru/api/v4/r7/edit"

type MailruDocsInfo struct {
	Token        string
	DocKey       string
	WsURL        string
	FileType     string
	DocURL       string
	DocTitle     string
	Permissions  map[string]interface{}
	CallbackURL  string
	EditorUserID string
}

type DocSession struct {
	Info       MailruDocsInfo
	Conn       *websocket.Conn
	WriteQueue chan []byte
	UserID     string
	writeMu    sync.Mutex
}

func (s *DocSession) safeWrite(messageType int, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.Conn == nil {
		return fmt.Errorf("no conn")
	}
	return s.Conn.WriteMessage(messageType, data)
}

type MailruDocsTransport struct {
	*transport.BaseTransport

	weblink     string
	session     *DocSession
	userCounter atomic.Int32
	baseUserID  string
	jar         http.CookieJar
	jarOnce     sync.Once
}

func NewMailruDocsTransport(weblink string, config transport.TransportConfig) *MailruDocsTransport {
	t := &MailruDocsTransport{
		BaseTransport: transport.NewBaseTransport(config),
		weblink:       normalizeWeblink(weblink),
	}
	t.baseUserID = randUserID()
	return t
}

func (t *MailruDocsTransport) cookieJar() http.CookieJar {
	t.jarOnce.Do(func() {
		j, _ := cookiejar.New(nil)
		t.jar = j
	})
	return t.jar
}

func normalizeWeblink(weblink string) string {
	weblink = strings.TrimSpace(weblink)
	for _, prefix := range []string{
		"https://cloud.mail.ru/public/",
		"http://cloud.mail.ru/public/",
		"https://cloud.mail.ru/",
		"http://cloud.mail.ru/",
	} {
		if strings.HasPrefix(weblink, prefix) {
			return strings.Trim(strings.TrimPrefix(weblink, prefix), "/")
		}
	}
	return weblink
}

func (t *MailruDocsTransport) Start() error {
	if err := t.BaseTransport.Start(); err != nil {
		return err
	}
	t.baseUserID = randUserID()
	utils.SafeGo("mailru.keepAlive", t.keepAliveLoop)
	utils.Debugf("[M-DOCS] start weblink=%s UA=%s", t.weblink, mailruUserAgent)
	t.connectToDoc(0)
	return nil
}

func (t *MailruDocsTransport) Send(data []byte) error {
	if !t.IsConnected() {
		return fmt.Errorf("transport not connected")
	}
	t.Mu.RLock()
	session := t.session
	t.Mu.RUnlock()
	if session == nil {
		return fmt.Errorf("no active session")
	}
	select {
	case session.WriteQueue <- data:
		t.RecordSend(len(data))
		return nil
	default:
		return fmt.Errorf("write queue full")
	}
}

func (t *MailruDocsTransport) connectToDoc(attempt int) {
	if !t.IsRunning() {
		return
	}
	utils.Debugf("[M-DOCS] connectToDoc attempt %d", attempt)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				utils.Debugf("[PANIC] recovered in mailru.connect: %v", r)
			}
		}()

		t.Mu.Lock()
		existingSession := t.session
		t.Mu.Unlock()

		var userID string
		if existingSession != nil && existingSession.UserID != "" {
			userID = existingSession.UserID
		} else {
			suffix := fmt.Sprintf("%03d", t.userCounter.Add(1)%1000)
			userID = t.baseUserID + suffix
		}

		info, err := t.fetchDocInfo(t.weblink)
		if err != nil {
			utils.Debugf("[M-DOCS] fetchDocInfo failed: %v", err)
			t.scheduleReconnect(attempt)
			return
		}

		dialer := websocket.Dialer{
			// Mail.ru edge often answers 409 unless the upgrade looks like a
			// real browser (permessage-deflate + Accept-Language + cookies).
			EnableCompression: true,
			HandshakeTimeout:  20 * time.Second,
			Jar:               t.cookieJar(),
			NetDialContext: (&net.Dialer{
				Timeout:   12 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		}

		headers := http.Header{}
		headers.Set("User-Agent", mailruUserAgent)
		// Public docs open from cloud.mail.ru; docs.datacloudmail.ru is the WS host.
		headers.Set("Origin", "https://cloud.mail.ru")
		headers.Set("Accept", "*/*")
		headers.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
		headers.Set("Cache-Control", "no-cache")
		headers.Set("Pragma", "no-cache")
		headers.Set("Sec-Fetch-Dest", "websocket")
		headers.Set("Sec-Fetch-Mode", "websocket")
		headers.Set("Sec-Fetch-Site", "cross-site")

		utils.Debugf("[M-DOCS] WebSocket dial %s", info.WsURL)
		conn, resp, err := dialer.Dial(info.WsURL, headers)
		if err != nil {
			status := 0
			if resp != nil {
				status = resp.StatusCode
				if resp.Body != nil {
					_ = resp.Body.Close()
				}
			}
			utils.Debugf("[M-DOCS] WebSocket dial failed (http %d): %v", status, err)
			if status == 409 {
				t.Mu.Lock()
				if t.session != nil {
					t.session.UserID = ""
				}
				t.Mu.Unlock()
				t.baseUserID = randUserID()
				// Fresh cookie jar — edge may have bound the old identity.
				t.jarOnce = sync.Once{}
				t.jar = nil
				utils.Debugf("[M-DOCS] HTTP 409 — rotated editor userID + cookie jar, backoff")
				t.scheduleReconnect(attempt + 2)
				return
			}
			t.scheduleReconnect(attempt)
			return
		}
		utils.Debugf("[M-DOCS] WebSocket connected")

		writeQueue := make(chan []byte, t.GetConfig().MaxQueueSize)
		if existingSession != nil {
			writeQueue = existingSession.WriteQueue
		}
		session := &DocSession{
			Info:       info,
			Conn:       conn,
			WriteQueue: writeQueue,
			UserID:     userID,
		}
		t.Mu.Lock()
		t.session = session
		t.SetConnected(true)
		t.Mu.Unlock()

		if existingSession == nil {
			utils.SafeGo("mailru.writer", t.writerLoop)
		}

		// Engine.IO / Socket.IO: wait briefly for open frames, then auth.
		_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
		for i := 0; i < 6; i++ {
			_, msg, rerr := conn.ReadMessage()
			if rerr != nil {
				break
			}
			text := string(msg)
			utils.Debugf("[M-DOCS] handshake frame: %s", truncate(text, 80))
			if strings.HasPrefix(text, "0") || text == "40" || strings.HasPrefix(text, "40") {
				break
			}
			if text == "2" {
				_ = session.safeWrite(websocket.TextMessage, []byte("3"))
			}
		}
		_ = conn.SetReadDeadline(time.Time{})

		auth1 := fmt.Sprintf(`40{"token":"%s"}`, info.Token)
		_ = session.safeWrite(websocket.TextMessage, []byte(auth1))

		authMsg := map[string]interface{}{
			"type":                "auth",
			"docid":               info.DocKey,
			"documentCallbackUrl": info.CallbackURL,
			"token":               "fghhfgsjdgfjs",
			"user": map[string]interface{}{
				"id":        info.EditorUserID,
				"username":  userID,
				"indexUser": -1,
			},
			"editorType":         0,
			"lastOtherSaveTime":  -1,
			"block":              []interface{}{},
			"documentFormatSave": 65,
			"view":               false,
			"isCloseCoAuthoring": false,
			"openCmd": map[string]interface{}{
				"c":               "open",
				"id":              info.DocKey,
				"userid":          info.EditorUserID,
				"format":          info.FileType,
				"url":             info.DocURL,
				"title":           info.DocTitle,
				"lcid":            25,
				"nobase64":        true,
				"convertToOrigin": ".pdf.xps.oxps.djvu",
			},
			"lang":                  "ru",
			"mode":                  "edit",
			"permissions":           info.Permissions,
			"IsAnonymousUser":       false,
			"timezoneOffset":        -180,
			"coEditingMode":         "fast",
			"jwtOpen":               info.Token,
			"time":                  1000,
			"supportAuthChangesAck": true,
		}
		messagePart, _ := json.Marshal([]interface{}{"message", authMsg})
		_ = session.safeWrite(websocket.TextMessage, []byte(fmt.Sprintf("42%s", string(messagePart))))

		connectedAt := time.Now()
		for t.IsRunning() {
			_, message, err := conn.ReadMessage()
			if err != nil {
				utils.Debugf("[M-DOCS] Read error: %v", err)
				t.SetConnected(false)
				_ = conn.Close()
				next := attempt
				if time.Since(connectedAt) > 45*time.Second {
					next = -1
				}
				t.scheduleReconnect(next)
				return
			}
			t.handleMessage(session, message)
		}
	}()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (t *MailruDocsTransport) writerLoop() {
	var queue chan []byte
	for t.IsRunning() && queue == nil {
		t.Mu.Lock()
		if t.session != nil {
			queue = t.session.WriteQueue
		}
		t.Mu.Unlock()
		if queue == nil {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if queue == nil {
		return
	}
	var pending []byte
	for t.IsRunning() {
		if pending == nil {
			packet, ok := <-queue
			if !ok {
				return
			}
			pending = packet
		}
		t.Mu.RLock()
		session := t.session
		t.Mu.RUnlock()
		if session == nil || session.Conn == nil {
			time.Sleep(15 * time.Millisecond)
			continue
		}
		payload := base64.StdEncoding.EncodeToString(pending)
		msg := fmt.Sprintf(`42["message",{"type":"cursor","cursor":"18;%s"}]`, payload)
		if err := session.safeWrite(websocket.TextMessage, []byte(msg)); err != nil {
			utils.Debugf("[M-DOCS] Write error: %v", err)
			time.Sleep(15 * time.Millisecond)
			continue
		}
		pending = nil
	}
}

func (t *MailruDocsTransport) keepAliveLoop() {
	ticker := time.NewTicker(t.GetConfig().KeepAliveInterval)
	defer ticker.Stop()
	keepAliveMsg := `42["message",{"type":"cursor","cursor":"18;---KA---"}]`
	for t.IsRunning() {
		<-ticker.C
		t.Mu.Lock()
		session := t.session
		t.Mu.Unlock()
		if session != nil && session.Conn != nil {
			if err := session.safeWrite(websocket.TextMessage, []byte(keepAliveMsg)); err != nil {
				utils.Debugf("[M-DOCS] Keep-alive failed: %v", err)
				t.SetConnected(false)
			}
		}
	}
}

func (t *MailruDocsTransport) handleMessage(session *DocSession, data []byte) {
	text := string(data)
	if strings.Contains(text, "---KA---") {
		return
	}
	if text == "2" {
		if session != nil {
			_ = session.safeWrite(websocket.TextMessage, []byte("3"))
		}
		return
	}
	if text == "3" {
		return
	}
	if strings.Contains(text, `"type":"auth"`) && strings.Contains(text, `"result":1`) {
		utils.Debugf("[M-DOCS] Auth OK for user %s", session.UserID)
		return
	}
	if strings.Contains(text, "cursor") {
		base64Str := t.extractBase64String(text)
		if base64Str == "" {
			return
		}
		decoded, err := base64.StdEncoding.DecodeString(base64Str)
		if err != nil {
			utils.Debugf("[M-DOCS] Base64 decode error: %v", err)
			return
		}
		t.RecordReceive(len(decoded))
		t.CallReceive(decoded)
	}
}

func (t *MailruDocsTransport) extractBase64String(response string) string {
	matches := cursorPayloadRe.FindStringSubmatch(response)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

func (t *MailruDocsTransport) scheduleReconnect(attempt int) {
	next := attempt + 1
	if !t.IsRunning() || next >= t.GetConfig().MaxReconnectAttempts {
		return
	}
	d := reconnectBackoff(next)
	utils.Debugf("[M-DOCS] reconnecting in %v (attempt %d)", d, next)
	time.Sleep(d)
	if !t.IsRunning() {
		return
	}
	t.RecordReconnect()
	t.connectToDoc(next)
}

func reconnectBackoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	shift := n - 1
	if shift > 5 {
		shift = 5
	}
	d := 500 * time.Millisecond * time.Duration(1<<uint(shift))
	if d > 6*time.Second {
		d = 6 * time.Second
	}
	d += time.Duration(rand.Int63n(int64(d/2) + 1))
	return d
}

func (t *MailruDocsTransport) fetchDocInfo(weblink string) (MailruDocsInfo, error) {
	client := &http.Client{
		Timeout: 20 * time.Second,
		Jar:     t.cookieJar(),
	}
	// Match mobile Safari fingerprint: API used to be desktop_web only; with an
	// iPhone UA, mobileweb reduces edge 409 on the subsequent WS upgrade.
	reqBody := map[string]string{
		"x-email":  "anonym",
		"public":   "/" + weblink,
		"platform": "mobileweb",
	}
	jsonData, _ := json.Marshal(reqBody)
	utils.Debugf("[M-DOCS] fetchDocInfo POST %s platform=mobileweb", mailruAPIURL)

	req, err := http.NewRequest("POST", mailruAPIURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return MailruDocsInfo{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", mailruUserAgent)
	req.Header.Set("X-Api-Version", "4")
	req.Header.Set("Origin", "https://cloud.mail.ru")
	req.Header.Set("Referer", fmt.Sprintf("https://cloud.mail.ru/public/%s", weblink))
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")

	resp, err := client.Do(req)
	if err != nil {
		return MailruDocsInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Fallback once with desktop_web if mobileweb is rejected.
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden {
			return t.fetchDocInfoDesktop(weblink)
		}
		return MailruDocsInfo{}, fmt.Errorf("API returned status %d", resp.StatusCode)
	}
	return parseDocInfo(resp.Body)
}

func (t *MailruDocsTransport) fetchDocInfoDesktop(weblink string) (MailruDocsInfo, error) {
	client := &http.Client{Timeout: 20 * time.Second, Jar: t.cookieJar()}
	reqBody := map[string]string{
		"x-email":  "anonym",
		"public":   "/" + weblink,
		"platform": "desktop_web",
	}
	jsonData, _ := json.Marshal(reqBody)
	utils.Debugf("[M-DOCS] fetchDocInfo fallback platform=desktop_web")
	req, _ := http.NewRequest("POST", mailruAPIURL, bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", mailruUserAgent)
	req.Header.Set("X-Api-Version", "4")
	req.Header.Set("Origin", "https://cloud.mail.ru")
	req.Header.Set("Referer", fmt.Sprintf("https://cloud.mail.ru/public/%s", weblink))
	resp, err := client.Do(req)
	if err != nil {
		return MailruDocsInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return MailruDocsInfo{}, fmt.Errorf("API returned status %d", resp.StatusCode)
	}
	return parseDocInfo(resp.Body)
}

func parseDocInfo(r io.Reader) (MailruDocsInfo, error) {
	bodyBytes, _ := io.ReadAll(r)
	var res map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		return MailruDocsInfo{}, fmt.Errorf("failed to parse JSON: %w", err)
	}
	apiBase, _ := res["api"].(string)
	token, _ := res["token"].(string)
	document, ok := res["document"].(map[string]interface{})
	if !ok || document == nil {
		return MailruDocsInfo{}, fmt.Errorf("document object missing")
	}
	docKey, _ := document["key"].(string)
	fileType, _ := document["fileType"].(string)
	docURL, _ := document["url"].(string)
	docTitle, _ := document["title"].(string)
	permissions, _ := document["permissions"].(map[string]interface{})
	if permissions == nil {
		permissions = make(map[string]interface{})
	}
	editorConfig, ok := res["editorConfig"].(map[string]interface{})
	if !ok || editorConfig == nil {
		return MailruDocsInfo{}, fmt.Errorf("editorConfig object missing")
	}
	callbackURL, _ := editorConfig["callbackUrl"].(string)
	userObj, _ := editorConfig["user"].(map[string]interface{})
	var editorUserID string
	if userObj != nil {
		editorUserID, _ = userObj["id"].(string)
	}
	wsBase := wsBaseFrom(apiBase)
	wsURL := fmt.Sprintf("%s/doc/%s/c/?EIO=4&transport=websocket", wsBase, docKey)
	return MailruDocsInfo{
		Token: token, DocKey: docKey, WsURL: wsURL, FileType: fileType,
		DocURL: docURL, DocTitle: docTitle, Permissions: permissions,
		CallbackURL: callbackURL, EditorUserID: editorUserID,
	}, nil
}

func wsBaseFrom(apiBase string) string {
	switch {
	case strings.HasPrefix(apiBase, "https://"):
		return "wss://" + strings.TrimPrefix(apiBase, "https://")
	case strings.HasPrefix(apiBase, "http://"):
		return "ws://" + strings.TrimPrefix(apiBase, "http://")
	}
	return apiBase
}

func randUserID() string {
	return fmt.Sprintf("%010d", rand.New(rand.NewSource(time.Now().UnixNano())).Intn(1000000000))
}

// silence unused import if url only needed for jar cookies edge cases
var _ = url.URL{}

// Package mailru implements a transport that tunnels packets through
// Mail.ru's cloud document editor (docs.datacloudmail.ru), the same
// coauthoring backend family as Yandex.Docs. Two peers open the same
// public document and smuggle packets through the "cursor" field of the
// collaborative editing protocol.
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

	"github.com/typeoftypes/OpenFlux2/netbind"
	"github.com/typeoftypes/OpenFlux2/transport"
	"github.com/typeoftypes/OpenFlux2/utils"
)

// Список User-Agent'ов — ротация при 409 / ошибке handshake / обрыве.
var mailruUserAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:155.0) Gecko/20100101 Firefox/155.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:155.0) Gecko/20100101 Firefox/155.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Safari/605.1.15",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36",
}

var cursorPayloadRe = regexp.MustCompile(`"cursor":"[^;]+;([^"]+)"`)

// saveChangesMessageTemplate — шаблон веб-клиента Mail.ru.
const saveChangesMessageTemplate = `42["message",{"type":"saveChanges","changes":"[\"76;AgAAADEA//8BAOwbfF7pEAAALQEAAAMAAAAAAAAAAAAAAAAAAAAAAAAA9v///xoAAAAyADAAMgA2AC4AMgAuADEALgAyADIANgA4AA==\",\"35;BgAAADYAMgA3AAEAHAABAAAAAAAAAAEAAABhAAAAAAMAAAA=\",\"35;BgAAADYAMgA3AAEAHAABAAAAAQAAAAEAAABzAAAAAAMAAAA=\",\"35;BgAAADYAMgA3AAEAHAABAAAAAgAAAAEAAABkAAAAAAMAAAA=\"]","startSaveChanges":true,"endSaveChanges":true,"isCoAuthoring":false,"isExcel":false,"deleteIndex":null,"excelAdditionalInfo":"{\"UserId\":\"%s\",\"UserShortId\":\"%s\",\"CursorInfo\":\"14;BgAAADYAMgA3AAMAAAA=\"}","unlock":false,"releaseLocks":false}]`

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
	authed     atomic.Bool // true только после Auth OK
}

func (s *DocSession) safeWrite(messageType int, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.Conn == nil {
		return fmt.Errorf("no conn")
	}
	_ = s.Conn.SetWriteDeadline(time.Now().Add(docWriteTimeout))
	return s.Conn.WriteMessage(messageType, data)
}

const docWriteTimeout = 20 * time.Second
const mailruSocketIOHandshakeTimeout = 15 * time.Second

func waitMailruSocketIO(session *DocSession, token string) error {
	if session == nil || session.Conn == nil {
		return fmt.Errorf("mailru: websocket session is nil")
	}
	conn := session.Conn

	if err := conn.SetReadDeadline(time.Now().Add(mailruSocketIOHandshakeTimeout)); err != nil {
		return fmt.Errorf("mailru: set handshake deadline: %w", err)
	}
	defer conn.SetReadDeadline(time.Time{})

	waitFor := func(match func(string) bool) error {
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				return err
			}
			if messageType != websocket.TextMessage {
				continue
			}
			text := string(payload)
			if text == "2" {
				if err := session.safeWrite(websocket.TextMessage, []byte("3")); err != nil {
					return fmt.Errorf("mailru: send Engine.IO pong: %w", err)
				}
				continue
			}
			if match(text) {
				return nil
			}
			utils.Debugf("[M-DOCS] handshake: ignoring server frame %q", text)
		}
	}

	if err := waitFor(func(text string) bool {
		return strings.HasPrefix(text, "0{")
	}); err != nil {
		return fmt.Errorf("mailru: wait for Engine.IO open: %w", err)
	}
	utils.Debugf("[M-DOCS] Engine.IO open")

	socketConnect := fmt.Sprintf(`40{"token":"%s"}`, token)
	if err := session.safeWrite(websocket.TextMessage, []byte(socketConnect)); err != nil {
		return fmt.Errorf("mailru: send Socket.IO connect: %w", err)
	}

	if err := waitFor(func(text string) bool {
		return strings.HasPrefix(text, "40")
	}); err != nil {
		return fmt.Errorf("mailru: wait for Socket.IO connect: %w", err)
	}
	utils.Debugf("[M-DOCS] Socket.IO connected")
	return nil
}

type MailruDocsTransport struct {
	*transport.BaseTransport

	weblink     string
	session     *DocSession
	userCounter atomic.Int32
	baseUserID  string
	cookieJar   *cookiejar.Jar
	jarMu       sync.RWMutex
	uaIndex     atomic.Uint32
	reconnecting atomic.Bool
}

func NewMailruDocsTransport(weblink string, config transport.TransportConfig) *MailruDocsTransport {
	t := &MailruDocsTransport{
		BaseTransport: transport.NewBaseTransport(config),
		weblink:       normalizeWeblink(weblink),
	}
	t.baseUserID = randUserID()
	jar, _ := cookiejar.New(nil)
	t.cookieJar = jar
	t.uaIndex.Store(uint32(rand.Intn(len(mailruUserAgents))))
	return t
}

func (t *MailruDocsTransport) currentUA() string {
	idx := t.uaIndex.Load() % uint32(len(mailruUserAgents))
	return mailruUserAgents[idx]
}

func (t *MailruDocsTransport) rotateUA() string {
	next := t.uaIndex.Add(1)
	ua := mailruUserAgents[next%uint32(len(mailruUserAgents))]
	utils.Debugf("[M-DOCS] rotated User-Agent → %s", truncate(ua, 60))
	return ua
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
	utils.SafeGo("mailru.editorActivity", t.editorActivityLoop)
	utils.Debugf("[M-DOCS] start weblink=%s UA=%s", t.weblink, truncate(t.currentUA(), 60))
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
	// Не шлём payload, пока не получили Auth OK — иначе сервер может сразу закрыть.
	if !session.authed.Load() {
		return fmt.Errorf("transport not authenticated yet")
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
	utils.Debugf("[M-DOCS] connectToDoc attempt %d ua=%s", attempt, truncate(t.currentUA(), 50))

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

		ua := t.currentUA()

		info, err := t.fetchDocInfo(t.weblink, ua)
		if err != nil {
			utils.Debugf("[M-DOCS] fetchDocInfo failed: %v", err)
			if utils.Throttled("m-docs.fetch", time.Minute) {
				utils.Infof("[M-DOCS] cannot open the document: %v; retrying", err)
			}
			t.rotateUA()
			t.scheduleReconnect(attempt)
			return
		}

		// Логируем permissions — важно для canEdit
		editOK := canEdit(info.Permissions)
		utils.Debugf("[M-DOCS] doc permissions edit=%v key=%s", editOK, info.DocKey)

		dialer := websocket.Dialer{
			EnableCompression: true,
			HandshakeTimeout:  15 * time.Second,
			Jar:               t.getJar(),
			NetDialContext: netbind.Wrap(&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		}

		headers := http.Header{}
		headers.Set("User-Agent", ua)
		headers.Set("Accept-Language", "en-US,en;q=0.9,ru;q=0.8")
		headers.Set("Origin", "https://docs.datacloudmail.ru")
		headers.Set("Cache-Control", "no-cache")
		headers.Set("Pragma", "no-cache")

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

			t.Mu.Lock()
			if t.session != nil {
				t.session.UserID = ""
			}
			t.Mu.Unlock()
			t.baseUserID = randUserID()
			t.rotateJar()
			t.rotateUA()

			if status == 409 {
				utils.Debugf("[M-DOCS] HTTP 409 — rotated UA + userID + jar")
				t.scheduleReconnect(attempt + 1)
				return
			}
			t.scheduleReconnect(attempt)
			return
		}
		utils.Debugf("[M-DOCS] WebSocket connected (ua=%s)", truncate(ua, 40))

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

		if err := waitMailruSocketIO(session, info.Token); err != nil {
			utils.Debugf("[M-DOCS] Socket.IO handshake failed: %v", err)
			_ = conn.Close()
			t.rotateUA()
			t.scheduleReconnect(attempt)
			return
		}

		// Пока не ставим Connected — ждём Auth OK в handleMessage.
		// Иначе session начинает слать hello до auth и сервер может закрыть.
		t.Mu.Lock()
		t.session = session
		t.Mu.Unlock()

		if existingSession == nil {
			utils.SafeGo("mailru.writer", t.writerLoop)
		}

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
		if err := session.safeWrite(websocket.TextMessage, []byte(fmt.Sprintf("42%s", string(messagePart)))); err != nil {
			utils.Debugf("[M-DOCS] auth write failed: %v", err)
			_ = conn.Close()
			t.scheduleReconnect(attempt)
			return
		}

		connectedAt := time.Now()
		for t.IsRunning() {
			_, message, err := conn.ReadMessage()
			if err != nil {
				utils.Debugf("[M-DOCS] Read error: %v", err)
				if utils.Throttled("m-docs.drop", time.Minute) {
					utils.Infof("[M-DOCS] connection to the document dropped: %v; reconnecting", err)
				}
				t.SetConnected(false)
				session.authed.Store(false)
				_ = conn.Close()

				t.rotateUA()

				next := attempt
				if time.Since(connectedAt) > 15*time.Second {
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
		t.Mu.RLock()
		if t.session != nil {
			queue = t.session.WriteQueue
		}
		t.Mu.RUnlock()
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
		if session == nil || session.Conn == nil || !session.authed.Load() {
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
		t.Mu.RLock()
		session := t.session
		t.Mu.RUnlock()
		if session != nil && session.Conn != nil && session.authed.Load() {
			if err := session.safeWrite(websocket.TextMessage, []byte(keepAliveMsg)); err != nil {
				utils.Debugf("[M-DOCS] Keep-alive failed: %v", err)
				t.SetConnected(false)
			}
		}
	}
}

func (t *MailruDocsTransport) editorActivityLoop() {
	const (
		minDelay = 2 * time.Second
		maxDelay = 8 * time.Second
	)
	for t.IsRunning() {
		span := int64(maxDelay - minDelay)
		delay := minDelay + time.Duration(rand.Int63n(span+1))
		time.Sleep(delay)

		t.Mu.RLock()
		session := t.session
		t.Mu.RUnlock()
		if session == nil || session.Conn == nil || !session.authed.Load() {
			continue
		}
		// КРИТИЧНО: view-only ссылка — сервер рвёт соединение на saveChanges (close 1005).
		if !canEdit(session.Info.Permissions) {
			continue
		}
		msg := buildSaveChanges(session)
		if err := session.safeWrite(websocket.TextMessage, msg); err != nil {
			utils.Debugf("[M-DOCS] saveChanges write error: %v", err)
		}
	}
}

func canEdit(permissions map[string]interface{}) bool {
	if permissions == nil {
		return true
	}
	edit, ok := permissions["edit"].(bool)
	return !ok || edit
}

func buildSaveChanges(session *DocSession) []byte {
	userID := session.Info.EditorUserID
	if userID == "" {
		userID = session.UserID
	}
	short := userID
	if len(short) > 1 {
		short = short[:len(short)-1]
	}
	return []byte(fmt.Sprintf(saveChangesMessageTemplate, userID, short))
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
		session.authed.Store(true)
		t.SetConnected(true)
		return
	}
	// Auth failed?
	if strings.Contains(text, `"type":"auth"`) && strings.Contains(text, `"result":`) {
		utils.Debugf("[M-DOCS] Auth response (not OK): %s", truncate(text, 120))
	}
	if strings.Contains(text, "cursor") {
		base64Str := extractBase64String(text)
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

func extractBase64String(response string) string {
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
	// Не запускаем два reconnect параллельно
	if !t.reconnecting.CompareAndSwap(false, true) {
		return
	}
	d := reconnectBackoff(next)
	utils.Debugf("[M-DOCS] reconnecting in %v (attempt %d)", d, next)
	go func() {
		time.Sleep(d)
		t.reconnecting.Store(false)
		if !t.IsRunning() {
			return
		}
		t.RecordReconnect()
		t.connectToDoc(next)
	}()
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
	if d > 15*time.Second {
		d = 15 * time.Second
	}
	d += time.Duration(rand.Int63n(int64(d/2) + 1))
	return d
}

func (t *MailruDocsTransport) getJar() http.CookieJar {
	t.jarMu.RLock()
	defer t.jarMu.RUnlock()
	return t.cookieJar
}

func (t *MailruDocsTransport) rotateJar() {
	jar, _ := cookiejar.New(nil)
	t.jarMu.Lock()
	t.cookieJar = jar
	t.jarMu.Unlock()
}

func (t *MailruDocsTransport) fetchDocInfo(weblink, ua string) (MailruDocsInfo, error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
		Jar:     t.getJar(),
	}
	reqBody := map[string]string{
		"x-email":  "anonym",
		"public":   "/" + weblink,
		"platform": "desktop_web",
	}
	jsonData, _ := json.Marshal(reqBody)

	apiURL := "https://cloud.mail.ru/api/v4/r7/edit"
	utils.Debugf("[M-DOCS] fetchDocInfo POST %s ua=%s", apiURL, truncate(ua, 40))

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return MailruDocsInfo{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,ru;q=0.8")
	req.Header.Set("User-Agent", ua)
	req.Header.Set("X-Api-Version", "4")
	req.Header.Set("Origin", "https://cloud.mail.ru")
	req.Header.Set("Referer", fmt.Sprintf("https://cloud.mail.ru/public/%s", weblink))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")

	resp, err := client.Do(req)
	if err != nil {
		return MailruDocsInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return MailruDocsInfo{}, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
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
		return MailruDocsInfo{}, fmt.Errorf("document object missing: %s", truncate(string(bodyBytes), 200))
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
	wsBase := strings.Replace(apiBase, "https://", "wss://", 1)
	wsURL := fmt.Sprintf("%s/doc/%s/c/?EIO=4&transport=websocket", wsBase, docKey)
	return MailruDocsInfo{
		Token: token, DocKey: docKey, WsURL: wsURL, FileType: fileType,
		DocURL: docURL, DocTitle: docTitle, Permissions: permissions,
		CallbackURL: callbackURL, EditorUserID: editorUserID,
	}, nil
}

func randUserID() string {
	return fmt.Sprintf("%010d", rand.New(rand.NewSource(time.Now().UnixNano())).Intn(1000000000))
}

// ---- CookieExchanger ----

func (t *MailruDocsTransport) FetchCookies() (map[string]string, error) {
	t.jarMu.RLock()
	jar := t.cookieJar
	t.jarMu.RUnlock()
	if jar == nil {
		return nil, fmt.Errorf("mailru: cookie jar is nil")
	}
	u, err := url.Parse("https://cloud.mail.ru/")
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	for _, c := range jar.Cookies(u) {
		out[c.Name] = c.Value
	}
	return out, nil
}

func (t *MailruDocsTransport) ApplyCookies(values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	u, _ := url.Parse("https://cloud.mail.ru/")
	jar, _ := cookiejar.New(nil)
	cookies := make([]*http.Cookie, 0, len(values))
	for k, v := range values {
		cookies = append(cookies, &http.Cookie{Name: k, Value: v, Path: "/"})
	}
	jar.SetCookies(u, cookies)

	t.jarMu.Lock()
	t.cookieJar = jar
	t.jarMu.Unlock()

	utils.Debugf("[M-DOCS] applied %d cookies, forcing reconnect", len(cookies))

	t.Mu.Lock()
	session := t.session
	t.session = nil
	t.SetConnected(false)
	t.Mu.Unlock()
	if session != nil && session.Conn != nil {
		_ = session.Conn.Close()
	}
	if t.IsRunning() {
		t.scheduleReconnect(0)
	}
	return nil
}

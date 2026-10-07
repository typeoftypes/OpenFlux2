package mobile

import (
	"fmt"
	"strings"

	"github.com/p1neappleXpress/OpenFlux/socks5"
	"github.com/p1neappleXpress/OpenFlux/streamproxy"
	"github.com/p1neappleXpress/OpenFlux/transport"
	"github.com/p1neappleXpress/OpenFlux/transport/phpbox"
	"github.com/p1neappleXpress/OpenFlux/tunnel"
	"github.com/p1neappleXpress/OpenFlux/utils"
)

// StartStreamProxy is StartProxy for the phpbox stream mode: the exit is a PHP
// node on an ordinary web host (deploy/phpbox, put there by PhpCall "deploy"),
// reached over a cups.online room ("cupsonline"), a Mail.ru document ("mailru"),
// or directly over HTTP to phpbox.php ("phpbox", url = endpoint|token).
// The local SOCKS5 proxy hands every connection to a stream of the mux. TCP only
// (ports 80 and 443 at the exit); no key, no session: the apps' own TLS protects
// the content.
func StartStreamProxy(transportType, url, listenAddr, username, password, bypassDomains string) string {
	proxy.mu.Lock()
	if proxy.running {
		proxy.mu.Unlock()
		return ""
	}
	proxy.mu.Unlock()

	if errMsg := validateStreamType(transportType, url); errMsg != "" {
		return errMsg
	}

	utils.SetLevel(int(debugLevel.Load()))
	utils.SetLogSink(appendLog)
	appendLog("[ANDROID] Запуск прокси в режиме без сервера (PHP-хостинг)")

	carrier, err := streamCarrier(transportType, strings.TrimSpace(url))
	if err != nil {
		appendLog(fmt.Sprintf("[ERROR] Ошибка запуска прокси: %v", err))
		return err.Error()
	}
	var wrap func(socks5.Dialer) socks5.Dialer
	if strings.TrimSpace(bypassDomains) != "" {
		list := strings.Split(bypassDomains, "\n")
		wrap = func(d socks5.Dialer) socks5.Dialer { return newSplitDialer(d, list) }
	}
	p, err := streamproxy.Start(streamproxy.Options{
		Carrier: carrier, Socks: listenAddr, Username: username, Password: password, Wrap: wrap, Label: transportType,
	})
	if err != nil {
		appendLog(fmt.Sprintf("[ERROR] Не удалось запустить прокси: %v", err))
		if strings.Contains(err.Error(), "bind") {
			return fmt.Sprintf("Порт %s уже занят", listenAddr)
		}
		return err.Error()
	}

	proxy.mu.Lock()
	proxy.running = true
	proxy.stream = p
	proxy.mu.Unlock()
	appendLog(fmt.Sprintf("[SUCCESS] SOCKS5-прокси слушает %s (режим без сервера)", listenAddr))
	return ""
}

// StartStreamPacket is Start for the stream mode over the VPN TUN.
func StartStreamPacket(transportType, url string) string {
	if errMsg := validateStreamType(transportType, url); errMsg != "" {
		return errMsg
	}
	return startPacket(func() (transport.Transport, error) {
		appendLog("[ANDROID] Запуск туннеля в режиме без сервера (PHP-хостинг)")
		carrier, err := streamCarrier(transportType, strings.TrimSpace(url))
		if err != nil {
			return nil, err
		}
		return tunnel.NewStreamNet(carrier), nil
	})
}

func validateStreamType(transportType, url string) string {
	switch transportType {
	case "cupsonline", "mailru", "phpbox":
	default:
		return fmt.Sprintf("Режим без сервера не работает через транспорт %q: нужен cups.online, Mail.ru или phpbox", transportType)
	}
	if strings.TrimSpace(url) == "" {
		return "Адрес ноды не указан"
	}
	return ""
}

// streamCarrier builds the byte carrier the phpbox mux rides.
func streamCarrier(transportType, url string) (phpbox.Carrier, error) {
	if transportType == "phpbox" {
		ep, tok := phpbox.ParseEndpoint(url)
		if ep == "" || tok == "" {
			return nil, fmt.Errorf("phpbox: укажите URL phpbox.php и токен (url|token или ?k=)")
		}
		// NewClient starts the mux; we only need the HTTP carrier underneath.
		// Expose a bare carrier so streamproxy/StreamNet own the mux.
		return newMaskedHTTPCarrier(ep, tok), nil
	}
	return newRawTransport(transportType, url, nil, transport.DefaultConfig(), false)
}

// newMaskedHTTPCarrier is the direct-HTTP link to phpbox.php with browser masking.
func newMaskedHTTPCarrier(endpoint, token string) phpbox.Carrier {
	return phpbox.NewHTTPCarrier(endpoint, token, transport.GenerateSessionID())
}

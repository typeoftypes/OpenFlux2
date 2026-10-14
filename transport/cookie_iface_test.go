package transport_test

import (
	"github.com/typeoftypes/OpenFlux2/transport"
	"github.com/typeoftypes/OpenFlux2/transport/cupsonline"
	"github.com/typeoftypes/OpenFlux2/transport/mailru"
	"github.com/typeoftypes/OpenFlux2/transport/yandex"
)

// Compile-time guarantees that every HTTP-based transport implements
// CookieExchanger. If any of these break, main.go's type assertion will
// silently skip control wiring and cookie exchange will be dead.
var (
	_ transport.CookieExchanger = (*yandex.YandexDocsTransport)(nil)
	_ transport.CookieExchanger = (*yandex.YandexVolgaTransport)(nil)
	_ transport.CookieExchanger = (*yandex.BoardsTransport)(nil)
	_ transport.CookieExchanger = (*mailru.MailruDocsTransport)(nil)
	_ transport.CookieExchanger = (*cupsonline.CupsonlineTransport)(nil)
)

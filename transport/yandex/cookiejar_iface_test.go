package yandex

import (
	"github.com/typeoftypes/OpenFlux2/transport"
)

var _ transport.CookieExchanger = (*YandexDocsTransport)(nil)

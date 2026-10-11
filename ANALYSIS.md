# Почему «ничего не изменилось» — разбор

## Факты из вашего CI-лога (успешная сборка liboflux)

1. Исходник уже был с новым UA:
   `iPhone OS 18_6 ... Version/26.6`
2. В `liboflux.a` через `strings` **был** этот UA.
3. CI упал только на проверке: Xcode `strings` → `failed to flush output` при `pipefail`.
4. В том же `.a` лежат ещё Chrome/Firefox — это **другие** транспорты (masking/yandex), не mailru.

Вывод: проблема не в том, что Go «не видит» строку в `mailru.go`.  
Либо **на телефон не попал новый IPA** (сборка оборвалась на проверке), либо **рантайм** (409 / VPN-петля).

## Частые причины

### A. IPA не обновился
Пока шаг `Build liboflux.a` exit 1 — artifact IPA со старым ядром или сборка не доходит до установки.
→ Нужен зелёный CI после фикса `build_ios.sh` (strings в файл).

### B. Импорты тянут чужой module path
Если `go.mod` = `github.com/typeoftypes/OpenFlux2`, а `mobile/*.go` импортирует
`github.com/p1neappleXpress/OpenFlux/transport/mailru`, Go может взять **прокси-копию**
upstream, а не ваш локальный `transport/mailru`.
→ Скрипт `scripts/apply-mailru-ios-fix.sh` переписывает все импорты на module из go.mod.

### C. Рантайм Mail.ru 409
Даже с новым UA edge отдаёт 409 при:
- `platform: desktop_web` + mobile UA
- нет cookies с API на WS
- конфликт userID (два клиента на одном документе)
→ В `mailru.go`: mobileweb, cookie jar, compression, 409→rotate ID, лог UA.

### D. Системный VPN
WS к mail.ru уходит внутрь TUN → петля. SOCKS при этом работает.
→ `PacketTunnelProvider`: exclude cloud.mail.ru / docs.datacloudmail.ru.

## Как проверить на 100%

```bash
# 1. Локальный пакет
go list -f '{{.Dir}}' ./transport/mailru
grep mailruUserAgent $(go list -f '{{.Dir}}' ./transport/mailru)/mailru.go

# 2. После build_ios.sh
grep -F 'iPhone OS 18_6' output/ios/liboflux.strings.txt

# 3. На устройстве в логе приложения
[M-DOCS] start weblink=... UA=Mozilla/5.0 (iPhone; CPU iPhone OS 18_6
[M-DOCS] WebSocket connected
[M-DOCS] Auth OK
```

Если в логе приложения **нет** строки `UA=Mozilla/5.0 (iPhone...` — на девайсе старое ядро.

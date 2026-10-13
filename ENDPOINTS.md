# Актуальные эндпоинты Mail.ru Docs (проверено 2026-10-13)

## Живой тест
- POST https://cloud.mail.ru/api/v4/r7/edit → **200**, поле `api`
- Пример `api`: `https://docs.datacloudmail.ru/2026.2.1.2268`
- WSS: `wss://docs.datacloudmail.ru/2026.2.1.2268/doc/<KEY>/c/?EIO=4&transport=websocket` → **101 + Engine.IO open**

## Важно
- Корень `https://docs.datacloudmail.ru/` отдаёт **404** — это нормально.
- Рабочий путь **версионный**: `/2026.x.x.xxxx/doc/...` (версия приходит в `api` из r7/edit).
- Альтернативный хост `docs-mo.datacloudmail.ru` с этой сети **не отвечает** (timeout).
- Не хардкодьте хост: всегда берите `api` из ответа r7/edit и делайте `wss://`.

## Обязательные условия WS (иначе 409)
1. `EnableCompression: true` (permessage-deflate)
2. UA Firefox 155 Windows на API **и** WS
3. `Origin: https://docs.datacloudmail.ru`

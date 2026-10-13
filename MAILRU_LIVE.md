# Mail.ru Docs — проверено 2026-10-13

## Живой API
- `POST https://cloud.mail.ru/api/v4/r7/edit`
  body: `{"x-email":"anonym","public":"/WEBLINK","platform":"desktop_web"}`
- Ответ `api`: `https://docs.datacloudmail.ru/2026.2.1.2268` (версия меняется)
- WSS: `wss://docs.datacloudmail.ru/<ver>/doc/<KEY>/c/?EIO=4&transport=websocket`

## Обязательный handshake
1. Дождаться Engine.IO `0{...}`
2. Отправить `40{"token":"<JWT>"}` (не голый `40`)
3. Дождаться `40{"sid":...}`
4. Отправить `42["message",{auth...}]` с полями:
   - `jwtOpen` = JWT из r7/edit
   - `openCmd.lcid` = 25
   - `lang`/`mode`/`permissions`/`coEditingMode` как в upstream

Без `jwtOpen` сервер отвечает `disconnectReason 4006 token:jwt must be provided`.

## 409 на upgrade (часть edge)
- `EnableCompression: true` (permessage-deflate)
- UA Firefox 155 Windows на API и WS
- `Origin: https://docs.datacloudmail.ru`
- `Accept-Language: en-US,en;q=0.9`

## Проверено в этой среде
- WS connect: OK (в т.ч. Chrome/Safari)
- Full auth + documentOpen: **OK** с полным auth payload

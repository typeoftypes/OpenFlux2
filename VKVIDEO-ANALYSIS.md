# VK Video: как реально ходит трафик

## 1. Обычное видео (VOD) — `vk.com/video-OWNER_ID` / `vkvideo.ru/video-…`

| Механизм | Есть? | Нужен токен? | Подходит для туннеля? |
|----------|-------|--------------|------------------------|
| WebSocket co-edit (как Mail.ru Docs) | **Нет** | — | — |
| `wss://pubsub.live.vkvideo.ru` (Centrifugo) | Только **Live**-чат | да для send | **Нет для VOD** |
| `video.getLongPollServer` | Только **трансляции** | да | Нет для обычного ролика |
| `video.getComments` / `createComment` | **Да** | **да** (user token, scope video) | Да, но медленно + модерация |
| Bots Long Poll `video_comment_new` | Для **группы**-владельца | community token | Rx realtime для group video |

**Вывод:** анонимного канала «только ссылка на видео» у VK **нет**.
Носитель = **комментарии к ролику** + access_token (cookies / token).

## 2. Правильная схема транспорта (как реализовать)

```
Client/Exit
   │  Parse URL → owner_id + video_id
   │
   ├─ SEND:  POST api.vk.com/method/video.createComment
   │           owner_id, video_id, message=OFXV1|<peer>|<b64>
   │
   └─ RECV:  poll video.getComments (sort=desc, count=20)
             every 1–2s  OR  Bots LP video_comment_new (если owner = group)
```

Сессия OpenFlux (`--encryption-key-file`) шифрует payload до base64 в тексте комментария.

## 3. Почему предыдущий Centrifugo-вариант неверен

`pubsub.live.vkvideo.ru` обслуживает **live.vkvideo.ru** стрим-чат.
Подписка на `public:ofx:…` для VOD либо отклоняется, либо это не инфраструктура «видео по ссылке».

## 4. Требования к профилю

- URL: `https://vk.com/video-123_456` (или vkvideo.ru)
- Token: user access_token **или** cookies сессии VK (remixsid → token через web)
- Оба конца: **разные** аккаунты (или один аккаунт + bot LP), комментарии открыты на ролике
- Шифрование: стандартный encryption-key-file

## 5. Ограничения (важно)

- Антиспам / flood control VK (error 9, 29)
- Длина комментария ~4096 символов → chunking
- Комментарии **видны** всем, кто открыл ролик (payload выглядит как мусор/base64)
- Скорость низкая vs Mail.ru WS (секунды RTT)

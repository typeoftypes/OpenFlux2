# Как правильно сделать обмен через VK Video

## Факт
У **обычного** ролика нет WebSocket «как у документа Mail.ru».
`video.getLongPollServer` и `pubsub.live.vkvideo.ru` — только **трансляции / Live**.

## Рабочий носитель
Комментарии к ролику:

```
SEND  video.createComment  message = OFXV1|<peer>|<base64>
RECV  video.getComments    poll ~1.5s  (или Bots LP video_comment_new)
```

Нужен **user access_token** (scope video) на обоих концах.

## Профиль
```
--transport=vkvideo
--url='https://vk.com/video-OWNER_VIDEOID'
# token в params / cookies: access_token=...
--encryption-key-file=secret.key
```

## Ограничения
- flood control VK
- ~4k на комментарий → chunk
- комментарии публичны (шифр на session layer)
- скорость низкая (сек RTT), не для YouTube 2K

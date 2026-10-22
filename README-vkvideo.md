# Transport vkvideo — VK Video (не Live)

Профиль: **ссылка на видео** + обычный `--encryption-key-file`.

## Ссылки
- `https://vk.com/video-22822305_456241864`
- `https://vkvideo.ru/video-22822305_456241864`
- `https://vk.com/video_ext.php?oid=-22822305&id=456241864`
- `https://vk.com/clip-123_456`

Канал = SHA256 от `owner_id` + `video_id` (не от live-канала).

```bash
./openflux --role=exit --mode=l4 --transport=vkvideo \
  --url='https://vk.com/video-OWNER_VIDEOID' \
  --encryption-key-file=./secret.key

./openflux --role=client --inbound=socks5 --transport=vkvideo \
  --url='https://vk.com/video-OWNER_VIDEOID' \
  --encryption-key-file=./secret.key --socks5=:1080
```

В Android: TransportType `vkvideo`, value = URL ролика.

# VK Video без авторизации VK

## Идея
VK не даёт анонимный канал на ролик.  
**Ссылка на видео** = общий секрет → topic  
**Трафик** = MQTT over WebSocket на публичном брокере (без логина).

```
URL  https://vk.com/video-123_456
  →  topic ofx/vkvid/<sha256>
  →  wss://broker.emqx.io:8084/mqtt
```

## Запуск
```bash
# оба конца — одна и та же ссылка на видео
./openflux --role=exit --mode=l4 --transport=vkvideo \
  --url='https://vk.com/video-OWNER_VIDEOID' \
  --encryption-key-file=./secret.key

./openflux --role=client --inbound=socks5 --transport=vkvideo \
  --url='https://vk.com/video-OWNER_VIDEOID' \
  --encryption-key-file=./secret.key --socks5=:1080
```

Опционально свой брокер: params `broker=wss://your-mosquitto/mqtt`

## Важно
- Публичный EMQX/Mosquitto — **demo/research**; для боя подними свой MQTT (без auth или с паролем в broker URL не светить).
- Кто знает ссылку на видео, может найти topic — поэтому **encryption-key-file обязателен**.
- Это не «трафик внутри файла VK», а **rendezvous по ссылке без аккаунта VK**.

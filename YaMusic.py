__version__ = (4, 0, 3)
# meta banner: https://raw.githubusercontent.com/kamekuro/hikka-mods/main/banners/yamusic.png
# meta developer: @codrago_m
# scope: heroku_only
# scope: heroku_min 2.0.0
# requires: aiohttp asyncio pillow>=10.0.0 yandex-music
import aiohttp
import asyncio
import hashlib
import io
import json
import logging
import math
import random
import string
import typing
import time
import uuid
from PIL import Image, ImageDraw, ImageEnhance, ImageFilter, ImageFont
import telethon
import yandex_music
import yandex_music.exceptions
from .. import loader, utils
logger = logging.getLogger(__name__)
# =========================================================
# Ynison client (ported from Go backend of the author's site)
# =========================================================
YNISON_REDIRECTOR = (
    "wss://ynison.music.yandex.ru/redirector.YnisonRedirectService/GetRedirectToYnison"
)
YNISON_STATE_PATH = "/ynison_state.YnisonStateService/PutYnisonState"
YNISON_ORIGIN = "https://music.yandex.ru"
YNISON_PING_INTERVAL = 20.0
YNISON_READ_WAIT = 70.0
YNISON_STATE_STALE_SEC = 90.0  # если стейт не обновлялся — переподключаемся
YNISON_HEALTH_CHECK_INTERVAL = 30.0
def _uuid4() -> str:
    return str(uuid.uuid4())
class YnisonError(Exception):
    """Базовая ошибка клиента Ynison."""
class YnisonAuthError(YnisonError):
    """Неверный или истёкший токен."""
class YnisonClient:
    """Собственный Ynison-клиент. Держит WebSocket-соединение и состояние плеера."""
    def __init__(self, token: str, device_id: str, device_title: str = "Hikka"):
        token = (token or "").strip()
        for prefix in ("OAuth ", "Bearer "):
            if token.lower().startswith(prefix.lower()):
                token = token[len(prefix):].strip()
        self.token = token
        self.device_id = device_id
        self.device_title = device_title
        self._session: typing.Optional[aiohttp.ClientSession] = None
        self._ws: typing.Optional[aiohttp.ClientWebSocketResponse] = None
        self._task: typing.Optional[asyncio.Task] = None
        self._ping_task: typing.Optional[asyncio.Task] = None
        self._health_task: typing.Optional[asyncio.Task] = None
        self._running = False
        self._state: typing.Optional[dict] = None
        self._state_event = asyncio.Event()
        self._last_error: typing.Optional[str] = None
        self._connected = False
        self._last_state_ts: float = 0.0  # monotonic time of last state
        self._reconnect_requested = asyncio.Event()
        self._state_listeners: list = []  # callbacks called on every new state
    @property
    def latest_state(self) -> typing.Optional[dict]:
        return self._state
    @property
    def is_connected(self) -> bool:
        return self._connected
    @property
    def last_error(self) -> typing.Optional[str]:
        return self._last_error
    @property
    def state_age(self) -> float:
        """Сколько секунд прошло с последнего обновления стейта."""
        if self._last_state_ts <= 0:
            return float("inf")
        return time.monotonic() - self._last_state_ts
    def is_state_fresh(self, max_age: float = YNISON_STATE_STALE_SEC) -> bool:
        return self._connected and self._state is not None and self.state_age < max_age
    def is_playing(self) -> bool:
        """Проверяет, играет ли сейчас трек (по последнему стейту)."""
        if not self.is_state_fresh():
            return False
        ps = _find_state(self._state)
        if not ps:
            return False
        status = ps.get("status") or {}
        if status.get("paused"):
            return False
        current = _current_playable(self._state)
        if not current:
            return False
        playable_id = (
            current.get("playable_id")
            or current.get("track_id")
            or current.get("id")
            or ""
        )
        return bool(playable_id)
    def add_state_listener(self, callback: typing.Callable) -> None:
        """Подписаться на обновления стейта (sync или async callback)."""
        if callback not in self._state_listeners:
            self._state_listeners.append(callback)
    def remove_state_listener(self, callback: typing.Callable) -> None:
        try:
            self._state_listeners.remove(callback)
        except ValueError:
            pass
    async def start(self) -> None:
        if self._running:
            return
        self._running = True
        self._reconnect_requested.clear()
        self._task = asyncio.create_task(self._run_forever())
        self._health_task = asyncio.create_task(self._health_loop())
    async def stop(self) -> None:
        self._running = False
        self._reconnect_requested.set()  # разбудить health/run если ждут
        if self._ping_task and not self._ping_task.done():
            self._ping_task.cancel()
        if self._health_task and not self._health_task.done():
            self._health_task.cancel()
        if self._ws and not self._ws.closed:
            try:
                await self._ws.close()
            except Exception:
                pass
        if self._task and not self._task.done():
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
        if self._session and not self._session.closed:
            await self._session.close()
    async def request_reconnect(self) -> None:
        """Принудительно запросить переподключение."""
        logger.info("Ynison reconnect requested")
        self._reconnect_requested.set()
        if self._ws and not self._ws.closed:
            try:
                await self._ws.close()
            except Exception:
                pass
    async def wait_for_state(self, timeout: float = 15.0) -> bool:
        try:
            await asyncio.wait_for(self._state_event.wait(), timeout=timeout)
            return True
        except asyncio.TimeoutError:
            return False
    async def _health_loop(self) -> None:
        """Периодически проверяет свежесть стейта и инициирует reconnect при необходимости."""
        try:
            while self._running:
                await asyncio.sleep(YNISON_HEALTH_CHECK_INTERVAL)
                if not self._running:
                    return
                if not self._connected:
                    continue
                age = self.state_age
                if age > YNISON_STATE_STALE_SEC:
                    logger.warning(
                        f"Ynison state is stale ({age:.0f}s old), requesting reconnect"
                    )
                    self._last_error = f"state stale ({age:.0f}s)"
                    await self.request_reconnect()
        except asyncio.CancelledError:
            return
        except Exception as e:
            logger.debug(f"Ynison health loop error: {e}")
    async def _run_forever(self) -> None:
        backoff = 2.0
        while self._running:
            started = time.monotonic()
            self._reconnect_requested.clear()
            try:
                await self._session_loop()
            except asyncio.CancelledError:
                return
            except YnisonAuthError as e:
                self._last_error = f"auth: {e}"
                logger.error(f"Ynison auth error, stopping: {e}")
                self._running = False
                self._connected = False
                return
            except Exception as e:
                self._last_error = str(e)
                logger.warning(f"Ynison session ended: {e}")
            self._connected = False
            if not self._running:
                return
            # Если reconnect запрошен явно — не ждать полный backoff
            if self._reconnect_requested.is_set():
                backoff = min(backoff, 3.0)
            if time.monotonic() - started > 60:
                backoff = 2.0
            try:
                await asyncio.wait_for(
                    self._reconnect_requested.wait(), timeout=backoff
                )
            except asyncio.TimeoutError:
                pass
            backoff = min(backoff * 2, 60.0)
    async def _session_loop(self) -> None:
        if self._session is None or self._session.closed:
            self._session = aiohttp.ClientSession()
        redirect = await self._redirect()
        host = redirect["host"]
        ticket = redirect["ticket"]
        session_id = redirect["session_id"]
        target = host.rstrip("/") + YNISON_STATE_PATH
        if not target.startswith("ws"):
            target = "wss://" + target
        ws_proto = self._make_protocols(ticket, session_id)
        headers = {
            "Origin": YNISON_ORIGIN,
            "Authorization": f"OAuth {self.token}",
        }
        logger.info(f"Ynison dialing {target}")
        ws = await self._session.ws_connect(
            target,
            headers=headers,
            protocols=ws_proto,
            heartbeat=None,
            autoping=True,
        )
        self._ws = ws
        self._connected = True
        self._last_error = None
        self._last_state_ts = time.monotonic()  # считаем момент подключения свежим
        hello = self._build_full_state_request()
        await ws.send_str(json.dumps(hello, ensure_ascii=False))
        self._ping_task = asyncio.create_task(self._ping_loop(ws))
        try:
            while True:
                if self._reconnect_requested.is_set() or not self._running:
                    raise YnisonError("reconnect requested")
                try:
                    msg = await asyncio.wait_for(ws.receive(), timeout=5.0)
                except asyncio.TimeoutError:
                    continue
                if msg.type == aiohttp.WSMsgType.TEXT:
                    try:
                        data = json.loads(msg.data)
                        self._handle_state(data)
                    except Exception as e:
                        logger.debug(f"Ynison bad frame: {e}")
                elif msg.type == aiohttp.WSMsgType.BINARY:
                    try:
                        data = json.loads(msg.data.decode("utf-8"))
                        self._handle_state(data)
                    except Exception as e:
                        logger.debug(f"Ynison bad binary frame: {e}")
                elif msg.type == aiohttp.WSMsgType.ERROR:
                    raise YnisonError(f"websocket error: {ws.exception()}")
                elif msg.type in (aiohttp.WSMsgType.CLOSE, aiohttp.WSMsgType.CLOSED):
                    raise YnisonError("websocket closed by server")
        finally:
            self._connected = False
            if self._ping_task and not self._ping_task.done():
                self._ping_task.cancel()
                self._ping_task = None
            if not ws.closed:
                try:
                    await ws.close()
                except Exception:
                    pass
            self._ws = None
    async def _ping_loop(self, ws: aiohttp.ClientWebSocketResponse) -> None:
        try:
            while True:
                await asyncio.sleep(YNISON_PING_INTERVAL)
                if ws.closed or self._reconnect_requested.is_set():
                    return
                try:
                    await ws.ping()
                except Exception:
                    return
        except asyncio.CancelledError:
            return
    def _make_protocols(self, ticket: str, session_id: str) -> list:
        info = {
            "Ynison-Device-Id": self.device_id,
            "Ynison-Device-Info": json.dumps(
                {"app_name": "Chrome", "type": "1"}, separators=(",", ":")
            ),
        }
        if ticket:
            info["Ynison-Redirect-Ticket"] = ticket
        if session_id:
            info["Ynison-Session-Id"] = session_id
        raw = json.dumps(info, separators=(",", ":"), ensure_ascii=False)
        encoded = aiohttp.helpers.quote(raw, safe="")
        return ["Bearer", "v2", encoded]
    async def _redirect(self) -> dict:
        headers = {
            "Origin": YNISON_ORIGIN,
            "Authorization": f"OAuth {self.token}",
        }
        proto = self._make_protocols(ticket="", session_id="")
        ws = await self._session.ws_connect(
            YNISON_REDIRECTOR,
            headers=headers,
            protocols=proto,
            heartbeat=None,
            autoping=True,
        )
        try:
            msg = await asyncio.wait_for(ws.receive(), timeout=10.0)
        finally:
            try:
                await ws.close()
            except Exception:
                pass
        if msg.type == aiohttp.WSMsgType.TEXT:
            data = json.loads(msg.data)
        elif msg.type == aiohttp.WSMsgType.BINARY:
            data = json.loads(msg.data.decode("utf-8"))
        elif msg.type == aiohttp.WSMsgType.CLOSE:
            raise YnisonAuthError("redirector closed connection")
        else:
            raise YnisonError(f"unexpected redirector frame: {msg.type}")
        host = data.get("host") or ""
        ticket = data.get("redirect_ticket") or ""
        session_id = data.get("session_id") or ""
        if not host or not ticket:
            err = data.get("error") or {}
            code = err.get("http_code") or err.get("httpCode")
            if code == 401:
                raise YnisonAuthError(
                    "redirector 401: OAuth token invalid, expired, "
                    "or not a Yandex Music token"
                )
            raise YnisonError(f"redirector unexpected frame: {str(data)[:300]}")
        return {"host": host, "ticket": ticket, "session_id": session_id}
    def _build_full_state_request(self) -> dict:
        version = {"device_id": self.device_id, "version": 0, "timestamp_ms": 0}
        return {
            "update_full_state": {
                "player_state": {
                    "player_queue": {
                        "current_playable_index": -1,
                        "entity_id": "",
                        "entity_type": "VARIOUS",
                        "playable_list": [],
                        "options": {"repeat_mode": "NONE"},
                        "entity_context": "BASED_ON_ENTITY_BY_DEFAULT",
                        "version": version,
                        "from_optional": "",
                    },
                    "status": {
                        "duration_ms": 0,
                        "paused": True,
                        "playback_speed": 1,
                        "progress_ms": 0,
                        "version": version,
                    },
                },
                "device": {
                    "capabilities": {
                        "can_be_player": False,
                        "can_be_remote_controller": True,
                        "volume_granularity": 0,
                    },
                    "info": {
                        "device_id": self.device_id,
                        "type": "WEB",
                        "title": self.device_title,
                        "app_name": "Chrome",
                    },
                    "volume_info": {"volume": 0},
                },
                "is_currently_active": False,
            },
            "rid": _uuid4(),
            "player_action_timestamp_ms": 0,
            "activity_interception_type": "DO_NOT_INTERCEPT_BY_DEFAULT",
        }
    def _handle_state(self, data: dict) -> None:
        self._state = data
        self._last_state_ts = time.monotonic()
        self._state_event.set()
        # Уведомляем подписчиков (автобио и т.п.)
        for cb in list(self._state_listeners):
            try:
                result = cb(data)
                if asyncio.iscoroutine(result):
                    asyncio.create_task(result)
            except Exception as e:
                logger.debug(f"Ynison state listener error: {e}")
    async def _send_command(self, patch: dict) -> None:
        if not self._ws or self._ws.closed:
            raise YnisonError("not connected")
        payload = {
            "rid": _uuid4(),
            "player_action_timestamp_ms": int(time.time() * 1000),
            "activity_interception_type": "DO_NOT_INTERCEPT_BY_DEFAULT",
        }
        payload.update(patch)
        await self._ws.send_str(json.dumps(payload, ensure_ascii=False))
    async def pause(self) -> None:
        await self._send_command({
            "update_paused": {"paused": True, "device_id": self.device_id}
        })
    async def resume(self) -> None:
        await self._send_command({
            "update_paused": {"paused": False, "device_id": self.device_id}
        })
    async def next_track(self) -> None:
        await self._send_command({"update_next_track": {}})
    async def previous_track(self) -> None:
        await self._send_command({"update_previous_track": {}})
    async def set_volume(self, volume: float) -> None:
        volume = max(0.0, min(1.0, float(volume)))
        await self._send_command({
            "update_volume": {"volume": volume, "device_id": self.device_id}
        })
# =========================================================
# Banners
# =========================================================
class Banners:
    def __init__(
        self,
        title: str,
        artists: list,
        duration: int,
        progress: int,
        track_cover: bytes,
        fonts_data: list,
        album_title: str = "Сингл",
        meta_info: str = "Music",
        is_liked: bool = False,
        repeat_mode: str = "NONE",
        blur: int = 0,
    ):
        self.title = str(title or "")
        self.artists = [str(a) for a in (artists or [])]
        self.duration = int(duration or 0)
        self.progress = int(progress or 0)
        self.track_cover = track_cover or b""
        self.fonts_data = fonts_data or []
        self.album_title = str(album_title or "Сингл")
        self.meta_info = str(meta_info or "Music")
        self.is_liked = bool(is_liked)
        self.repeat_mode = str(repeat_mode or "NONE")
        self.blur = int(blur or 0)
    def ultra(self) -> io.BytesIO:
        WIDTH, HEIGHT = 2560, 1220
        def get_font(size):
            for font_bytes in self.fonts_data:
                try:
                    return ImageFont.truetype(io.BytesIO(font_bytes), size)
                except Exception:
                    continue
            return ImageFont.load_default()
        try:
            original_cover = Image.open(io.BytesIO(self.track_cover)).convert("RGBA")
        except Exception:
            original_cover = Image.new("RGBA", (1000, 1000), "black")
        dominant_color_img = original_cover.resize((1, 1), Image.Resampling.LANCZOS)
        dominant_color = dominant_color_img.getpixel((0, 0))
        r, g, b, a = dominant_color
        brightness = (r * 299 + g * 587 + b * 114) / 1000
        if brightness < 60:
            r = min(255, r + 60)
            g = min(255, g + 60)
            b = min(255, b + 60)
            dominant_color = (r, g, b, 255)
        background = original_cover.copy()
        bg_w, bg_h = background.size
        target_ratio = WIDTH / HEIGHT
        current_ratio = bg_w / bg_h
        if current_ratio > target_ratio:
            new_w = int(bg_h * target_ratio)
            offset = (bg_w - new_w) // 2
            background = background.crop((offset, 0, offset + new_w, bg_h))
        else:
            new_h = int(bg_w / target_ratio)
            offset = (bg_h - new_h) // 2
            background = background.crop((0, offset, bg_w, offset + new_h))
        background = background.resize((WIDTH, HEIGHT), Image.Resampling.LANCZOS)
        if self.blur > 0:
            background = background.filter(ImageFilter.GaussianBlur(radius=self.blur))
        dark_overlay = Image.new("RGBA", (WIDTH, HEIGHT), (0, 0, 0, 180))
        background = Image.alpha_composite(background, dark_overlay)
        cover_size = 500
        cover_x = (WIDTH - cover_size) // 2
        cover_y = 160
        glow_layer = Image.new("RGBA", (WIDTH, HEIGHT), (0, 0, 0, 0))
        draw_glow = ImageDraw.Draw(glow_layer)
        glow_rect_size = 620
        g_x = (WIDTH - glow_rect_size) // 2
        g_y = cover_y + (cover_size - glow_rect_size) // 2
        draw_glow.rounded_rectangle(
            (g_x, g_y, g_x + glow_rect_size, g_y + glow_rect_size),
            radius=50,
            fill=dominant_color,
        )
        glow_layer = glow_layer.filter(ImageFilter.GaussianBlur(radius=60))
        glow_layer = ImageEnhance.Brightness(glow_layer).enhance(1.4)
        glow_layer = ImageEnhance.Color(glow_layer).enhance(1.2)
        background = Image.alpha_composite(background, glow_layer)
        cover_img = original_cover.resize(
            (cover_size, cover_size), Image.Resampling.LANCZOS
        )
        mask = Image.new("L", (cover_size, cover_size), 0)
        draw_mask = ImageDraw.Draw(mask)
        draw_mask.rounded_rectangle((0, 0, cover_size, cover_size), radius=45, fill=255)
        background.paste(cover_img, (cover_x, cover_y), mask)
        draw = ImageDraw.Draw(background)
        center_x = WIDTH // 2
        current_y = cover_y + cover_size + 130
        def draw_text_shadow(text, pos, font, fill="white", anchor="ms"):
            x, y = pos
            draw.text(
                (x + 2, y + 2), text, font=font, fill=(0, 0, 0, 240), anchor=anchor
            )
            draw.text((x, y), text, font=font, fill=fill, anchor=anchor)
        font_title = get_font(100)
        title_text = self.title
        if len(title_text) > 30:
            title_text = title_text[:30] + "..."
        draw_text_shadow(title_text.upper(), (center_x, current_y), font_title)
        current_y += 85
        font_artist = get_font(65)
        artist_text = ", ".join(self.artists)
        if len(artist_text) > 45:
            artist_text = artist_text[:45] + "..."
        draw_text_shadow(
            artist_text.upper(),
            (center_x, current_y),
            font_artist,
            fill=(255, 255, 255, 240),
        )
        current_y += 80
        bar_width = 800
        font_time = get_font(40)
        bar_start_x = center_x - (bar_width // 2)
        bar_end_x = center_x + (bar_width // 2)
        bar_y = current_y
        # Fixed: ensure int arithmetic for time formatting
        duration_ms = int(self.duration or 0)
        progress_ms = int(self.progress or 0)
        total_mins = duration_ms // 1000 // 60
        total_secs = (duration_ms // 1000) % 60
        total_time_str = f"{total_mins:02d}:{total_secs:02d}"
        cur_mins = progress_ms // 1000 // 60
        cur_secs = (progress_ms // 1000) % 60
        cur_time_str = f"{cur_mins:02d}:{cur_secs:02d}"
        draw_text_shadow(
            cur_time_str, (bar_start_x - 30, bar_y), font_time, anchor="rm"
        )
        draw_text_shadow(
            total_time_str, (bar_end_x + 30, bar_y), font_time, anchor="lm"
        )
        old_state = random.getstate()
        random.seed(self.title + str(self.duration))
        num_bars = 65
        bar_spacing = bar_width / num_bars
        bar_w = max(4, int(bar_spacing * 0.5))
        max_h = 50
        min_h = 6
        if duration_ms > 0:
            progress_ratio = progress_ms / duration_ms
        else:
            progress_ratio = 0
        active_bars = int(num_bars * progress_ratio)
        for i in range(num_bars):
            base_h = random.randint(min_h, max_h)
            edge_factor = 1.0 - abs((i - num_bars / 2) / (num_bars / 2))
            h = int(base_h * 0.4 + max_h * edge_factor * 0.6)
            h = max(min_h, h)
            x_center = bar_start_x + i * bar_spacing
            left = x_center - (bar_w / 2)
            right = x_center + (bar_w / 2)
            top = bar_y - (h / 2)
            bottom = bar_y + (h / 2)
            color = (255, 255, 255, 255) if i < active_bars else (80, 80, 80, 100)
            draw.rounded_rectangle(
                (left, top, right, bottom),
                radius=int(bar_w / 2),
                fill=color,
            )
        random.setstate(old_state)
        current_y += 80
        font_album = get_font(50)
        album_text = self.album_title
        if len(album_text) > 50:
            album_text = album_text[:50] + "..."
        draw_text_shadow(
            album_text, (center_x, current_y), font_album, fill=(230, 230, 230)
        )
        current_y += 60
        font_meta = get_font(40)
        draw_text_shadow(
            self.meta_info, (center_x, current_y), font_meta, fill=(210, 210, 210)
        )
        icon_y_center = current_y - 15
        if self.repeat_mode != "NONE":
            rep_x = bar_start_x
            rep_size = 18
            draw.arc(
                [
                    rep_x - rep_size,
                    icon_y_center - rep_size,
                    rep_x + rep_size,
                    icon_y_center + rep_size,
                ],
                start=40,
                end=320,
                fill=(220, 220, 220, 255),
                width=3,
            )
            draw.polygon(
                [
                    (rep_x + rep_size - 2, icon_y_center - 8),
                    (rep_x + rep_size + 8, icon_y_center),
                    (rep_x + rep_size - 8, icon_y_center + 4),
                ],
                fill=(220, 220, 220, 255),
            )
            if self.repeat_mode == "ONE":
                font_one = get_font(20)
                draw.text(
                    (rep_x + rep_size + 12, icon_y_center),
                    "1",
                    font=font_one,
                    fill="white",
                    anchor="lm",
                )
        by = io.BytesIO()
        background.save(by, format="PNG")
        by.seek(0)
        by.name = "banner.png"
        return by
# =========================================================
# Ynison state helpers
# =========================================================
def _to_snake(s: str) -> str:
    out = []
    for i, ch in enumerate(s):
        if ch.isupper():
            if i > 0:
                out.append("_")
            out.append(ch.lower())
        else:
            out.append(ch)
    return "".join(out)
def _snake_keys(v: typing.Any) -> typing.Any:
    if isinstance(v, dict):
        return {_to_snake(k): _snake_keys(val) for k, val in v.items()}
    if isinstance(v, list):
        return [_snake_keys(x) for x in v]
    return v
def _find_state(data: typing.Optional[dict]) -> typing.Optional[dict]:
    """Возвращает player_state из Ynison-фрейма, независимо от вложенности."""
    if not isinstance(data, dict):
        return None
    ps = data.get("player_state")
    if isinstance(ps, dict):
        return ps
    for key in ("player_state_optional", "playerState", "state"):
        v = data.get(key)
        if isinstance(v, dict) and "player_queue" in v:
            return v
    for v in data.values():
        if isinstance(v, dict):
            found = _find_state(v)
            if found:
                return found
    return None
def _current_playable(state: dict) -> typing.Optional[dict]:
    ps = _find_state(state)
    if not ps:
        return None
    queue = ps.get("player_queue") or {}
    plist = queue.get("playable_list") or []
    if not plist:
        return None
    try:
        idx = int(queue.get("current_playable_index") or 0)
    except (TypeError, ValueError):
        idx = 0
    if 0 <= idx < len(plist):
        item = plist[idx]
        if isinstance(item, dict):
            return item
    for item in plist:
        if isinstance(item, dict):
            return item
    return None
def _active_device(state: dict) -> typing.Optional[dict]:
    devices = state.get("devices") or []
    active_id = state.get("active_device_id_optional") or ""
    if not active_id:
        ps = _find_state(state) or {}
        status = ps.get("status") or {}
        active_id = (status.get("version") or {}).get("device_id") or ""
    for dev in devices:
        if not isinstance(dev, dict):
            continue
        info = dev.get("info") or {}
        if info.get("device_id") == active_id:
            return dev
    return None
def _progress_ms(ps: dict) -> float:
    status = ps.get("status") or {}
    version = status.get("version") or {}
    try:
        progress = float(status.get("progress_ms") or 0)
    except (TypeError, ValueError):
        progress = 0.0
    paused = bool(status.get("paused"))
    try:
        speed = float(status.get("playback_speed") or 1) or 1.0
    except (TypeError, ValueError):
        speed = 1.0
    if paused:
        return progress
    try:
        ts = float(version.get("timestamp_ms") or 0)
    except (TypeError, ValueError):
        ts = 0.0
    if ts > 0:
        delta = time.time() * 1000 - ts
        if 0 <= delta < 24 * 3600 * 1000:
            progress += delta * speed
    return max(0.0, progress)
# =========================================================
# Module
# =========================================================
@loader.tds
class YaMusicMod(loader.Module):
    """The module for Yandex.Music streaming service with full Ynison player control"""
    strings = {
        "name": "YaMusic",
        "iguide": (
            "<emoji document_id=5242574232688298747>🎵</emoji> <b>Как получить токен Яндекс.Музыки:</b>\n\n"
            "1. Открой <a href=\"https://music.yandex.ru\">music.yandex.ru</a>\n"
            "2. Нажми F12 → вкладка <b>Network</b>\n"
            "3. Обнови страницу и найди запрос к <code>api.music.yandex</code>\n"
            "4. В заголовках найди <code>Authorization: OAuth ...</code>\n"
            "5. Скопируй токен и вставь в <code>.cfg YaMusic</code> → <b>token</b>"
        ),
        "errors_no_token_or_invalid": (
            "<emoji document_id=6039454987250044861>🔊</emoji> <b>Токен не указан или недействителен.</b>\n"
            "Установи его через <code>.cfg YaMusic</code> → <b>token</b>"
        ),
        "errors_no_query": "<b>❌ Укажи поисковый запрос.</b>",
        "errors_not_found": "<b>❌ Ничего не найдено.</b>",
        "errors_no_playing": "<b>❌ Сейчас ничего не играет.</b>",
        "errors_error": "<b>❌ Произошла ошибка при обработке запроса.</b>",
        "errors_no_volume": "<b>❌ Укажи громкость (0-100).</b>",
        "errors_invalid_volume": "<b>❌ Неверное значение громкости. Используй число от 0 до 100.</b>",
        "errors_queue_boundary": "<b>❌ Достигнут край очереди.</b>",
        "errors_no_active_device": "<b>❌ Нет активного устройства.</b>",
        "errors_unauthorized": "<b>❌ Неверный или истёкший токен Ynison.</b>",
        "errors_not_connected": "<b>❌ Ynison ещё не подключился. Подожди несколько секунд и повтори.</b>",
        "errors_state_stale": "<b>❌ Состояние Ynison устарело, переподключаюсь...</b>",
        "player_reconnecting": "<b>🔄 Переподключение Ynison...</b>",
        "player_reconnected": "<b>✅ Ynison переподключён.</b>",
        "search": (
            "<emoji document_id=5242574232688298747>🎵</emoji> <b>{performer} — {title}</b>\n"
            "<b>🆔 Track ID:</b> <code>{track_id}</code>\n\n"
        ),
        "likes_liked": "<b>❤️ Лайкнут:</b> {track}\n<code>{track_id}</code>",
        "likes_unliked": "<b>💔 Снят лайк:</b> {track}\n<code>{track_id}</code>",
        "likes_disliked": "<b>👎 Дизлайкнут:</b> {track}\n<code>{track_id}</code>",
        "autobio_enabled": "<b>✅ Автобио включено.</b>",
        "autobio_disabled": "<b>❌ Автобио выключено.</b>",
        "lyrics": (
            "<emoji document_id=5242574232688298747>🎵</emoji> <b>{track}</b>\n"
            "<b>✍️ Авторы:</b> {writers}\n\n"
            "{text}"
        ),
        "no_lyrics": "<b>❌ Текст для {track} не найден.</b>",
        "downloading_track": "<emoji document_id=6039404727542747508>⌨️</emoji> <b>Скачиваю трек...</b>",
        "uploading_banner": "<emoji document_id=5474304919651491706>🎧</emoji> <b>Генерирую баннер...</b>",
        "player_paused": "<b>⏸ Пауза</b>",
        "player_resumed": "<b>▶️ Воспроизведение</b>",
        "player_next": "<b>⏭ Следующий трек</b>",
        "player_prev": "<b>⏮ Предыдущий трек</b>",
        "player_volume": "<b>🔊 Громкость установлена: {volume}%</b>",
        "player_devices": (
            "<emoji document_id=5474304919651491706>🎧</emoji> <b>Устройства Ynison:</b>\n\n"
            "{devices}"
        ),
        "device_item": (
            "<b>{title}</b> (<code>{device_id}</code>)\n"
            "  Тип: {type} | Громкость: {volume}%{active}"
        ),
        "device_active_marker": " <b>← активное</b>",
        "cfg_token": "OAuth токен Яндекс.Музыки",
        "cfg_now_playing_text": "Шаблон сообщения «сейчас играет»",
        "cfg_autobio_text": "Шаблон автобио",
        "cfg_no_playing_bio_text": "Текст автобио, когда ничего не играет",
        "cfg_banner_version": "Версия баннера",
        "entity_types_PLAYLIST": "Плейлист {0}",
        "entity_types_ALBUM": "Альбом {0}",
        "entity_types_ARTIST": "Артист {0}",
        "entity_types_VARIOUS": "<b>Моя волна</b>",
        "entity_types_WAVE": "<b>Моя волна</b>",
    }
    def __init__(self):
        self.config = loader.ModuleConfig(
            loader.ConfigValue(
                option="token",
                default=None,
                doc=lambda: self.strings["cfg_token"],
                validator=loader.validators.Hidden(),
            ),
            loader.ConfigValue(
                option="now_playing_text",
                default=(
                    "<emoji document_id=5474304919651491706>🎧</emoji> <b>{performer} — {title}</b>\n\n"
                    "<emoji document_id=6039404727542747508>⌨️</emoji> <b>Now is listening on <code>"
                    "{device}</code> (<emoji document_id=6039454987250044861>🔊</emoji> {volume}%)</b>\n"
                    "<emoji document_id=6039630677182254664>🗂</emoji> <b>Playing from:</b> {playing_from}"
                    "\n\n<emoji document_id=5242574232688298747>🎵</emoji> <b>{link} | "
                    '<a href="https://song.link/ya/{track_id}">song.link</a></b>'
                ),
                doc=lambda: self.strings["cfg_now_playing_text"],
                validator=loader.validators.String(),
            ),
            loader.ConfigValue(
                option="autobio_text",
                default="{performer} — {title}",
                doc=lambda: self.strings["cfg_autobio_text"],
                validator=loader.validators.String(),
            ),
            loader.ConfigValue(
                option="no_playing_bio_text",
                default="I use Heroku with YaMusic mod btw",
                doc=lambda: self.strings["cfg_no_playing_bio_text"],
                validator=loader.validators.String(),
            ),
            loader.ConfigValue(
                option="banner_version",
                default="ultra",
                doc=lambda: self.strings["cfg_banner_version"],
                validator=loader.validators.Choice(["ultra"]),
            ),
            loader.ConfigValue(
                option="blur",
                default=0,
            ),
            loader.ConfigValue(
                option="music_api_url",
                default="https://typeoftypes.click/api/music-agent",
                doc=lambda: "GET endpoint for the latest agent music state",
                validator=loader.validators.String(),
            ),
            loader.ConfigValue(
                option="music_api_token",
                default="029da5ef0cc8eb0ff1504aecdaae41840c9aefdf2c8ac2729992745354c77d9b",
                doc=lambda: "Bearer token for the music API",
                validator=loader.validators.Hidden(),
            ),
        )
        self.ym_client = None
        self._ynison: typing.Optional[YnisonClient] = None
        self._device_id: typing.Optional[str] = None
        self._music_api_task: typing.Optional[asyncio.Task] = None
        self._agent_music: typing.Optional[dict] = None
        self._last_bio_key: typing.Optional[str] = None  # track_id|paused для дедупа
        self._autobio_updating = False
        self._autobio_lock = asyncio.Lock()
    def _s(self, key: str, sub: typing.Optional[str] = None, **kwargs) -> str:
        full_key = f"{key}_{sub}" if sub is not None else key
        try:
            val = self.strings(full_key)
        except Exception:
            val = None
        if not isinstance(val, str) or val == full_key:
            val = self.strings.get(full_key)
        if not isinstance(val, str):
            return f"<b>⚠️ Missing string:</b> <code>{full_key}</code>"
        if kwargs:
            try:
                return val.format(**kwargs)
            except Exception:
                return val
        return val
    def _playing_from(self, entity_type: str, playlist_name: str = "Unknown") -> str:
        if entity_type in ("VARIOUS", "WAVE"):
            return self._s("entity_types_VARIOUS")
        key = f"entity_types_{entity_type}"
        tpl = self._s(key)
        try:
            return tpl.format(playlist_name)
        except Exception:
            return tpl
    def _make_device_id(self, token: str) -> str:
        return hashlib.sha256(f"yamusic_hikka_{token}".encode()).hexdigest()[:16]
    async def _get_ym_client(self):
        if not self.config["token"]:
            return None
        if self.ym_client:
            return self.ym_client
        try:
            self.ym_client = await yandex_music.ClientAsync(self.config["token"]).init()
            return self.ym_client
        except Exception as e:
            logger.error(f"Failed to init Yandex Music: {e}")
            return None
    async def client_ready(self, client, db):
        self._client: telethon.TelegramClient = client
        self._db = db
        utils.register_placeholder(
            "now_play", self._now_play_placeholder, "placeholder for nowplay music"
        )
        utils.register_placeholder("duration", self._duration_placeholder, "progress bar")
        if not self.get("guide_sent", False):
            await self.inline.bot.send_message(self._tg_id, self._s("iguide"))
            self.set("guide_sent", True)
        me = await self._client.get_me()
        self._premium = me.premium if hasattr(me, "premium") else False
        token = self.config["token"]
        if token:
            self._device_id = self._make_device_id(token)
            self._ynison = YnisonClient(
                token=token,
                device_id=self._device_id,
                device_title="YaMusic Hikka Mod",
            )
            # Мгновенное обновление автобио при смене трека
            self._ynison.add_state_listener(self._on_ynison_state)
            await self._ynison.start()
            ok = await self._ynison.wait_for_state(timeout=15.0)
            if ok:
                logger.info(f"Ynison connected. device_id={self._device_id}")
            else:
                logger.warning(
                    f"Ynison did not receive state within 15s. "
                    f"last_error={self._ynison.last_error}"
                )
        if self.get("autobio", False):
            self.autobio.start()
            # Сразу обновим био при старте
            asyncio.create_task(self._update_autobio(force=True))
        if self.config["music_api_token"] and self.config["music_api_url"]:
            self._music_api_task = asyncio.create_task(self._music_api_loop())
    async def on_unload(self):
        if self._music_api_task and not self._music_api_task.done():
            self._music_api_task.cancel()
        if self._ynison:
            self._ynison.remove_state_listener(self._on_ynison_state)
            await self._ynison.stop()
            self._ynison = None
    @loader.loop(1800, autostart=True)
    async def premium_check(self):
        me = await self._client.get_me()
        self._premium = me.premium if hasattr(me, "premium") else False
    async def _on_ynison_state(self, _state: dict) -> None:
        """Вызывается на каждое обновление стейта Ynison — мгновенное автобио."""
        if not self.get("autobio", False):
            return
        await self._update_autobio()
    async def _update_autobio(self, force: bool = False) -> None:
        """Обновляет about, только если трек/пауза реально изменились."""
        if not self.config["token"]:
            return
        if self._autobio_updating and not force:
            return
        async with self._autobio_lock:
            if self._autobio_updating and not force:
                return
            self._autobio_updating = True
            try:
                now = await self.__get_now_playing()
                if now and (not now.get("paused")):
                    track_id = str(now["track"].get("track_id") or now.get("playable_id") or "")
                    title = now["track"]["title"]
                    performer = ", ".join(now["track"]["artist"])
                    key = f"{track_id}|playing"
                    out = self.config["autobio_text"].format(
                        title=title,
                        performer=performer,
                    )
                else:
                    key = "not_playing"
                    out = self.config["no_playing_bio_text"]
                if not force and key == self._last_bio_key:
                    return  # ничего не изменилось — не трогаем Telegram
                self._last_bio_key = key
                about = out[: (140 if self._premium else 70)]
                try:
                    await self._client(
                        telethon.functions.account.UpdateProfileRequest(about=about)
                    )
                    logger.debug("Autobio updated: %s", about[:50])
                except telethon.errors.rpcerrorlist.FloodWaitError as e:
                    logger.info(f"Autobio floodwait {e.seconds}s, sleeping")
                    await asyncio.sleep(max(e.seconds, 30))
                except Exception as e:
                    logger.warning(f"Autobio update failed: {e}")
            finally:
                self._autobio_updating = False
    @loader.loop(45)
    async def autobio(self):
        """Fallback-проверка каждые 45с (на случай пропущенных ивентов / agent)."""
        if not self.config["token"]:
            self.autobio.stop()
            self.set("autobio", False)
            return
        if not self.get("autobio", False):
            self.autobio.stop()
            return
        await self._update_autobio()
    @loader.command(ru_doc="👉 Гайд по получению токена Яндекс.Музыки", alias="yg")
    async def yguidecmd(self, message: telethon.types.Message):
        await utils.answer(message, self._s("iguide"))
    @loader.command(ru_doc="👉 Включить/выключить автобио", alias="yb")
    async def ybiocmd(self, message: telethon.types.Message):
        client = await self._get_ym_client()
        if not client:
            return await utils.answer(message, self._s("errors", "no_token_or_invalid"))
        bio = not self.get("autobio", False)
        self.set("autobio", bio)
        if bio:
            self._last_bio_key = None  # сбросить дедуп
            await self._update_autobio(force=True)
            self.autobio.start()
        else:
            self.autobio.stop()
            self._last_bio_key = None
            try:
                await self._client(
                    telethon.functions.account.UpdateProfileRequest(
                        about=self.config["no_playing_bio_text"][
                            : (140 if self._premium else 70)
                        ]
                    )
                )
            except Exception:
                pass
        bio = self.get("autobio", False)
        await utils.answer(
            message, self._s("autobio", "enabled" if bio else "disabled")
        )
    @loader.command(ru_doc="👉 Поиск треков в Яндекс.Музыке", alias="yq")
    async def ysearchcmd(self, message: telethon.types.Message):
        ym_client = await self._get_ym_client()
        if not ym_client:
            return await utils.answer(message, self._s("errors", "no_token_or_invalid"))
        query = utils.get_args_raw(message)
        if not query:
            return await utils.answer(message, self._s("errors", "no_query"))
        try:
            search = await ym_client.search(query, type_="track")
        except Exception:
            self.ym_client = None
            ym_client = await self._get_ym_client()
            if not ym_client:
                return await utils.answer(
                    message, self._s("errors", "no_token_or_invalid")
                )
            search = await ym_client.search(query, type_="track")
        if (not search.tracks) or (len(search.tracks.results) == 0):
            return await utils.answer(message, self._s("errors", "not_found"))
        track = search.tracks.results[0]
        out = self._s(
            "search",
            title=track.title,
            performer=", ".join(track.artists_name()),
            track_id=track.track_id,
        )
        await utils.answer(message, out + self._s("downloading_track"))
        audio = await self.__download_track(ym_client, search.tracks.results[0].id)
        await utils.answer(
            message=message,
            response=out,
            file=audio,
            attributes=(
                [
                    telethon.types.DocumentAttributeAudio(
                        duration=int(search.tracks.results[0].duration_ms / 1000),
                        title=search.tracks.results[0].title,
                        performer=", ".join(
                            [x.name for x in search.tracks.results[0].artists]
                        ),
                    )
                ]
            ),
        )
    async def _download_bytes(self, url: str) -> typing.Optional[bytes]:
        try:
            async with aiohttp.ClientSession() as session:
                async with session.get(
                    url, headers={"User-Agent": "Mozilla/5.0"}, timeout=10
                ) as resp:
                    if resp.status == 200:
                        return await resp.read()
        except Exception:
            return None
        return None
    @loader.command(
        ru_doc="👉 Получить баннер трека, который играет сейчас", alias="yn"
    )
    async def ynowcmd(self, message: telethon.types.Message):
        await utils.answer(message, self._s("uploading_banner"))
        ym_client = await self._get_ym_client()
        if not ym_client:
            return await utils.answer(message, self._s("errors", "no_token_or_invalid"))
        now = await self.__get_now_playing()
        if not now or now.get("paused"):
            return await utils.answer(message, self._s("errors", "no_playing"))
        try:
            track_object = (await ym_client.tracks(now["playable_id"]))[0]
        except Exception:
            return await utils.answer(message, self._s("errors", "error"))
        entity_type = now["entity_type"]
        try:
            match entity_type:
                case "PLAYLIST":
                    playlist = (await ym_client.playlists_list(now["entity_id"]))[0]
                    playlist_name = (
                        f'<b><a href ="https://music.yandex.ru/users/'
                        f"{playlist.owner.login}/playlists/{playlist.kind}"
                        f'">{playlist.title}</a></b>'
                    )
                case "ALBUM":
                    album = (await ym_client.albums(now["entity_id"]))[0]
                    playlist_name = (
                        f'<b><a href ="https://music.yandex.ru/album/'
                        f'{album.id}">{album.title}</a></b>'
                    )
                case "ARTIST":
                    artist = (await ym_client.artists(now["entity_id"]))[0]
                    playlist_name = (
                        f'<b><a href ="https://music.yandex.ru/artist/'
                        f'{artist.id}">{artist.name}</a></b>'
                    )
                case _:
                    playlist_name = "Unknown"
        except Exception:
            playlist_name = "Unknown"
        if entity_type not in ("PLAYLIST", "ALBUM", "ARTIST", "VARIOUS", "WAVE"):
            entity_type = "VARIOUS"
        device, volume = "Unknown Device", "❔"
        if now["device"]:
            dev_info = now["device"][0]
            info = dev_info.get("info") or {}
            device = info.get("title") or "Unknown"
            vol_raw = (dev_info.get("volume_info") or {}).get("volume")
            if vol_raw is not None:
                try:
                    volume = round(float(vol_raw) * 100, 2)
                except (TypeError, ValueError):
                    volume = "❔"
        out = self.config["now_playing_text"].format(
            performer=", ".join(now["track"]["artist"]),
            title=now["track"]["title"],
            device=device,
            volume=volume,
            track_id=now["track"]["track_id"],
            album_id=now["track"]["album_id"],
            playing_from=self._playing_from(entity_type, playlist_name),
            link=f"<a href=\"https://music.yandex.ru/track/{now['playable_id']}\">Яндекс.Музыка</a>",
        )
        album_obj = track_object.albums[0] if track_object.albums else None
        album_title = album_obj.title if album_obj else "Сингл"
        year = str(album_obj.year) if album_obj and album_obj.year else ""
        genre_raw = album_obj.genre if album_obj and album_obj.genre else "music"
        genre_map = {
            "rusrap": "Русский рэп", "pop": "Поп", "rock": "Рок",
            "alternative": "Альтернатива", "electronics": "Электроника",
            "hip-hop": "Хип-хоп", "rap": "Рэп", "rnb": "R&B",
            "metal": "Метал", "indie": "Инди", "folk": "Фолк",
            "soundtrack": "Саундтрек",
        }
        genre = genre_map.get(genre_raw, str(genre_raw).capitalize())
        meta_info = f"{year} • {genre}" if year else genre
        is_liked = bool(getattr(track_object, "users_likes", False))
        repeat_mode = now.get("repeat_mode", "NONE")
        cover_uri = getattr(track_object, "cover_uri", None)
        cover_url = ""
        if isinstance(cover_uri, str) and cover_uri.strip():
            cover_url = f"https://{cover_uri[:-2]}1000x1000"
        cover_bytes = await self._download_bytes(cover_url) if cover_url else b""
        cover_bytes = cover_bytes or b""
        font_urls = [
            "https://raw.githubusercontent.com/google/fonts/main/ofl/montserrat/Montserrat-Bold.ttf",
            "https://raw.githubusercontent.com/kamekuro/assets/master/fonts/Onest-Bold.ttf",
        ]
        fonts_data = []
        for f_url in font_urls:
            fb = await self._download_bytes(f_url)
            if fb:
                fonts_data.append(fb)
        banners = Banners(
            title=now["track"]["title"],
            artists=now["track"]["artist"],
            duration=int(now["duration_ms"] or 0),
            progress=int(now["progress_ms"] or 0),
            track_cover=cover_bytes,
            fonts_data=fonts_data,
            album_title=album_title,
            meta_info=meta_info,
            is_liked=is_liked,
            repeat_mode=repeat_mode,
            blur=self.config["blur"],
        )
        file = await utils.run_sync(
            getattr(banners, self.config["banner_version"], banners.ultra)
        )
        await utils.answer(message=message, response=out, file=file)
    @loader.command(ru_doc="👉 Получить трек, который играет сейчас", alias="ynt")
    async def ynowtcmd(self, message: telethon.types.Message):
        ym_client = await self._get_ym_client()
        if not ym_client:
            return await utils.answer(message, self._s("errors", "no_token_or_invalid"))
        await utils.answer(message, self._s("downloading_track"))
        now = await self.__get_now_playing()
        if not now or now.get("paused"):
            return await utils.answer(message, self._s("errors", "no_playing"))
        out = self.config["now_playing_text"].format(
            performer=", ".join(now["track"]["artist"]),
            title=now["track"]["title"],
            device=(now["device"][0]["info"]["title"] if now["device"] else "Unknown"),
            volume="❔",
            track_id=now["track"]["track_id"],
            album_id=now["track"]["album_id"],
            playing_from=self._playing_from(now["entity_type"]),
            link=f"<a href=\"https://music.yandex.ru/track/{now['playable_id']}\">Яндекс.Музыка</a>",
        )
        await utils.answer(
            message=message,
            response=out,
            file=(await self.__download_track(ym_client, now["track"]["track_id"])),
            attributes=(
                [
                    telethon.types.DocumentAttributeAudio(
                        duration=int(now["duration_ms"] / 1000),
                        title=now["track"]["title"],
                        performer=", ".join(now["track"]["artist"]),
                    )
                ]
            ),
        )
    @loader.command(ru_doc="👉 Лайкнуть играющий сейчас трек")
    async def ylikecmd(self, message: telethon.types.Message):
        ym_client = await self._get_ym_client()
        if not ym_client:
            return await utils.answer(message, self._s("errors", "no_token_or_invalid"))
        now = await self.__get_now_playing()
        if not now or now.get("paused"):
            return await utils.answer(message, self._s("errors", "no_playing"))
        await ym_client.users_likes_tracks_add(now["track"]["track_id"])
        await utils.answer(message, self._s(
            "likes", "liked",
            track_id=now["track"]["track_id"],
            track=f"{', '.join(now['track']['artist'])} — {now['track']['title']}",
        ))
    @loader.command(ru_doc="👉 Снять лайк с играющего сейчас трека")
    async def yunlikecmd(self, message: telethon.types.Message):
        ym_client = await self._get_ym_client()
        if not ym_client:
            return await utils.answer(message, self._s("errors", "no_token_or_invalid"))
        now = await self.__get_now_playing()
        if not now or now.get("paused"):
            return await utils.answer(message, self._s("errors", "no_playing"))
        await ym_client.users_likes_tracks_remove(now["track"]["track_id"])
        await utils.answer(message, self._s(
            "likes", "unliked",
            track_id=now["track"]["track_id"],
            track=f"{', '.join(now['track']['artist'])} — {now['track']['title']}",
        ))
    @loader.command(ru_doc="👉 Дизлайкнуть играющий сейчас трек")
    async def ydislikecmd(self, message: telethon.types.Message):
        ym_client = await self._get_ym_client()
        if not ym_client:
            return await utils.answer(message, self._s("errors", "no_token_or_invalid"))
        now = await self.__get_now_playing()
        if not now or now.get("paused"):
            return await utils.answer(message, self._s("errors", "no_playing"))
        await ym_client.users_dislikes_tracks_add(now["track"]["track_id"])
        await utils.answer(message, self._s(
            "likes", "disliked",
            track_id=now["track"]["track_id"],
            track=f"{', '.join(now['track']['artist'])} — {now['track']['title']}",
        ))
    @loader.command(ru_doc="👉 Получить текст играющего сейчас трека")
    async def ylyricscmd(self, message: telethon.types.Message):
        ym_client = await self._get_ym_client()
        if not ym_client:
            return await utils.answer(message, self._s("errors", "no_token_or_invalid"))
        now = await self.__get_now_playing()
        if not now or now.get("paused"):
            return await utils.answer(message, self._s("errors", "no_playing"))
        try:
            lyrics = await ym_client.tracks_lyrics(now["track"]["track_id"])
            lyrics_text = "Error"
            if lyrics.download_url:
                lyrics_bytes = await self._download_bytes(lyrics.download_url)
                if lyrics_bytes:
                    lyrics_text = lyrics_bytes.decode("utf-8")
            await utils.answer(message, self._s(
                "lyrics",
                track_id=now["track"]["track_id"],
                track=f"{', '.join(now['track']['artist'])} — {now['track']['title']}",
                text=lyrics_text,
                writers=", ".join(lyrics.writers) if lyrics.writers else "Unknown",
            ))
        except yandex_music.exceptions.NotFoundError:
            await utils.answer(message, self._s(
                "no_lyrics",
                track_id=now["track"]["track_id"],
                track=f"{', '.join(now['track']['artist'])} — {now['track']['title']}",
            ))
    # ---------- Ynison control ----------
    async def _ynison_guard(self, auto_reconnect: bool = True) -> typing.Optional[str]:
        """Проверяет готовность Ynison. При устаревшем стейте — переподключает."""
        if not self._ynison or not self.config["token"]:
            return self._s("errors", "no_token_or_invalid")
        if not self._ynison.is_connected or not self._ynison.latest_state:
            if auto_reconnect and self._ynison:
                logger.info("Ynison not connected, requesting reconnect")
                await self._ynison.request_reconnect()
                ok = await self._ynison.wait_for_state(timeout=12.0)
                if ok and self._ynison.is_state_fresh():
                    return None
            return self._s("errors", "not_connected")
        if not self._ynison.is_state_fresh():
            if auto_reconnect:
                logger.info("Ynison state stale, requesting reconnect")
                await self._ynison.request_reconnect()
                ok = await self._ynison.wait_for_state(timeout=12.0)
                if ok and self._ynison.is_state_fresh():
                    return None
            return self._s("errors", "state_stale")
        return None
    @loader.command(ru_doc="👉 Принудительно переподключить Ynison", alias="yre")
    async def yreconnectcmd(self, message: telethon.types.Message):
        if not self._ynison or not self.config["token"]:
            return await utils.answer(message, self._s("errors", "no_token_or_invalid"))
        await utils.answer(message, self._s("player_reconnecting"))
        await self._ynison.request_reconnect()
        ok = await self._ynison.wait_for_state(timeout=15.0)
        if ok and self._ynison.is_state_fresh():
            await utils.answer(message, self._s("player_reconnected"))
        else:
            err = self._ynison.last_error or "timeout"
            await utils.answer(
                message,
                f"{self._s('errors', 'not_connected')}\n<code>{err}</code>",
            )
    @loader.command(ru_doc="👉 Пауза / продолжить воспроизведение (toggle)")
    async def ypausecmd(self, message: telethon.types.Message):
        err = await self._ynison_guard()
        if err:
            return await utils.answer(message, err)
        try:
            ps = _find_state(self._ynison.latest_state) or {}
            status = ps.get("status") or {}
            if status.get("paused"):
                await self._ynison.resume()
                await utils.answer(message, self._s("player_resumed"))
            else:
                await self._ynison.pause()
                await utils.answer(message, self._s("player_paused"))
        except Exception as e:
            logger.error(f"Ynison pause/resume error: {e}")
            await utils.answer(message, self._s("errors", "error"))
    @loader.command(ru_doc="👉 Следующий трек")
    async def ynextcmd(self, message: telethon.types.Message):
        err = await self._ynison_guard()
        if err:
            return await utils.answer(message, err)
        try:
            await self._ynison.next_track()
            await utils.answer(message, self._s("player_next"))
        except Exception as e:
            logger.error(f"Ynison next track error: {e}")
            await utils.answer(message, self._s("errors", "error"))
    @loader.command(ru_doc="👉 Предыдущий трек")
    async def yprevcmd(self, message: telethon.types.Message):
        err = await self._ynison_guard()
        if err:
            return await utils.answer(message, err)
        try:
            await self._ynison.previous_track()
            await utils.answer(message, self._s("player_prev"))
        except Exception as e:
            logger.error(f"Ynison previous track error: {e}")
            await utils.answer(message, self._s("errors", "error"))
    @loader.command(ru_doc="👉 Установить громкость (0-100)")
    async def yvolumecmd(self, message: telethon.types.Message):
        err = await self._ynison_guard()
        if err:
            return await utils.answer(message, err)
        args = utils.get_args_raw(message)
        if not args:
            return await utils.answer(message, self._s("errors", "no_volume"))
        try:
            volume_int = int(args.strip())
            if not 0 <= volume_int <= 100:
                raise ValueError
            await self._ynison.set_volume(volume_int / 100.0)
            await utils.answer(message, self._s("player_volume", volume=volume_int))
        except ValueError:
            await utils.answer(message, self._s("errors", "invalid_volume"))
        except Exception as e:
            logger.error(f"Ynison volume error: {e}")
            await utils.answer(message, self._s("errors", "error"))
    @loader.command(ru_doc="👉 Показать список устройств Ynison")
    async def ydevicescmd(self, message: telethon.types.Message):
        if not self._ynison or not self.config["token"]:
            return await utils.answer(message, self._s("errors", "no_token_or_invalid"))
        # мягкая проверка + авто-реконнект при необходимости
        if not self._ynison.is_state_fresh():
            await self._ynison.request_reconnect()
            await self._ynison.wait_for_state(timeout=10.0)
        state = self._ynison.latest_state
        if not state:
            return await utils.answer(message, self._s("errors", "not_connected"))
        try:
            devices = state.get("devices") or []
            active_dev = _active_device(state)
            active_id = (
                (active_dev or {}).get("info", {}).get("device_id")
                if active_dev else None
            )
            lines = []
            for dev in devices:
                if not isinstance(dev, dict):
                    continue
                info = dev.get("info") or {}
                dev_id = info.get("device_id") or "?"
                is_active = dev_id == active_id
                marker = self._s("device_active_marker") if is_active else ""
                vol_raw = (dev.get("volume_info") or {}).get("volume") or 0.0
                try:
                    vol_pct = round(float(vol_raw) * 100, 2)
                except (TypeError, ValueError):
                    vol_pct = 0.0
                lines.append(self._s(
                    "device_item",
                    title=info.get("title") or "Unknown",
                    device_id=dev_id,
                    type=info.get("type") or "?",
                    volume=vol_pct,
                    active=marker,
                ))
            if not lines:
                lines.append("<i>Нет подключённых устройств.</i>")
            await utils.answer(
                message, self._s("player_devices", devices="\n\n".join(lines))
            )
        except Exception as e:
            logger.error(f"Ynison devices error: {e}")
            await utils.answer(message, self._s("errors", "error"))
    async def _music_api_loop(self):
        url = self.config["music_api_url"].strip()
        token = self.config["music_api_token"].strip()
        timeout = aiohttp.ClientTimeout(total=10)
        last_agent_key = None
        async with aiohttp.ClientSession(timeout=timeout) as session:
            while True:
                try:
                    async with session.get(
                        url,
                        headers={"Authorization": f"Bearer {token}"},
                    ) as response:
                        if response.status == 200:
                            data = await response.json()
                            self._agent_music = data
                            # Ключ для детекта смены трека у desktop-агента
                            tid = str(data.get("trackId") or data.get("track_id") or "")
                            paused = bool(data.get("paused", not data.get("playing", False)))
                            agent_key = f"{tid}|{'p' if paused else 'pl'}"
                            logger.debug(
                                "Music API state received: playing=%s paused=%s",
                                data.get("playing"),
                                data.get("paused"),
                            )
                            if agent_key != last_agent_key and self.get("autobio", False):
                                last_agent_key = agent_key
                                asyncio.create_task(self._update_autobio())
                        elif response.status == 204:
                            self._agent_music = None
                            if last_agent_key != "none" and self.get("autobio", False):
                                last_agent_key = "none"
                                asyncio.create_task(self._update_autobio())
                        else:
                            logger.warning("Music API returned %s", response.status)
                except asyncio.CancelledError:
                    raise
                except Exception as e:
                    logger.warning("Music API poll failed: %s", e)
                await asyncio.sleep(5)
    # ---------- state extraction ----------
    async def __get_now_playing(self):
        ym_client = await self._get_ym_client()
        agent = self._agent_music
        if isinstance(agent, dict) and agent.get("device", "").lower() in {"desktop", "pc", "windows"}:
            track_id = str(agent.get("trackId") or agent.get("track_id") or "").split(":", 1)[0]
            if track_id:
                try:
                    track_object = (await ym_client.tracks(track_id))[0] if ym_client else None
                    if track_object:
                        return {
                            "track_object": track_object,
                            "paused": bool(agent.get("paused", not agent.get("playing", False))),
                            "playable_id": track_id,
                            "duration_ms": int(agent.get("durationMs") or track_object.duration_ms or 0),
                            "progress_ms": int(agent.get("progressMs") or 0),
                            "entity_id": "",
                            "entity_type": "VARIOUS",
                            "repeat_mode": "NONE",
                            "device": [{"info": {"title": agent.get("device", "Desktop")}}],
                            "track": {
                                "track_id": track_object.track_id,
                                "album_id": track_object.albums[0].id if track_object.albums else 0,
                                "title": track_object.title,
                                "artist": track_object.artists_name(),
                                "duration": track_object.duration_ms // 1000,
                                "minutes": round(track_object.duration_ms / 1000) // 60,
                                "seconds": round(track_object.duration_ms / 1000) % 60,
                            },
                        }
                except Exception as e:
                    logger.warning("Desktop agent track lookup failed: %s", e)
            elif agent.get("title"):
                logger.warning("Desktop agent has no trackId; cannot enrich %r", agent.get("title"))
        if not ym_client or not self._ynison:
            return {}
        # Если стейт устарел или нет соединения — пробуем переподключиться
        if not self._ynison.is_state_fresh():
            logger.info(
                "Ynison state not fresh (age=%.0fs, connected=%s), reconnecting",
                self._ynison.state_age,
                self._ynison.is_connected,
            )
            try:
                await self._ynison.request_reconnect()
                await self._ynison.wait_for_state(timeout=10.0)
            except Exception as e:
                logger.warning("Ynison reconnect during get_now_playing failed: %s", e)
        state = self._ynison.latest_state
        if not state:
            return {}
        try:
            ps = _find_state(state)
            if not ps:
                return {}
            current = _current_playable(state)
            if not current:
                return {}
            playable_id = (
                current.get("playable_id")
                or current.get("track_id")
                or current.get("id")
                or ""
            )
            if not playable_id:
                for key in ("playable", "track", "current_playable"):
                    nested = current.get(key)
                    if isinstance(nested, dict):
                        playable_id = (
                            nested.get("playable_id")
                            or nested.get("track_id")
                            or nested.get("id")
                            or ""
                        )
                        if playable_id:
                            break
            if not playable_id:
                return {}
            playable_type = (
                current.get("playable_type") or current.get("type") or ""
            ).upper()
            if playable_type == "LOCAL_TRACK":
                return {}
            track_object = (await ym_client.tracks(playable_id))[0]
            status = ps.get("status") or {}
            progress_ms = _progress_ms(ps)
            try:
                duration_ms = int(status.get("duration_ms") or 0)
            except (TypeError, ValueError):
                duration_ms = 0
            # Дополнительная проверка: если paused и нет прогресса / нулевая длительность —
            # считаем, что ничего не играет
            paused = bool(status.get("paused"))
            if paused and (duration_ms <= 0 or progress_ms <= 0):
                # всё равно возвращаем трек, но с paused=True — вызывающий код сам решит
                pass
            active_dev = _active_device(state)
            device_list = [active_dev] if active_dev else []
            queue = ps.get("player_queue") or {}
            repeat_mode = ((queue.get("options") or {}).get("repeat_mode") or "NONE")
            return {
                "track_object": track_object,
                "paused": paused,
                "playable_id": playable_id,
                "duration_ms": duration_ms,
                "progress_ms": progress_ms,
                "entity_id": queue.get("entity_id") or "",
                "entity_type": queue.get("entity_type") or "VARIOUS",
                "repeat_mode": repeat_mode,
                "device": device_list,
                "track": {
                    "track_id": track_object.track_id,
                    "album_id": track_object.albums[0].id if track_object.albums else 0,
                    "title": track_object.title,
                    "artist": track_object.artists_name(),
                    "duration": track_object.duration_ms // 1000,
                    "minutes": round(track_object.duration_ms / 1000) // 60,
                    "seconds": round(track_object.duration_ms / 1000) % 60,
                },
            }
        except Exception as e:
            logger.error(f"Get Now Playing Error (Ynison): {e}")
            return {}
    async def _now_play_placeholder(self):
        if not self.config["token"]:
            return "No Token"
        try:
            now = await self.__get_now_playing()
            if not now or now.get("paused"):
                return "Not playing"
            return f"{now['track']['title']} — {', '.join(now['track']['artist'])}"
        except Exception:
            return "Error"
    async def _duration_placeholder(self):
        if not self.config["token"]:
            return "No Token"
        try:
            now = await self.__get_now_playing()
            if not now or now.get("paused"):
                return "<code>Not Playing</code>"
            duration = int(now.get("duration_ms", 0) or 0)
            progress = int(now.get("progress_ms", 0) or 0)
            if duration == 0:
                return "0%"
            percent = (progress / duration) * 100
            fill_logic = int(percent // 16.66)
            dp = {
                "start_duration": "<tg-emoji emoji-id=5262663495538742892>☀️</tg-emoji><tg-emoji emoji-id=5260381609479153468>☀️</tg-emoji>",
                "start_full_duration": "<tg-emoji emoji-id=5262663495538742892>☀️</tg-emoji><tg-emoji emoji-id=5260609582048254485>☀️</tg-emoji>",
                "closed_duration": "<tg-emoji emoji-id=5260467667738859177>☀️</tg-emoji>",
                "empty_mid": "<tg-emoji emoji-id=5260415715814448198>☀️</tg-emoji>",
                "end_duration_full": "<tg-emoji emoji-id=5260467667738859177>☀️</tg-emoji>",
                "empty_closed_duration": "<tg-emoji emoji-id=5260239235608255208>☀️</tg-emoji>",
            }
            bar = dp["start_full_duration"] if fill_logic >= 1 else dp["start_duration"]
            for i in range(2, 6):
                bar += dp["closed_duration"] if fill_logic >= i else dp["empty_mid"]
            bar += dp["end_duration_full"] if fill_logic >= 6 else dp["empty_closed_duration"]
            return bar
        except Exception as e:
            return f"Error: {e}"
    async def __download_track(
        self,
        client: yandex_music.ClientAsync,
        track_id: typing.Union[int, str],
        link_only: bool = False,
    ):
        last_exception = None
        for attempt in range(5):
            try:
                info = await client.tracks_download_info(
                    track_id, get_direct_links=True
                )
                if link_only:
                    return info[0].direct_link
                by = io.BytesIO(await info[0].download_bytes_async())
                by.name = "audio.mp3"
                return by
            except Exception as e:
                last_exception = e
                if attempt != 4:
                    await asyncio.sleep(1)
                    continue
                raise e
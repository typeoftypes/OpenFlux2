# Android transport synchronization

Android builds `android/app/libs/openflux.aar` directly from `core/mobile` via `build_android_app.sh`.
The transport implementations are therefore the same implementations as the current OpenFlux core.

Core 0.3.0 transport backends used by Android:

- `yandex` — Yandex Docs WebSocket transport; current core includes browser-like `saveChanges` traffic with randomized 0.5–5s intervals.
- `vyandex` — Yandex Volga HTTP relay + WebSocket transport; Android uses the low-memory configuration exposed by the mobile bridge.
- `boards` — Yandex Board transport; current core uses `modify-objects` / `drop-objects` instead of the removed `notify-position` path.
- `mailru` — Mail.ru Docs WebSocket transport; current core includes randomized editing traffic and ordered cursor payload handling.
- `cupsonline` — Cups.online transport; the mobile bridge explicitly constructs the client-side carrier for Session mode and the stream mode.
- `oneme` — MAX/OneMe WebRTC DataChannel transport; token and UID are passed through Session parameters.
- `direct` — direct TCP transport used as a Session carrier/fallback.

The Android Java layer only serializes these transport definitions and passes them to `mobile.StartSession` / `mobile.StartStreamPacket`. It must not contain a second implementation of the masking protocol.

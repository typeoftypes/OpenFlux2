# OpenFlux Android update

- App version: 1.1.4 (versionCode 15)
- Telegram: https://t.me/typeoftypes
- Embedded core: ba31d0f (OpenFlux 0.3.0 + node-v1.2.0)
- Android transport bridge: `core/mobile`
- Supported transport profiles: direct, yandex, vyandex, boards, mailru, cupsonline, oneme.

The project archive is **source code**, not an APK. Android must not be given this ZIP as an install package.
Build an APK with `./build_android_app.sh`, then verify it with `./verify_android_release.sh`.

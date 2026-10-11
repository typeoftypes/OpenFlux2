# iOS: локальное хранение + сборка через OpenFlux-core

## Профили на устройстве
- Файл: `Application Support/OpenFlux/profiles.vault` (AES-GCM)
- Ключ: Keychain (`ThisDeviceOnly`)
- Миграция со старых UserDefaults — один раз автоматически

## Сборка ядра (без git submodule)

```bash
# из корня monorepo (mobile/ + transport/ уже здесь)
./scripts/sync-openflux-core.sh   # копирует ядро в ./OpenFlux-core
./scripts/build-ios-core.sh       # → ios-app/Lib/liboflux.a

# или одной командой:
./build_ios.sh
```

`OpenFlux-core/` — **локальная папка** (в `.gitignore`), не submodule → CI не падает на recursive.

## CI
```yaml
- uses: actions/checkout@v7   # без submodules
- run: ./build_ios.sh
- run: mkdir -p ios-app/Lib && cp output/... # build-ios-core already fills ios-app/Lib
```

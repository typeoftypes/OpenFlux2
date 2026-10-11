# OpenFlux iOS (submodule core, as Android)

| Path | Role |
|------|------|
| `OpenFlux-core/` | **git submodule** — Go core (`mobile/`, `transport/`, …) |
| `ios-app/` | SwiftUI + Network Extension |
| `scripts/build-ios-core.sh` | как `build-android-core.sh` → `ios-app/Lib/liboflux.a` |

Имя **OpenFlux-core** (не `OpenFlux`), чтобы не путать с приложением.

## Clone

```bash
git clone --recurse-submodules <repo>
# или:
git submodule update --init --recursive OpenFlux-core
```

По умолчанию:

```
OpenFlux-core → https://github.com/p1neappleXpress/OpenFlux.git
```

Свой форк:

```bash
# url в .gitmodules → https://github.com/typeoftypes/OpenFlux2.git
git submodule sync OpenFlux-core
git submodule update --init --remote OpenFlux-core
```

## Build (macOS)

```bash
./scripts/build-ios-core.sh
# или
./build_ios.sh
```

→ `ios-app/Lib/liboflux.a`, `liboflux.h`, `openflux-core.version`

UA/mailru: **`OpenFlux-core/transport/mailru/mailru.go`**, затем снова `./scripts/build-ios-core.sh`.

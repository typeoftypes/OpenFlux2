# OpenFlux2 mailru + iOS build fix

## Files
- `transport/mailru/mailru.go` — Safari iPhone UA, mobileweb API, cookies, WS compression, 409 rotate, Engine.IO wait, log `UA=...`
- `build_ios.sh` — build from local mobile/, verify UA without Xcode strings pipefail
- `ios-app/OpenFluxTunnel/PacketTunnelProvider.swift` — mail.ru hosts bypass VPN (no WS loop)

## Apply
```bash
cd ~/github/OpenFlux2
cp transport/mailru/mailru.go transport/mailru/
cp build_ios.sh ./
# optional VPN loop fix:
cp ios-app/OpenFluxTunnel/PacketTunnelProvider.swift ios-app/OpenFluxTunnel/

# CI macos or Mac:
./build_ios.sh
cp output/ios/liboflux.* ios-app/Lib/
```

## Verify in app log
```
[M-DOCS] start weblink=... UA=Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 ...
[M-DOCS] WebSocket connected
[M-DOCS] Auth OK
```

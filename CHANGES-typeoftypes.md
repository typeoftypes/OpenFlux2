# OpenFlux2 from upstream v0.13.0 (2026-10-16)

Source tag: p1neappleXpress/OpenFlux **v0.13.0** (4f9c786)
Note: `main` branch is still on edcf77a — use the **tag**, not main.

## Upstream highlights (0.7.x → 0.13.0)
- **Mail.ru chat carrier** (not cursor): `OFX1`+base64 on editor chat channel
- No cursor/saveChanges keepalive (R7 4004 access deny)
- ~500 KB preferred batch, message pacing, **make-before-break rotation**
- **bitrix24** transport
- StripedTransport for cups/avito; bonding removed
- node-v1.6.0 pins

## typeoftypes layer
- module path → github.com/typeoftypes/OpenFlux2
- mailru: SetReadLimit 16MB, Sec-Fetch headers, classifyMailruAPIError
- Chrome 141 / Firefox 155 UA bumps where old versions remained

## Build
```bash
go mod tidy
go build -o openflux .
```

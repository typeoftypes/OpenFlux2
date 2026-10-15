# OpenFlux2 transport updates (typeoftypes)

- Module path: `github.com/typeoftypes/OpenFlux2`
- **mailru**: UA pool, Sec-Fetch, cursor `len;base64`, no ---KA---, Auth→connected,
  SetReadLimit 16MB (fixes 1005), MTU 2800 + frags, 6-lane NewMailruGroup, forceWSS,
  dial semaphore, TCP_NODELAY
- **registry**: mailru → NewMailruGroup (stripes param)
- **avito / cupsonline**: Chrome 141, Sec-Fetch, forceWSS
- **yandex docs**: no application KA, SetReadLimit, Firefox 155 UA
- UA bump: Chrome 141 / Firefox 155 across transports

## Build
```bash
go mod tidy
go build -o openflux .
```

## Mail.ru note
Datacenter IPs may get `challenge.html?hash429` from cloud.mail.ru — that is WAF, not a code bug.

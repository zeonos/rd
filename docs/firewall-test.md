# Firewall test (phase 0.5)

Each site needs outbound access to `rd.sfohosting.dk`
(`2.29.149.12`, `2a01:4f9:c01d:1415::1`): **TCP 443, TCP 21115-21117 and
UDP 21116**. Nothing inbound. (443 only becomes possible once our extended
hbbs with WebSocket registration is deployed.)

Run on one machine per site, as any user:

```powershell
powershell -ExecutionPolicy Bypass -File test-connection.ps1
```

| Check | Fails when | Fix |
| --- | --- | --- |
| DNS | name does not resolve | allow DNS, or use the IP in a hosts entry |
| Proxy | a system proxy is set (warning) | set the same proxy in the RD client: Settings → Network → Proxy |
| TCP 443 | firewall blocks the server | add the outbound rule |
| TLS | certificate is not from Let's Encrypt | the firewall inspects TLS: exempt `rd.sfohosting.dk` |
| WebSocket | upgrade refused | allow WebSocket for the host on the proxy/firewall |
| API | not deployed yet (warning) | deploy the API server |

Record the result per site in `docs/phase-0-exit.md`.

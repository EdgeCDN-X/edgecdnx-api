# Utils

`POST /utils/resolve` requires the API's usual authentication. It forwards a DNS
lookup to an HTTP router, not directly to a DNS or gRPC server.

```bash
curl -i http://localhost:5555/utils/resolve \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "ns": "http://dns-router:8080/route",
    "domain": "app.example.com",
    "source_ip": "85.216.128.10"
  }'
```

- `ns`: required full HTTP or HTTPS endpoint, including `/route`. Used exactly as
  supplied; `/route` is not appended. Credentials and URL fragments are rejected.
- `domain`: required name to resolve.
- `record_type`: optional DNS query type, default `A`. Supports `A`, `AAAA`,
  `CNAME`, `TXT`, `MX`, `SRV`, and `NS` (case-insensitive). Unsupported types return
  400 before contacting the router. SRV names retain their `_service._protocol` labels.
- `source_ip`: optional IPv4 or IPv6 address, forwarded as EDNS client subnet input.

The outgoing POST contains only `domain`, `record_type` (when supplied), and `source_ip`; caller authentication
headers are not forwarded. The router's JSON object and HTTP status are returned
without wrapping, including router validation errors and DNS response codes.
Redirects are not followed. Local validation failures return 400; connection
failures or invalid upstream responses return 502; request timeouts return 504.
Requests have a 10-second timeout and upstream responses are limited to 4 MiB.

Authenticated callers can select destinations reachable by the API, including
internal HTTP services. Restrict API access and outbound network access accordingly.

# API

All v2 endpoints live under `/station/api/v2`.
`v2.enabled` defaults to `true`; setting it to `false` disables all these
endpoints (404). `/health` remains available independently.

JSON responses are wrapped in an envelope, and errors repeat the HTTP status:

```jsonc
{ "code": 200, "data": {} }
{ "code": 404, "data": {}, "error": "announcement not found" }
```

Unknown routes and methods get Go's plain 404/405 responses.

## Rooms

A room, as returned by `/recent` and sent in `room` events:

```json
{
  "time": 1777083784,
  "id": "01234",
  "msg": "Room message",
  "name": "Player",
  "source": "x",
  "info": {
    "handle": "@player",
    "url": "https://x.com/player/status/123",
    "avatar": null
  }
}
```

- `info` has the same fields for every platform.
  `avatar` is `null` when missing or when `v2.send_avatar` is off.
- `?extra=1` (or `extra=true`) on `/recent` and `/realtime` adds `info.extra`:
  the stored `XInfo` / `QQInfo` with a `type` field.
- `source` is `""` when the room was submitted without one.

## Endpoints

| Method | Path | Returns |
| --- | --- | --- |
| GET | `/realtime` | SSE stream, see below |
| GET | `/recent` | Rooms from the last 5 minutes, oldest first |
| GET | `/statistic` | `{ online, past15m }` |
| GET | `/status` | `{ pastCount: { past15m, past1h, past24h }, channelHealth }` |
| GET | `/announcement?lang={locale}` | `{ time, msg }` |
| GET | `/ping` | `{ time }`, server time in ms |
| POST | `/submit` | Adds rooms, see below |

- `online` counts open SSE and WebSocket connections combined.
- `past15m` / `past1h` / `past24h` count distinct room IDs.
- `channelHealth` is `[{ "name": "collector", "tick": [...] }]`, 60 per-minute
  counts per channel, oldest first. `/status` returns 503 if the database fails.
- `/announcement`: `lang` defaults to `en` and matches case-insensitively, with
  no fallback to another language. Unknown languages return 404. `time` is the
  announcement file's modification time.

### Realtime stream

`GET /realtime` opens with a `: connected` comment, replays the rooms of the
last 5 minutes, then sends live events. Replayed and live rooms can overlap,
so deduplicate on the client.

| Event | Data |
| --- | --- |
| `room` | A room |
| `roomSkills` | `{ "id": "01234", "time": 1777083784, "data": [123, 134] }` |
| `heartbeat` | `{ "time": 1777083784000 }` |
| `statistic` | `{ "online": 12, "past15m": 31 }` |

`heartbeat` and `statistic` alternate every 15 s.

### Submitting rooms

`POST /submit` is registered only when both `v2.enabled` and `submission.http.enabled` are true.
Otherwise it returns 404. When enabled, send exactly one configured credential:

- Static key: `X-API-Key: <key>`; the key is configured in
  `auth.static.clients[].token` in YAML. No scope setting is needed.
- OAuth: `Authorization: Bearer <access_token>`; requires configured scopes
  (defaults to `rooms:submit`). JWT mode verifies issuer, audience, expiration,
  signature and not-before time with public JWKS. Introspection mode verifies
  `active`, subject, audience, scopes, and expiration/not-before when provided;
  an issuer claim, when provided, must match the configured issuer.

Static keys identify the configured client `name`; OAuth identifies the verified
`sub`. This server-verified identity is the statistics channel. The old client-name
digest headers are not accepted. Read endpoints and UDS require no HTTP credentials.
Both JWT and introspection modes consume access tokens, including tokens obtained
through device login; device codes and refresh tokens are not submission credentials.

```bash
curl -X POST http://127.0.0.1:8888/station/api/v2/submit \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: your-configured-token' \
  --data-binary @rooms.json
```

For OAuth, replace the key header with `Authorization: Bearer <access_token>`.
Use HTTPS for remote submission. Do not put credentials in URLs or frontend code.
Multiple credential headers or both methods together return 400. Missing/invalid
credentials return 401; insufficient OAuth permissions return 403; an unavailable
token verification service returns 503. Authentication failures do not fall back
to another provider. OAuth tokens need a nonempty subject of at most 128 bytes.

The body uses the submission format:

```json
{
  "data": [
    {
      "time": 1777083784,
      "id": "01234",
      "msg": "Room message",
      "name": "Player",
      "source": "x",
      "info": { "tid": "123456", "userName": "@player", "screenName": "Player", "avatar": "" }
      // `or "info": { "qq": 123456, "group": 3333333, "nickname": "Player", "avatar": "" }` for QQ rooms
    }
  ]
}
```

Notice that:

- At most 1000 rooms and 1 MiB per request.
- Rooms whose `id` is not five digits are skipped.
- Any other invalid room will rejects the whole batch.
- Duplicate and filtered rooms are skipped silently, so a 200 does not mean
  every room was added.
- Payload errors: 400 for an invalid body or too many rooms, 413 for a body over
  1 MiB. Authentication errors are described above.

## Collectors (UDS)

Local collectors can write newline-delimited JSON to `uds_path`. This is independent from HTTP API.

```json
{ "type": "room", "channel": "collector", "data": [] }
```

`data` holds rooms in the submission format. `type` defaults to `room`; `channel` names the collector in statistics and is separate from each room's `source`. See `tests/uds_send.py` for an example.

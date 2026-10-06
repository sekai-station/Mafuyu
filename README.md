# Mafuyu

Backend server of Sekai Station.

Current release: **1.0.0**. `VERSION` is the source of truth for the CLI, Docker
image tag and Linux release archives. Run `./mafuyu -version` to check a binary.

## Development

Requires Go 1.26.6 or newer.

```bash
go mod download

# local server
go run ./cmd/server/ -config config.yaml
go run -tags zhcn ./cmd/server/ -config config.yaml

# default build
go build -o server ./cmd/server/

# build with tags
go build -tags zhcn -o server ./cmd/server/

./server -config config.yaml
```

*Only tested on Linux, we cannot guarantee it can work on other platform.*

### Tests

```bash
go test -race ./...
go test -tags zhcn ./...
go vet ./...
go mod verify
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

Run the Python integration tests on Linux with [uv](https://docs.astral.sh/uv/) from the root directory:

```bash
uv sync --locked
uv run --locked pytest -v
```

## Docker

Deploy on Linux. Place the following files under a deployment directory and copy `config.yaml` to `data/config.yaml`:

```text
./
├── docker-compose.yaml
└── data/
    ├── config.yaml
    └── announcements/
```

In `data/config.yaml`, use these settings like these:

```yaml
host: 0.0.0.0
port: 8888
database_path: /data/data.db
uds_path: /data/mafuyu.sock
announcement_dir: /data/announcements
```

Example `docker-compose.yaml`:

```yaml
services:
  mafuyu:
    image: ghcr.io/sekai-station/mafuyu:1.0.0
    command: ["-config", "/data/config.yaml"]
    restart: unless-stopped
    stop_grace_period: 30s
    ports:
      - "127.0.0.1:8888:8888"
    volumes:
      - ./data:/data
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://127.0.0.1:8888/health"]
      interval: 30s
      timeout: 3s
      retries: 3
      start_period: 10s
```

The supplied configuration enables static HTTP authentication. Set each
collector's key directly in `data/config.yaml`:

```yaml
auth:
  static:
    enabled: true
    clients:
      - name: collector-a
        token: "replace-with-your-private-token"
```

Replace the example token with your collector's actual key. The collector sends the same value in the `X-API-Key` request header; its configured `name` becomes the statistics channel.

Ensure the server can write to the directory and the collector user has permission to access the socket.

## Releases

Push a tag matching `VERSION` (for example `v1.0.0`) to run the release workflow.
It validates the version, runs the default/zhcn Go tests and Python integration
tests, publishes Linux amd64/arm64 binaries with SHA-256 checksums, and builds
`ghcr.io/sekai-station/mafuyu:1.0.0` and `:latest` for both architectures.
The archives contain the binary, example configuration, API documentation and
an empty announcements directory. Replace the example authentication token
before enabling public HTTP submissions.

The GHCR package must have public visibility for anonymous Docker pulls. This
is a one-time package setting after its first publication.

To package locally: `bash scripts/package-release.sh amd64` (or `arm64`).
The Dockerfile also accepts `--build-arg GO_BUILD_TAGS=zhcn`; that edition needs
a `filter` configuration to enable keyword or pure-digit rejection.

## Announcements

To make a server-side announcement, add text file with locale name such as `en.txt`, `ja.txt`, `zh-hans.txt` and `zh-hant.txt` under `announcements/`.

## Configuration

See [config.yaml](config.yaml) for all options.

## API

See [API.md](API.md).

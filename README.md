# s3hog

> ⚠️ **Development preview only.** s3hog is a lightweight S3 mock for local development and testing (think Mailpit, but for S3). It is **not** intended for production use: it ships with default credentials, has no TLS, no IAM, no durability guarantees beyond the local filesystem, and has not been security-reviewed. Do not expose it to untrusted networks.

A small Go binary that exposes an S3-compatible API on one port and a **read-only** web UI listing all uploaded objects on another.

## Features

- S3-compatible API (backed by [gofakes3](https://github.com/johannesboyne/gofakes3))
- Filesystem-backed storage (survives restarts, volume-mountable)
- SigV4 authentication (header + presigned URLs)
- Auto-create buckets on first use
- Transparent decoding of `aws-chunked` uploads (AWS CLI v2 default)
- Web UI: list, filter, search, download; live updates via Server-Sent Events
- **Read-only UI** — no uploads, deletes or bucket changes from the browser
- Single static binary, multi-arch Docker image

## Ports

- `9000` — S3-compatible API
- `8025` — Web UI (read-only)

## Quick start (Docker)

```sh
docker compose up -d
```

Open http://localhost:8025 and use S3 credentials `s3hog` / `s3hogsecret`.

If ports `9000` or `8025` are already in use on your host, override the host-side ports:

```sh
S3_HOST_PORT=19000 UI_HOST_PORT=18025 docker compose up -d
```

## Configuration (environment variables)

| Var | Default | Description |
|---|---|---|
| `S3_PORT` | `9000` | S3 API port (inside the container) |
| `UI_PORT` | `8025` | Web UI port (inside the container) |
| `ACCESS_KEY_ID` | `s3hog` | S3 access key (also reads `AWS_ACCESS_KEY_ID`) |
| `SECRET_ACCESS_KEY` | `s3hogsecret` | S3 secret key (also reads `AWS_SECRET_ACCESS_KEY`) |
| `S3_DATA_DIR` | `/data` | Persistence directory |
| `AUTO_BUCKET` | `true` | Auto-create buckets on first use |
| `REGION` | `us-east-1` | Region scope |

## Usage with AWS CLI

```sh
export AWS_ACCESS_KEY_ID=s3hog
export AWS_SECRET_ACCESS_KEY=s3hogsecret
export AWS_DEFAULT_REGION=us-east-1

aws --endpoint-url http://localhost:9000 s3 mb s3://mybucket
aws --endpoint-url http://localhost:9000 s3 cp file.txt s3://mybucket/file.txt
aws --endpoint-url http://localhost:9000 s3 ls s3://mybucket
```

## Run locally

```sh
go run .
```

## Notes

- Data persists in `S3_DATA_DIR` (mount a volume in Docker).
- SigV4 (header + presigned URLs) is verified when credentials are configured.
- Live updates are pushed to the web UI via Server-Sent Events.
- AWS CLI v2 uses `aws-chunked` encoding by default; s3hog decodes it transparently.
- The module/repo name can be changed by editing `go.mod` and the import paths.

## License

MIT — see [LICENSE](LICENSE).

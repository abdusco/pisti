# Pişti

A small, self-hosted two-player Pişti game. Invite a friend to a private table or play against Ada, a computer opponent. Includes English and Turkish, light/dark/system themes, QR invitations, and an installable mobile client.

Play to **101 points**, with 10-point Pişti, 20-point jack Pişti, and 3 points for most cards. Tied matches continue. The final card of a round cannot score Pişti. See the in-game guide for the full rules, adapted from [Pagat’s Pişti rules](https://www.pagat.com/fishing/pishti.html) with a 101-point target.

## Run

Requires Go 1.27 or later. Browser libraries are vendored; no frontend build is needed.

```sh
go run .
# Edit assets without rebuilding:
DEBUG=1 go run .
```

Open http://localhost:8080. Set `PORT` to change the port, or `ADDR` when `PORT` is unset. `go build -o pisti .` produces a binary with all assets embedded.

Tables live in memory and reset on server restart. A signed seat session lasts seven days; abandoned tables are removed after 24 hours. A temporary disconnection preserves the seat. Ada uses her own cards and the public table, without seeing the opponent’s hand.

## Test

```sh
go test -race ./...
go vet ./...
node --test tests/*.test.js
npm ci
npx playwright install chromium
# With the game running:
npm run test:ui
```

UI tests use `PISTI_URL` (default `http://127.0.0.1:8080`). Screenshots are saved to `test-results/`.

## Deploy

```sh
docker compose pull
docker compose up -d --no-build
```

Set `HOST_PORT` for the exposed port. Compose uses `ghcr.io/abdusco/pisti:latest`. For a local build, run `docker compose up --build -d`. Put a TLS reverse proxy in front of the server and enable WebSocket upgrades. HTTPS enables installation and sharing on phones. The offline app shell loads without a network connection; gameplay needs the server.

Stable releases publish `v1.0.0`, `1.0.0`, and `latest` image tags for Linux amd64 and arm64. To pin this release, use `ghcr.io/abdusco/pisti:v1.0.0`. Prereleases do not update `latest`.

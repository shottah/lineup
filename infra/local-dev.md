# Local development stack

Four processes, in start order. All local; no cloud access.

## 1. Postgres 16 (Docker, port 5433 — 5432 is often taken)

    docker run -d --name lineup-pg -p 127.0.0.1:5433:5432 \
      -e POSTGRES_USER=lineup -e POSTGRES_PASSWORD=lineup -e POSTGRES_DB=lineup postgres:16

Already created once? `docker start lineup-pg`.

## 2. Firebase Auth emulator (port 9099; needs Java ≥ 11)

    firebase emulators:start --only auth --project demo-lineup

`demo-` prefixed project IDs are guaranteed offline — the emulator cannot
touch real cloud resources with them. Do NOT use the real project ID here.

## 3. API (port 8080; migrations run at boot)

    cd api && DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable' \
      FIREBASE_PROJECT_ID=demo-lineup FIREBASE_AUTH_EMULATOR_HOST=localhost:9099 \
      PORT=8080 go run ./cmd/api

`FIREBASE_AUTH_EMULATOR_HOST` makes firebase-admin-go verify tokens against
the emulator. Omit it (and use the real project ID) to verify real tokens.

## 4. Web (port 3001 — 3000 is often taken)

    cd web && pnpm run dev --port 3001

## Minting a test ID token without the web app

    curl -s 'http://localhost:9099/identitytoolkit.googleapis.com/v1/accounts:signUp?key=any' \
      -H 'Content-Type: application/json' \
      -d '{"email":"dev@example.com","password":"password123","returnSecureToken":true}' | jq -r .idToken

Any non-empty `key=` works against the emulator. Use the token as
`Authorization: Bearer <idToken>` against `GET http://localhost:8080/v1/me`.

## Store integration tests

    cd api && TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable' go test ./internal/store/

Skipped automatically when `TEST_DATABASE_URL` is unset (CI stays hermetic).

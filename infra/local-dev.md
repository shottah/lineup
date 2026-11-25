# Local development stack

Three processes, in start order. All local; no cloud access.

## 1. Firebase emulators: Auth (9099) and Firestore (8085)

Needs Java 21 (older JDKs are rejected by the Firestore emulator). On macOS
with keg-only Homebrew Java, prefix emulator commands with
`JAVA_HOME=/opt/homebrew/opt/openjdk@21 PATH=/opt/homebrew/opt/openjdk@21/bin:$PATH`.

    firebase emulators:start --only auth,firestore --project demo-lineup

`demo-` prefixed project IDs are guaranteed offline: the emulators cannot
touch real cloud resources with them. Do NOT use the real project ID here.
Emulator data is in memory and gone when the emulators stop.

## 2. API (port 8080)

    cd api && FIRESTORE_EMULATOR_HOST=localhost:8085 \
      FIREBASE_AUTH_EMULATOR_HOST=localhost:9099 FIREBASE_PROJECT_ID=demo-lineup \
      PORT=8080 go run ./cmd/api

`FIRESTORE_EMULATOR_HOST` and `FIREBASE_AUTH_EMULATOR_HOST` make the Firebase
SDKs talk to the emulators. Omit both (and use the real project ID with ADC)
to hit real Firestore and verify real tokens.

Unlike the Next dev server, `go run` does NOT hot-reload: after pulling or
switching branches with `api/` changes, kill and restart this process, or
the web app will talk to a stale API (missing fields, absent routes).

## 3. Web (port 3001 — 3000 is often taken)

    cd web && pnpm run dev --port 3001

## Minting a test ID token without the web app

    curl -s 'http://localhost:9099/identitytoolkit.googleapis.com/v1/accounts:signUp?key=any' \
      -H 'Content-Type: application/json' \
      -d '{"email":"dev@example.com","password":"password123","returnSecureToken":true}' | jq -r .idToken

Any non-empty `key=` works against the emulator. Use the token as
`Authorization: Bearer <idToken>` against `GET http://localhost:8080/v1/me`.

## Store integration tests

    cd api && firebase emulators:exec --project demo-lineup --only firestore \
      --config ../firebase.json "go test ./..."

Requires Java 21 (see above). The store tests skip automatically when
`FIRESTORE_EMULATOR_HOST` is unset (CI stays hermetic); `emulators:exec` sets
it. Each test uses its own `demo-<unique>` project ID, so tests never share
data with each other or with a running dev emulator.

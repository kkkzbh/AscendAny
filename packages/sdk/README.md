# `@ascendany/sdk`

This package is the sole first-party TypeScript HTTP contract for AscendAny v2.
Operation paths, request shapes, response DTOs, error DTOs, authentication, and
media types are generated from
`../../contracts/openapi/ascendany-v2.yaml`. Do not edit `src/generated/` by
hand or define parallel endpoint/DTO copies in applications.

## Generate and verify

The generator is pinned to `@hey-api/openapi-ts@0.99.0` and requires Node
22.18 or newer.

```sh
pnpm --filter @ascendany/sdk generate
pnpm --filter @ascendany/sdk check
pnpm --filter @ascendany/sdk test
```

`check` performs all contract gates:

1. Redocly `recommended-strict` parsing/linting, including unresolved refs,
   path parameters, required operation IDs, and operation-ID uniqueness.
2. Fresh generation into an OS temporary directory followed by a byte-for-byte
   comparison with committed `src/generated/`. The check never reads Git state,
   so unrelated dirty files cannot mask drift.
3. An esbuild `platform: browser` bundle of the public SDK entry point. Node
   builtins in generated runtime code fail this gate.
4. Strict TypeScript checking of generated and public source.
5. Local Fetch smoke tests for base URL, bearer auth, Pintia vendor media type,
   UUID path serialization, enrollment issue/consume/revoke security behavior,
   self-analytics query serialization, returned API errors, and `throwOnError`
   behavior.

The generated runtime uses standard Fetch APIs and can run in browsers and
Electron renderers. Node-only packages remain generation/test dependencies.

## Client setup

Applications create a client and pass it to generated operations:

```ts
import { createClient, getImportJob } from "@ascendany/sdk";

const client = createClient({
  baseUrl: "https://ascendany.example",
  credentials: "include",
  auth: (security) => security.in === "cookie" ? undefined : session.accessToken,
});

const result = await getImportJob({
  client,
  path: { jobId },
});
```

The client returns `{ data, error, response }` by default. Pass
`throwOnError: true` to an operation when exception flow is explicitly desired.

The refresh credential is an `HttpOnly` browser cookie. Keep
`credentials: "include"` for cross-origin deployments and return `undefined`
from the auth callback for cookie schemes. This lets the browser attach the
cookie without exposing it to JavaScript or synthesizing a `Cookie` header.
The enrollment consume operation is public and its generated request never
attaches a configured bearer token; the server still requires the exact web
Origin because success creates a refresh cookie.

## Browser LSP sessions

Use `BrowserSession.openLspSession()` for browser and renderer clients:

```ts
const connection = await browserSession.openLspSession();
const { socket, session } = connection;
socket.send(JSON.stringify(initializeMessage));
await connection.close();
```

The method creates the bounded server session with the in-memory access token,
immediately consumes the returned one-time attach ticket as the second
WebSocket subprotocol, and returns metadata with the ticket removed. It never
writes the ticket to `BrowserSessionStorage`, a URL, or the returned session
object. A failed WebSocket construction, upgrade, or protocol negotiation
closes the server session through the authenticated DELETE endpoint.
`connection.close()` is idempotent, deletes the authenticated server session,
and closes the WebSocket even when server cleanup fails.

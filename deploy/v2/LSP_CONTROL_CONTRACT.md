# LSP v1 control contract

The LSP session manager belongs to the server write capability. With
`ASCENDANY_WRITE_MODE=disabled`, `ascendanyd` does not resolve the LSP worker
identity, construct the manager, create the control socket, or start worker
units. All three public LSP session routes return `503 writes_disabled` before
request validation. Enabling writes creates and serves the manager before the
HTTP listener accepts traffic.

This document is the closed production contract between `ascendanyd`, one
`ascendany-lsp@<session-id>.service` instance, and its single `clangd` child.

## Ownership and lifecycle

1. `ascendanyd` creates a random canonical lowercase UUIDv4, records the
   authenticated account as its owner, and reserves one bounded in-memory
   session slot.
2. `SystemdLauncher` starts exactly
   `ascendany-lsp@<canonical-uuid>.service`. The root-owned polkit rule permits
   the `ascendany` OS user to start or stop only that UUID-shaped template.
3. The worker accepts the session id only when it matches its workspace
   basename. It connects to `/run/ascendany-lsp-control/control.sock`; it never listens
   on TCP or Unix sockets.
4. Both peers verify the other process UID with `SO_PEERCRED` before accepting
   protocol data. The manager expects the dedicated `ascendany-lsp` UID. The
   worker requires the read-only bound control socket to remain the same
   mode-`0660` inode across `connect`, derives its non-root owner UID, and
   requires the connected server peer to have that exact UID.
5. The worker sends exactly one versioned hello frame before starting the LSP
   relay:

   ```json
   {"schema":"ascendany.lsp.control.v1","sessionId":"11111111-1111-4111-8111-111111111111"}
   ```

   Unknown fields, duplicate fields, noncanonical UUIDs, unmatched sessions,
   duplicate worker connections, late workers, and wrong peer UIDs are
   rejected by closing the connection.
6. Session creation generates a separate 32-byte CSPRNG attach ticket. The
   response returns its canonical unpadded base64url text once; the manager
   retains only SHA-256. The capability is bound to the authenticated owner,
   session id, and exact creation Origin.
7. The manager validates the capability in constant time and atomically
   consumes it before one WebSocket upgrade. Cross-session tickets, a changed
   Origin, malformed tickets, and repeated use resolve as an absent session.
   Upgrade failure destroys the claimed session.
8. Disconnect, explicit DELETE, request cancellation, manager shutdown,
   protocol failure, workspace-limit failure, or the session deadline closes
   the control socket and stops the unit.
9. The worker sends `SIGKILL` to the complete `clangd` process group, waits for
   it, removes its per-session workspace, and exits. systemd supplies a second
   process and mount namespace teardown boundary.

## Framing and message policy

The control socket uses standard LSP framing. `Content-Length` is one canonical
positive decimal followed by CRLF; an optional second header is accepted only
as the exact value
`Content-Type: application/vscode-jsonrpc; charset=utf-8`. LF-only lines,
unknown or duplicate headers, zero-length frames, partial bodies, and trailing
JSON are rejected.

The production policy is shared by the manager, worker, and HTTP handler:

| Boundary | Limit |
| --- | ---: |
| Header bytes | 256 |
| Header fields | 2 |
| JSON body | 1 MiB |
| Framed messages per reader or writer | 4096 |
| JSON nesting | 32 levels |
| Session duration | 30 minutes |
| Workspace regular-file bytes | 32 MiB |
| Workspace entries | 512 |
| Workspace inspection interval | 250 ms |

Every body is one UTF-8 JSON object with valid Unicode scalar values and no
duplicate object keys. Client messages require JSON-RPC 2.0 and a closed LSP
method allowlist. `workspace/executeCommand`,
`workspace/didChangeConfiguration`, initialization options, and non-null
client process ids are rejected. URI-bearing fields accept only canonical safe
ASCII paths beneath `file:///workspace`; percent escapes, traversal, queries,
fragments, authorities, symbolic links, and special workspace files are
rejected.

The public URI is always `file:///workspace`. The worker starts the fixed,
root-owned `/usr/bin/clangd` executable directly, with no shell, and maps that
public URI to its private session directory. clangd receives a fixed argument
list, fixed environment, bounded result settings, no background index, no
configuration files, and no network namespace.

## HTTP boundary

The browser-facing contract is:

- `POST /api/v2/lsp/sessions` creates one owned session and returns one
  in-memory-only attach ticket.
- `GET /api/v2/lsp/sessions/{sessionId}/websocket` attaches the only client.
- `DELETE /api/v2/lsp/sessions/{sessionId}` cancels the session and destroys
  its workspace.

POST and DELETE require one exact allowed `Origin` and one `Authorization:
Bearer ...` header. The WebSocket requires the exact Origin used for creation,
forbids Authorization, and offers exactly `ascendany.lsp.v1` plus
`ascendany.lsp.ticket.<ticket>` in that order. The server selects only the
version protocol, so the ticket is never echoed. Query strings, including query
tokens, are rejected. The one-time ticket is never persisted or logged.
Compression is disabled, only text messages are accepted, and the shared
one-MiB message limit applies before JSON and LSP validation.

## Isolation boundary

The service enters the root-owned `/var/lib/ascendany-lsp-root` skeleton with
`RootDirectory=`. Its only host-backed views are a read-only `/usr`, the exact
release `ascendany-lsp` executable, and the exact dedicated control socket.
The socket bind is read-only beneath a root-owned directory; its non-root inode
owner is the worker's explicit server capability identity.
`/bin`, `/lib`, and `/lib64` are reviewed relative links into `/usr`; `/etc`,
`/home`, and `/var` remain empty. `MountAPIVFS=yes`, `PrivateDevices=yes`, a
disconnected private `/tmp`, and a private PID namespace provide the minimal
runtime view needed by the static Go worker and clangd. The dedicated
`ascendany-lsp-control` group owns only the control directory; the LSP identity
is not a member of `ascendany-runtime`.

The service also has `PrivateNetwork=yes`, `RestrictAddressFamilies=AF_UNIX`,
no capabilities, no credentials, no database socket, no shared writable LSP
state, and no artifact or old-release visibility. `MemoryMax`, `TasksMax`,
`CPUQuota`, and `LimitFSIZE` provide outer resource ceilings.

The worker binary dependency graph contains no database, credential, auth,
artifact, or OJ package. There is no network, shell, credential, database, or
alternate-executable fallback.

## API-side construction

Production wiring resolves the dedicated worker UID once and passes one shared
policy instance to the manager and HTTP handler:

```go
launcher, err := lspexecutor.NewSystemdLauncher("/usr/bin/systemctl")
manager, err := lspexecutor.NewManager(launcher, lspexecutor.Config{
    SocketPath:               "/run/ascendany-lsp-control/control.sock",
    ExpectedWorkerUID:        resolvedLSPWorkerUID,
    MaximumSessions:          configuredMaximumSessions,
    MaximumPendingHandshakes: configuredMaximumPendingHandshakes,
    HandshakeTimeout:         configuredHandshakeTimeout,
    StartupTimeout:           configuredStartupTimeout,
    StopTimeout:              configuredStopTimeout,
    Random:                   cryptorand.Reader,
    Policy:                   lspPolicy,
})
```

`manager.Serve` owns the Unix listener for the application lifetime. The same
manager is supplied as `httpapi.Options.LSP`; `httpapi.Options.LSPPolicy` is
the exact `lspPolicy` value. Startup fails if the socket parent is absent or
traverses a symbolic link, or if the socket path already exists.

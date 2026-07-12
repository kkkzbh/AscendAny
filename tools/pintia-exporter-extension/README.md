# AscendAny Pintia snapshot v2 exporter

This Manifest V3 extension exports one complete Pintia problem set as the
strict `ascendany.pintia.snapshot.v2` contract. It has no v1 writer, v1 reader,
or conversion path. Pintia authentication remains in the browser; AscendAny
receives only the downloaded JSON file.

The authoritative schema and semantic rules live in
`../../contracts/pintia/ascendany.pintia.snapshot.v2.schema.json` and
`../../contracts/pintia/README.md`. The build fails when that schema's exact
SHA-256 differs from the digest compiled into the exporter. Ajv compiles that
exact schema into a CSP-safe standalone validator before typechecking, tests,
and production bundling; Chrome never compiles the schema at runtime.

## Build and load

From the repository root:

```sh
pnpm install --filter @ascendany/pintia-exporter...
pnpm --filter @ascendany/pintia-exporter typecheck
pnpm --filter @ascendany/pintia-exporter test
pnpm --filter @ascendany/pintia-exporter build
```

Then open `chrome://extensions`, enable Developer mode, choose **Load
unpacked**, and select:

```text
tools/pintia-exporter-extension/dist
```

`dist/manifest.json` references only deterministic bundled output. Files under
`src/` are never loaded directly by Chrome. Re-run `build` after every source
change and reload the extension.

## Export flow

1. Open `https://pintia.cn/problem-sets/<problemSetId>/...` while signed in.
2. Open the extension and start the export.
3. The progress tab stays attached to the durable task. The original Pintia
   tab is temporarily navigated through the problems, rankings, and submissions
   routes, then restored.
4. After all validation succeeds, the extension writes
   `AscendAny-Pintia-<title>-<problemSetId>-<timestamp>-<downloadIdentity>.json`
   directly to the browser's configured download directory. The UUIDv4
   download identity makes exact recovery and cleanup independent of other
   files with the same human-readable prefix. No save dialog interrupts a
   long-running export.

The extension owns one global export lease from capture start through download
completion. A second problem set is rejected with an explicit wait-for-download
message, which also prevents snapshot delivery cleanup from touching another
export's live Blob or OPFS file.

Problems and rankings use exhaustive numbered pagination. Submissions use the
Pintia `before` cursor until `hasBefore` is explicitly false. Repeated IDs,
non-advancing cursors, missing exhaustion signals, and pagination limits stop
the export. `ListSubmissions` has no stable collection-count contract, so
submission `sourceReportedCount` is always `null`; `observedCount` is derived
from the fully exhausted cursor chain. Download remains disabled until all
three pagination chains report `paginationExhausted: true`.

Exam identity comes from Pintia's named `GetProblemSet` query
(`/api/problem-sets/{problem_set_id}`), whose `problemSet.id`, `name`,
`startAt`, and `endAt` fields are validated and copied into a closed metadata
record before pagination starts. Browser tab titles and route labels never
enter the snapshot domain hash. `exporter.exportedAt` is generated only after
all collection work has completed, so checkpoint age does not become export
provenance.

Rankings come from the named `GetCommonRankings` query. Group display names are
collected independently through `ListUserGroupsForProblemSet` and joined by the
typed `userGroupId`. Pintia's literal exam-member sentinel `userGroupId = "0"`
means that the member is ungrouped and maps to a null `groupName`; every other
dangling group reference fails validation. Explicitly null `startAt` and
`endAt` values remain null in the snapshot.

Only submissions whose source `problemType` is `PROGRAMMING` are exported.
`completeness.submissions.observedCount` records the complete unfiltered list;
`exportedCount` records the programming subset. A programming submission that
references a missing problem is treated as incomplete source collection and
fails the task.

## Long-running code collection

Submission detail collection preserves the long-task behavior of the original
prototype:

- the service worker sends ordered production batches of at most 8 IDs; the
  MAIN-world collector accepts at most 32 only as a hard contract cap;
- one MAIN-world injection starts at most 8 requests and contains no pacing
  timer. The service worker waits `batch size * 90 ms` between injections, so a
  hidden Pintia tab cannot stretch pacing through Chrome's background-timer
  throttling. A batch commits only after every requested ID binds exactly to
  its response;
- at the 20,000-submission limit, two complete 90 ms request-start sequences
  reserve a conservative 1-hour pacing budget inside the 2-hour whole-export
  deadline;
- a rate-limit response stops the current pass immediately and causes one
  120-second service-worker-owned backoff before the next of five bounded
  recovery rounds;
- completed code entries are checkpointed every 40 entries or five seconds;
- checkpoints live in the extension's v2 IndexedDB database and keep task
  state visible when a progress tab disconnects;
- a keepalive tick accompanies long waits and collector requests;
- after a service-worker restart, the progress page distinguishes a live owner
  from an interrupted persistent journal. It waits the journal's exact safety
  window, refreshes ownership, and starts one recovery only when no live owner
  exists; a live owner cancels the pending recovery timer;
- the progress page exposes phase, completed/pending counts, retry, and an
  explicit checkpoint reset.

A newly numbered capture attempt starts from empty parts. If the service worker
restarts during that same attempt, it recomputes the cumulative budget from the
closed typed checkpoint parts and fetches only missing collections or
submission details. Once the pass is complete, the exporter re-fetches all
three collections and every submission detail as canonical digests. Collection
drift, edited code, or a rejudge result discards every persisted part and starts
the next empty attempt. At most three attempts run. Generation-bound checkpoint
writes and the final full re-scan prove that resumed parts belong to one stable
source state before delivery.

The checkpoint contract is tagged `ascendany.pintia.snapshot.v2` and carries
an exact internal task-format version. A task from another format is rejected
instead of being interpreted. An extension upgrade that changes that exact
format recreates the checkpoint store, so an interrupted task starts cleanly
under the new collector contract.

## Normalization and security

The downloaded object contains exactly:

```text
schema, schemaSha256, exporter, exam, problems, participants, submissions, completeness
```

Participant identity is `userId`; participants are the exact union of ranking
users and exported-submission users. The exporter creates typed case-result
arrays, converts time to milliseconds and memory to bytes, hashes the exact
UTF-8 code string, rejects duplicate IDs and dangling references, verifies all
counts, and emits no `raw`, `rawIndexes`, or unknown source object.

The service worker injects one self-contained function into the exact main
frame with `chrome.scripting.executeScript({ world: "MAIN" })`. It resolves the
six named Pintia APIs from the current Webpack factory exports and receives the
structured result through Chrome's execution-result channel. There is no
content script, web-accessible bridge, `window.postMessage`, fixed module id,
or page-global request channel. The manifest permissions are exactly
`downloads`, `offscreen`, `scripting`, and `unlimitedStorage`, plus the Pintia
host grant.

Each of the six Pintia API calls has a 20-second terminal deadline. A static
collection is bounded to 10 minutes, a detail batch to 2 minutes, and the whole
export to 2 hours. Whole-export cancellation keeps the global lease while the
longest MAIN-world collector reaches its own deadline. Download start and
terminal state share a 5-minute deadline. Every failed, interrupted, recovered,
or superseded download is identified by both Chrome ID and its UUID-bearing
exact filename, then cancelled if necessary, removed from disk, and erased from
Chrome history through a separately bounded cleanup operation. Recovery freezes
old producer transitions before it inspects resources, so a late Chrome ID
cannot re-enter the recovered generation.

Source collection and final delivery share the production importer limits:
128 MiB serialized snapshot bytes, 2,000,000 decoded JSON nodes, 32 MiB total
decoded string bytes, 8 MiB per string, depth 32, 1,000 problems, 20,000
participants, 20,000 submissions, 1,000 ranking results, 1,000 case results,
and 1 MiB of code per submission. Programs are limited by exact UTF-8 bytes.
Every nullable score is exactly null, zero, or within `[1e-100, 1e100]`; every
integer and unit conversion must remain a safe non-negative integer. Delivery
writes 1 MiB UTF-8-safe
chunks to a dedicated OPFS directory; only the canonical UUIDv4 filename and
byte count cross the runtime-message boundary. An offscreen document owns the
Blob URL until Chrome confirms download completion. Extension startup and each
new globally leased export revoke unrecoverable URLs and remove audited orphan
files. A failed revoke removes its live ownership before reporting cleanup
failure, which lets the next exclusive reconciliation repair the remaining
offscreen URL or OPFS file. Any unexpected directory name or entry kind stops
reconciliation rather than being ignored.

The source-shape golden fixture is fully synthetic. Its derivation and
sanitization rules are recorded in
`tests/fixtures/SANITIZATION.md`; the original local export is never copied,
loaded by tests, or committed.

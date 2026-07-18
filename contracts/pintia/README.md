# Pintia snapshot v2 contract

`ascendany.pintia.snapshot.v2.schema.json` is the authoritative structural
contract for browser-exported Pintia snapshots. A bundle has exactly these
top-level fields:

```text
schema, schemaSha256, exporter, exam, problems, participants, submissions, completeness
```

Every object is closed with `additionalProperties: false`. `raw`,
`rawIndexes`, source response objects, and untyped case-result maps are outside
the contract. A source value which may be unavailable is still a required field
whose schema explicitly admits `null`; exporters must not omit it.

## Identity and normalization

- `problemSetId`, `problemSetProblemId`, `problemId`, `userId`,
  `studentUserId`, and `submissionId` are stable Pintia IDs copied from Pintia.
  Display labels, student numbers, names, array indexes, and locally generated
  values must not be substituted for them.
- `problemSetProblemId` is the problem reference inside this problem set.
  `problemId` identifies the underlying Pintia problem.
- `userId` is the sole participant key and the actor reference used by every
  submission. `studentUserId` and `studentNumber` are nullable attributes.
- Official exporter provenance accepted by the registration nickname capability
  (strict SemVer `>=2.2.3` within major `2`) guarantees that
  `participants[].displayName` is the exact nullable PTA `user.nickname`.
  Earlier snapshot-v2 exporter releases remain valid import and analytics input,
  but their `displayName` cannot prove a registration nickname.
- Ranking rows are normalized into `participants[].ranking`. There is no
  top-level `rankings` array.
- `acceptTimeSeconds` is Pintia's non-negative elapsed ranking time in seconds.
  It comes directly from PTA `GetCommonRankings`, is required for every ranking
  problem result, and must never be interpreted or stored as a wall-clock
  timestamp.
- `passed` is `score >= problem.maxScore` when both values exist. It is `null`
  when either value is unavailable. A positive partial score does not mean the
  problem passed.
- `caseResults` is an array of typed objects. Its `caseId` is the Pintia map key
  or ID, copied without inventing an ordinal identity.
- `memoryBytes` is measured in bytes; `timeMs` is measured in milliseconds.
- All timestamps are RFC 3339 UTC instants. SHA-256 strings are lowercase hex.

## Decimal score domain

Every non-null score-shaped field uses one numeric contract: zero or an exact
JSON number in the closed interval `[1e-100, 1e100]`. This covers
`exam.totalScore`, `problems[].maxScore`, ranking totals and problem scores,
submission scores, and case-result scores. The schema expresses the interval as
`null | const 0 | [1e-100, 1e100]`; the Go importer repeats the comparison on
the canonical base-10 value and requires conversion to a finite `float64`.
Non-zero values which convert to zero are rejected.

The bound is an arithmetic contract for the analytics engine. Division of any
two accepted non-zero values is at most `1e200`; one square is at most `1e200`.
Even a conservative sum of squared values across the largest possible signed
64-bit node count remains below `9.23e218`, well inside finite `float64` range.
The current analytics paths are narrower: ranking total divided by exam total,
ranking total divided by at least one second expressed in minutes, and a
two-term quantile interpolation. The reviewed production collection caps admit
at most 220,221,001 Decimal slots in one snapshot, so their corresponding sum
and sum-of-squares envelopes remain below `2.21e108` and `2.21e208`.

The exporter normalizes each entry of Pintia's
`testcaseJudgeResults` object with this exact mapping:

- `caseId` is the object key and `verdict` is `result`.
- `score` is `testcaseScore`.
- `timeMs` is `Math.round(time * 1000)` because Pintia reports `time` in
  seconds; `memoryBytes` is `Math.round(memory)` because Pintia reports
  `memory` in bytes. Missing values become `null`; negative or non-finite
  numeric values reject the export.
- `message` is the non-empty string `error` when present, otherwise the
  non-empty string `checkerOutput`, otherwise `null`. The exporter does not
  stringify other objects or infer a message from unrelated fields.

## Required semantic validation

JSON Schema cannot express the following cross-record rules. The exporter must
check them before download, and the importer must check them again before any
business write:

1. `schemaSha256` equals the digest defined below.
2. `problemSetProblemId` and `problemId` are unique in `problems`; `userId` is
   unique in `participants`; `submissionId` is unique in `submissions`; case IDs
   are unique within one submission; ranking problem references are unique
   within one participant.
3. Every submission and ranking problem result references an exported
   `problemSetProblemId`; every submission references an exported participant
   `userId`.
4. Every ranking problem result's `passed` value follows the exact rule above
   using the referenced problem's `maxScore`; importers recompute and reject a
   mismatch.
5. Participant IDs are exactly the union of ranking user IDs (participants with
   non-null `ranking`) and submission user IDs. There are no missing or extra
   participants.
6. `SHA-256(UTF-8(submission.code))` equals `submission.codeSha256`, using the
   code string exactly as represented after JSON decoding and without newline
   normalization. `submission.code` is non-empty; an empty programming detail
   is an incomplete export.
7. `completeness.*.exportedCount` equals its exported array length. Ranking
   count equals the number of participants with non-null `ranking`; participant
   count equals `participants.length`.
8. `observedCount >= exportedCount`. When `sourceReportedCount` is non-null and
   pagination is exhausted, it equals `observedCount`. Submission observation
   covers the unfiltered source list; export contains only submissions whose
   problem is an exported `PROGRAMMING` problem.
9. Parsed timestamps are valid and `exam.startsAt <= exam.endsAt` when both
   exist. `exam.sourceUrl` is an absolute `https://pintia.cn` URL whose first
   two path segments are exactly `problem-sets/<exam.problemSetId>`; prefix
   collisions and nested lookalike routes are rejected. Each submission belongs
   to the declared problem set snapshot.
10. Every Decimal field satisfies the exact analytics interval above before
    any snapshot row, exam head, or analytics generation is written.

Failure of any rule rejects the whole snapshot. The importer uses the exam as a
single transaction boundary.

## Schema digest rule

`schemaSha256` does not hash a bundle and is not a digest literal embedded in
the schema. It is SHA-256 of the exact bytes of
`ascendany.pintia.snapshot.v2.schema.json` as stored: UTF-8, LF line endings,
and its final newline included. This avoids self-reference because the schema
only constrains the digest shape; it contains no copy of its own digest.

Compute it from the repository root:

```sh
sha256sum contracts/pintia/ascendany.pintia.snapshot.v2.schema.json
```

During fixture or exporter generation, an uncommitted template may contain
`__SCHEMA_SHA256__`. Finalization computes the command above and replaces every
placeholder with the lowercase 64-character digest. Committed bundles and
fixtures must contain the actual digest, and CI/import validation must reject
both placeholders and stale digests. Any byte change to the schema requires
updating every fixture and exporter constant in the same change.

## Fixtures and local checks

`fixtures/valid` contains accepted bundles. `fixtures/invalid-structural`
contains JSON Schema failures. `fixtures/invalid-semantic` contains bundles
which deliberately pass the structural shape and fail one named semantic rule.

| Fixture | Expected result |
| --- | --- |
| `valid/complete.json` | accepted |
| `invalid-structural/unknown-raw-field.json` | JSON Schema rejection for closed `problem` object |
| `invalid-semantic/duplicate-problem-set-problem-id.json` | duplicate stable problem key |
| `invalid-semantic/dangling-problem-reference.json` | unknown submission problem reference |
| `invalid-semantic/dangling-user-reference.json` | unknown submission actor and participant-union mismatch |
| `invalid-semantic/bad-code-hash.json` | decoded code SHA-256 mismatch |
| `invalid-semantic/count-mismatch.json` | problem exported count differs from array length |

The Go importer compiles this Draft 2020-12 contract with
`github.com/santhosh-tekuri/jsonschema/v6`, enables asserted format checks, and
then applies the semantic rules above. Its fixture suite exercises every valid,
structurally invalid, and semantically invalid bundle. JSON syntax can also be
checked independently with existing tools:

```sh
find contracts/pintia -name '*.json' -print0 | xargs -0 -n1 jq empty
```

The fixture digest fields can be compared with the authoritative bytes using:

```sh
schema=contracts/pintia/ascendany.pintia.snapshot.v2.schema.json
digest=$(sha256sum "$schema" | awk '{print $1}')
find contracts/pintia/fixtures -name '*.json' -print0 | \
  xargs -0 -n1 jq -e --arg digest "$digest" '.schemaSha256 == $digest'
```

Run the importer fixture and preflight coverage tests from `backend/`:

```sh
go test ./internal/pintia
```

# Go Pintia snapshot v2 validation

This package accepts exactly `ascendany.pintia.snapshot.v2`. Online workers use
the byte-for-byte contract copy embedded in the binary and construct one
long-lived `Validator`:

```go
validator, err := pintia.NewEmbeddedValidator(pintia.DefaultLimits())
if err != nil {
    return err
}
snapshot, err := validator.ValidateReader(ctx, upload)
if err != nil {
    return err
}
domainHash, err := pintia.DomainHash(ctx, snapshot)
```

Callers pass the non-nil lifecycle `context.Context` through both validation
and `DomainHash`.
Cancellation is observed while reading, streaming tokens, materializing JSON,
decoding typed values, checking semantics, and producing canonical domain
bytes.

`NewValidator` computes SHA-256 over those exact schema bytes. Every snapshot's
`schemaSha256` must match it. The binary also pins `ExpectedSchemaSHA256`, so a
changed contract file fails validator construction before compilation. There
is no v1 parser, compatibility conversion, unknown-field tolerance, or digest
fallback.

## Validation order

1. Enforce total input bytes and valid UTF-8.
2. Run a schema-derived token-stream preflight before constructing a generic
   JSON tree. It rejects unknown and duplicate keys immediately, enforces JSON
   depth, total node count, decoded string bytes, every individual string and
   code body, and every known array including ranking problem results and
   submission case results. Context cancellation is checked throughout token
   scanning.
3. Materialize the bounded generic JSON value.
4. Validate Draft 2020-12 structure, closed objects, constants, patterns, and
   asserted formats with `github.com/santhosh-tekuri/jsonschema/v6` and the
   authoritative schema. The compiler receives the already strictly decoded
   schema document and enables `AssertFormat` before compilation.
5. Decode typed values without unknown fields and compare the schema digest.
6. Enforce every cross-record rule from the contract README: unique IDs,
   references, exact participant union, code SHA-256, completeness counts,
   exhausted pagination counts, exam time ordering, PostgreSQL
   representability, and the exact analytics Decimal interval.

Default limits are 64 MiB total input, 2,000,000 JSON nodes, 32 MiB of total
decoded string data, depth 32, 8 MiB per string, 1,000 problems, 20,000
participants, 1,000 problem results per ranking, 20,000 submissions, 1,000
case results per submission, and 1 MiB of UTF-8 code per submission. Production
passes every value explicitly from required configuration. Limit relationships
are validated at startup, and schema compilation fails if a newly introduced
array lacks an explicit cap. Exact decimal normalization remains capped at
1 MiB to reject compact exponent expansion attacks.

All non-null Pintia Decimal fields are zero or lie in `[1e-100, 1e100]`.
Canonical decimal comparison happens before `float64` conversion; non-zero
underflow and overflow are permanent snapshot validation failures.

Failures use `ValidationError` and stable `ErrorCode` values so the HTTP/job
boundary can classify permanent input failures without parsing error text.

## `domain_hash_proto_v1`

`CanonicalDomainV1` creates the language-neutral bytes hashed by `DomainHash`:

- a closed canonical JSON record whose first field is
  `"protocol":"domain_hash_proto_v1"`;
- fixed struct field order and no maps;
- UTF-8 without whitespace or a trailing newline; JSON strings use RFC 8259
  escaping, do not HTML-escape `<`, `>` or `&`, and escape U+2028/U+2029 as
  lowercase `\u2028`/`\u2029`;
- problems sorted by `problemSetProblemId`, participants by `userId`,
  submissions by `submissionId`, ranking results by
  `problemSetProblemId`, and case results by `caseId`;
- instants represented as signed UTC epoch milliseconds;
- decimals represented as exact canonical base-10 strings;
- nullable values always emitted, with JSON `null` preserving presence;
- all exam, problem, participant, ranking, submission, and case-result domain
  fields included, including both `code` and the already verified
  `codeSha256`.

Canonical object field order is fixed as follows:

| Record | Field order |
| --- | --- |
| envelope | `protocol`, `exam`, `problems`, `participants`, `submissions` |
| exam | `platform`, `problemSetId`, `title`, `startsAtEpochMs`, `endsAtEpochMs`, `totalScoreDecimal` |
| problem | `problemSetProblemId`, `problemId`, `label`, `title`, `type`, `maxScoreDecimal`, `contentHtml`, `timeLimitMs`, `memoryLimitBytes` |
| participant | `userId`, `studentUserId`, `studentNumber`, `displayName`, `groupName`, `ranking` |
| ranking | `rank`, `totalScoreDecimal`, `timeUsedSeconds`, `problemResults` |
| ranking result | `problemSetProblemId`, `scoreDecimal`, `passed`, `validSubmissionCount`, `acceptTimeSeconds` |
| submission | `submissionId`, `problemSetProblemId`, `userId`, `submittedAtEpochMs`, `language`, `compiler`, `verdict`, `scoreDecimal`, `timeMs`, `memoryBytes`, `code`, `codeSha256`, `compileLog`, `caseResults` |
| case result | `caseId`, `verdict`, `scoreDecimal`, `timeMs`, `memoryBytes`, `message` |

Exporter name/version/time, schema metadata, `exam.sourceUrl`, upload filename,
and completeness observations are transport/provenance metadata and are
excluded. The artifact and snapshot records retain them separately. Any change
to included fields or encoding requires a new protocol identifier.

Official exporter provenance accepted by the registration nickname capability
(strict SemVer `>=2.2.3` within major `2`) guarantees that
`participant.displayName` is the exact nullable PTA `user.nickname`. Earlier
snapshot-v2 exporter releases remain importable analytics input, while their
`displayName` cannot authorize registration.

The complete fixture currently has the following protocol golden hash:

```text
6582875727744c1d33f8559fc35b3ff0b5b8c0009b18ea374f7b5f41b51ff48a
```

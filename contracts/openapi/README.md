# AscendAny v2 OpenAPI contract

`ascendany-v2.yaml` is the authoritative public HTTP contract. Go handlers and generated TypeScript clients must match the same operation ids and schemas. First-party applications do not define duplicate endpoint strings or response DTOs.

The contract covers runtime health, authentication and enrollment, account and
administration workflows, configuration, authenticated feedback, student-owned
Agent Notes, self-service analytics, exams, and the Pintia v2 import vertical.
New product domains extend this document in the same change that adds their Go
handlers and generated SDK surface.

Every HTTP operation has one globally unique `operationId`. The SDK exports
operations using those identifiers. Contract changes must regenerate and commit
`packages/sdk/src/generated/`; first-party applications consume
`@ascendany/sdk` and do not maintain endpoint or DTO copies.

Pintia upload uses a streaming request body with media type:

```text
application/vnd.ascendany.pintia.snapshot.v2+json
```

The backend durably publishes the content-addressed artifact before returning `202`. Repeated identical bytes return the same idempotent job. Import event ids are durable per-job sequences and clients resume with `Last-Event-ID`.

Validate the OpenAPI 3.1 document, generated output, browser runtime, types, and
smoke behavior with:

```sh
pnpm --filter @ascendany/sdk check
```

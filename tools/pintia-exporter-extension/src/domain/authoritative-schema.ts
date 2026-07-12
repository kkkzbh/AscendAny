import validateAuthoritativeSchema from "../../.generated/authoritative-schema-validator.mjs";

export function validateAuthoritativeSnapshotSchema(value: unknown): void {
  if (validateAuthoritativeSchema(value)) {
    return;
  }
  const detail = (validateAuthoritativeSchema.errors ?? [])
    .slice(0, 8)
    .map((error) => `${error.instancePath || "$"} ${error.message ?? error.keyword}`)
    .join("; ");
  throw new Error(`Snapshot violates the authoritative Pintia v2 JSON Schema: ${detail || "unknown violation"}.`);
}

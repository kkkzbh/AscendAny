import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { promisify } from "node:util";
import { cp, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { SCHEMA_SHA256 } from "../src/domain/types";

const execute = promisify(execFile);
const extensionRoot = fileURLToPath(new URL("../", import.meta.url));
const schemaRelativePath = "contracts/pintia/ascendany.pintia.snapshot.v2.schema.json";

describe("extension build contract", () => {
  it("uses a deterministic CSP-safe standalone validator generated from the authoritative schema", async () => {
    const generated = await readFile(
      join(extensionRoot, ".generated", "authoritative-schema-validator.mjs"),
      "utf8",
    );
    const metadata = JSON.parse(await readFile(
      join(extensionRoot, ".generated", "authoritative-schema-validator.meta.json"),
      "utf8",
    )) as { schemaSha256?: unknown; generatedSha256?: unknown };

    expect(metadata.schemaSha256).toBe(SCHEMA_SHA256);
    expect(metadata.generatedSha256).toBe(
      createHash("sha256").update(generated).digest("hex"),
    );
    expect(generated).not.toMatch(/\bnew Function\b|\beval\s*\(/);
  });

  it("fails the direct build when domain SCHEMA_SHA256 drifts from schema bytes", async () => {
    const temporaryRepository = await mkdtemp(join(tmpdir(), "ascendany-pintia-build-"));
    const temporaryExtension = join(
      temporaryRepository,
      "tools",
      "pintia-exporter-extension",
    );
    try {
      await mkdir(join(temporaryExtension, "scripts"), { recursive: true });
      await mkdir(join(temporaryExtension, "src", "domain"), { recursive: true });
      await mkdir(join(temporaryExtension, "src", "static"), { recursive: true });
      await mkdir(join(temporaryRepository, "contracts", "pintia"), { recursive: true });
      await cp(
        join(extensionRoot, "scripts", "build.mjs"),
        join(temporaryExtension, "scripts", "build.mjs"),
      );
      await cp(
        join(extensionRoot, "scripts", "generate-schema-validator.mjs"),
        join(temporaryExtension, "scripts", "generate-schema-validator.mjs"),
      );
      await cp(join(extensionRoot, "package.json"), join(temporaryExtension, "package.json"));
      await cp(
        join(extensionRoot, "src", "static", "manifest.json"),
        join(temporaryExtension, "src", "static", "manifest.json"),
      );
      await cp(
        fileURLToPath(new URL(`../../../${schemaRelativePath}`, import.meta.url)),
        join(temporaryRepository, schemaRelativePath),
      );
      await symlink(
        join(extensionRoot, "node_modules"),
        join(temporaryExtension, "node_modules"),
        "dir",
      );

      const domainTypes = await readFile(
        join(extensionRoot, "src", "domain", "types.ts"),
        "utf8",
      );
      const driftedDomainTypes = domainTypes.replace(
        /SCHEMA_SHA256 = "[0-9a-f]{64}"/,
        `SCHEMA_SHA256 = "${"0".repeat(64)}"`,
      );
      expect(driftedDomainTypes).not.toBe(domainTypes);
      await writeFile(
        join(temporaryExtension, "src", "domain", "types.ts"),
        driftedDomainTypes,
        "utf8",
      );

      let stderr = "";
      try {
        await execute(process.execPath, ["scripts/build.mjs"], { cwd: temporaryExtension });
      } catch (error: unknown) {
        stderr = String((error as { stderr?: unknown }).stderr ?? error);
      }
      expect(stderr).toContain("Pintia v2 schema digest mismatch");
      expect(stderr).toContain(`domain ${"0".repeat(64)}`);
      expect(stderr).toContain(`actual ${SCHEMA_SHA256}`);
    } finally {
      await rm(temporaryRepository, { recursive: true, force: true });
    }
  });
});

#!/usr/bin/env node

import { readdir } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const applicationRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const ignoredDirectories = new Set(["dist", "dist-electron", "node_modules", "release"]);
const allowedDeclarations = new Set(["src/vite-env.d.ts"]);
const emittedFiles = [];

async function visit(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (entry.isDirectory() && ignoredDirectories.has(entry.name)) continue;
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      await visit(path);
      continue;
    }
    if (!entry.isFile()) continue;
    const localPath = relative(applicationRoot, path).split("\\").join("/");
    if (localPath.endsWith(".d.ts") && !allowedDeclarations.has(localPath)) {
      emittedFiles.push(localPath);
    }
  }
}

await visit(applicationRoot);
if (emittedFiles.length > 0) {
  throw new Error(`Desktop typecheck polluted its source tree:\n${emittedFiles.sort().join("\n")}`);
}

process.stdout.write("Desktop typecheck left the source tree clean.\n");

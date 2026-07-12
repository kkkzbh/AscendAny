import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const packageRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const result = await build({
  absWorkingDir: packageRoot,
  entryPoints: ["src/index.ts"],
  bundle: true,
  platform: "browser",
  format: "esm",
  target: "es2022",
  treeShaking: true,
  write: false,
  logLevel: "silent",
});

const bytes = result.outputFiles.reduce((total, file) => total + file.contents.byteLength, 0);
if (result.outputFiles.length === 0 || bytes === 0) {
  throw new Error("Browser SDK bundle verification produced no runtime output.");
}

process.stdout.write(`Browser SDK bundle verified (${bytes} bytes).\n`);

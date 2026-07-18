#!/usr/bin/env node

import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import {
  chmod,
  copyFile,
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rename,
  rm,
  writeFile,
} from "node:fs/promises";
import { dirname, extname, join, posix, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "parse5";

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const packageRoot = join(repositoryRoot, "backend", "internal", "publicdelivery");
const committedAssets = join(packageRoot, "assets");
const flockBinary = "/usr/bin/flock";
const lockHeldEnvironmentName = "ASCENDANY_PUBLIC_ASSET_LOCK_HELD";
const manifestSchema = "ascendany.public-assets.v1";
const maximumFiles = 512;
const maximumAssetBytes = 4 * 1024 * 1024;
const maximumManifestBytes = 96 * 1024;
const maximumTotalBytes = 16 * 1024 * 1024;

export function productionApplicationBuildPlan(desktopOutputRoot) {
  const desktopOutput = resolve(desktopOutputRoot);
  return [
    {
      packageName: "@ascendany/site",
      source: "apps/site/dist",
      target: "site",
      commands: [
        ["pnpm", "--filter", "@ascendany/site", "build"],
      ],
    },
    {
      packageName: "@ascendany/desktop",
      source: desktopOutput,
      target: "app",
      commands: [
        [
          "pnpm",
          "--dir",
          "apps/desktop",
          "exec",
          "tsc",
          "--noEmit",
          "-p",
          "tsconfig.json",
          "--preserveSymlinks",
        ],
        [
          "pnpm",
          "--dir",
          "apps/desktop",
          "exec",
          "vite",
          "build",
          "--config",
          "vite.web.config.ts",
          "--base",
          "/app/",
          "--outDir",
          desktopOutput,
          "--emptyOutDir",
        ],
      ],
    },
    {
      packageName: "@ascendany/import-console",
      source: "apps/import-console/dist",
      target: "admin",
      commands: [
        ["pnpm", "--filter", "@ascendany/import-console", "build"],
      ],
    },
  ];
}

function usage() {
  process.stderr.write(
    "usage: node tools/build-v2-public-assets.mjs (--write|--check)\n",
  );
}

function compareText(left, right) {
  return left < right ? -1 : left > right ? 1 : 0;
}

function canonicalBuildEnvironment() {
  const environment = { ...process.env };
  for (const name of Object.keys(environment)) {
    if (name.startsWith("VITE_")) delete environment[name];
  }
  delete environment.NODE_OPTIONS;
  delete environment[lockHeldEnvironmentName];
  return environment;
}

function buildApplications(desktopOutputRoot) {
  const environment = canonicalBuildEnvironment();
  const plan = productionApplicationBuildPlan(desktopOutputRoot);
  for (const application of plan) {
    for (const [command, ...args] of application.commands) {
      const result = spawnSync(command, args, {
        cwd: repositoryRoot,
        env: environment,
        stdio: "inherit",
      });
      if (result.error) throw result.error;
      if (result.status !== 0) {
        throw new Error(
          `${application.packageName} build command exited with ${result.status}`,
        );
      }
    }
  }
  return plan.map(({ source, target }) => ({ source, target }));
}

function portablePath(root, path) {
  return relative(root, path).split(sep).join("/");
}

async function copyClosedTree(sourceRoot, targetRoot) {
  const sourceMetadata = await lstat(sourceRoot);
  if (!sourceMetadata.isDirectory() || sourceMetadata.isSymbolicLink()) {
    throw new Error(`build output is not one real directory: ${sourceRoot}`);
  }
  await mkdir(targetRoot, { recursive: false, mode: 0o755 });

  async function visit(sourceDirectory, targetDirectory) {
    const entries = await readdir(sourceDirectory, { withFileTypes: true });
    entries.sort((left, right) => compareText(left.name, right.name));
    for (const entry of entries) {
      const sourcePath = join(sourceDirectory, entry.name);
      const targetPath = join(targetDirectory, entry.name);
      const metadata = await lstat(sourcePath);
      if (metadata.isSymbolicLink()) {
        throw new Error(`build output contains a symbolic link: ${sourcePath}`);
      }
      if (metadata.isDirectory()) {
        await mkdir(targetPath, { mode: 0o755 });
        await visit(sourcePath, targetPath);
        continue;
      }
      if (!metadata.isFile()) {
        throw new Error(`build output contains a non-regular entry: ${sourcePath}`);
      }
      await copyFile(sourcePath, targetPath);
      await chmod(targetPath, 0o644);
    }
  }

  await visit(sourceRoot, targetRoot);
}

async function listRegularFiles(root) {
  const files = [];
  async function visit(directory) {
    const entries = await readdir(directory, { withFileTypes: true });
    entries.sort((left, right) => compareText(left.name, right.name));
    for (const entry of entries) {
      const path = join(directory, entry.name);
      const metadata = await lstat(path);
      if (metadata.isSymbolicLink()) {
        throw new Error(`asset tree contains a symbolic link: ${path}`);
      }
      if (metadata.isDirectory()) {
        await visit(path);
      } else if (metadata.isFile()) {
        files.push(portablePath(root, path));
      } else {
        throw new Error(`asset tree contains a non-regular entry: ${path}`);
      }
    }
  }
  await visit(root);
  files.sort(compareText);
  return files;
}

function embeddedPathForAbsoluteReference(owner, reference) {
  const publicPath = reference.split(/[?#]/u, 1)[0];
  const bases = { site: "/", app: "/app/", admin: "/admin/" };
  const base = bases[owner];
  if (base === undefined || !publicPath.startsWith(base)) {
    throw new Error(`${owner} build references a resource outside its public base: ${reference}`);
  }
  if (owner === "site" && (publicPath.startsWith("/app/") || publicPath.startsWith("/admin/"))) {
    throw new Error(`site build references another application's resource: ${reference}`);
  }
  const remainder = publicPath.slice(base.length);
  if (remainder.length === 0) {
    throw new Error(`${owner} build references its route root as a resource`);
  }
  return `${owner}/${remainder}`;
}

function embeddedPathForStylesheetReference(stylesheetPath, reference) {
  if (reference.startsWith("#") || reference.startsWith("?")) return null;
  const unqualified = reference.split(/[?#]/u, 1)[0];
  if (unqualified.startsWith("data:")) return null;
  if (/^(?:[a-z][a-z0-9+.-]*:)?\/\//iu.test(unqualified)) {
    throw new Error(`stylesheet references an external resource: ${stylesheetPath}`);
  }
  const owner = stylesheetPath.split("/", 1)[0];
  if (unqualified.startsWith("/")) {
    return embeddedPathForAbsoluteReference(owner, unqualified);
  }
  const resolved = posix.normalize(posix.join(posix.dirname(stylesheetPath), unqualified));
  if (!resolved.startsWith(`${owner}/`)) {
    throw new Error(`stylesheet reference escapes its ownership root: ${stylesheetPath}`);
  }
  return resolved;
}

function validateHTMLReference(owner, name, reference, assetPaths) {
  if (/^(?:[a-z][a-z0-9+.-]*:)?\/\//iu.test(reference) || /^[a-z][a-z0-9+.-]*:/iu.test(reference)) {
    throw new Error(`${name} build references an external HTML resource: ${reference}`);
  }
  if (!reference.startsWith("/")) {
    throw new Error(`${name} build resource is not root-relative: ${reference}`);
  }
  const embeddedPath = embeddedPathForAbsoluteReference(owner, reference);
  if (!assetPaths.has(embeddedPath)) {
    throw new Error(`${name} build references a missing asset: ${reference}`);
  }
  return embeddedPath;
}

function validateSourceSet(owner, name, value, assetPaths) {
  const candidates = value.split(",");
  if (candidates.length === 0) {
    throw new Error(`${name} build contains an empty srcset`);
  }
  for (const candidate of candidates) {
    const fields = candidate.trim().split(/\s+/u);
    if (fields.length === 0 || fields.length > 2 || fields[0] === "") {
      throw new Error(`${name} build contains an invalid srcset`);
    }
    if (fields.length === 2 && !/^(?:[1-9][0-9]*w|(?:[1-9][0-9]*)(?:\.[0-9]+)?x)$/u.test(fields[1])) {
      throw new Error(`${name} build contains an invalid srcset descriptor`);
    }
    validateHTMLReference(owner, name, fields[0], assetPaths);
  }
}

function validateHTMLDocument(owner, name, document, assetPaths) {
  const parseErrors = [];
  const root = parse(document, { onParseError: (error) => parseErrors.push(error) });
  if (parseErrors.length > 0) {
    throw new Error(`${name} build contains invalid HTML: ${parseErrors[0].code}`);
  }

  const resourceAttributes = new Map([
    ["audio", ["src"]],
    ["body", ["background"]],
    ["embed", ["src"]],
    ["feimage", ["href"]],
    ["frame", ["src"]],
    ["html", ["manifest"]],
    ["iframe", ["src"]],
    ["image", ["href"]],
    ["img", ["src"]],
    ["input", ["src"]],
    ["link", ["href"]],
    ["object", ["data"]],
    ["source", ["src"]],
    ["table", ["background"]],
    ["td", ["background"]],
    ["th", ["background"]],
    ["track", ["src"]],
    ["use", ["href"]],
    ["video", ["poster", "src"]],
  ]);
  const entryScripts = [];
  const entryStylesheets = [];

  function visit(node) {
    if (node.tagName !== undefined) {
      const tagName = node.tagName.toLowerCase();
      const attributes = new Map(node.attrs.map((attribute) => [attribute.name, attribute.value]));
      if (tagName === "base") {
        throw new Error(`${name} build contains a base element forbidden by the static CSP`);
      }
      if (tagName === "style") {
        throw new Error(`${name} build contains an inline style element forbidden by the static CSP`);
      }
      if (tagName === "noscript") {
        throw new Error(`${name} build contains a noscript branch outside the asset closure`);
      }
      for (const attribute of node.attrs) {
        if (attribute.name.startsWith("on") || attribute.name === "style") {
          throw new Error(`${name} build contains an inline executable attribute forbidden by the static CSP`);
        }
        if (attribute.name === "ping" || attribute.name === "srcdoc") {
          throw new Error(`${name} build contains an active HTML attribute outside the asset closure`);
        }
      }
      if (tagName === "meta" && attributes.get("http-equiv")?.toLowerCase() === "refresh") {
        throw new Error(`${name} build contains a meta refresh`);
      }
      if (tagName === "script") {
        if (!attributes.has("src")) {
          throw new Error(`${name} build contains an inline script forbidden by the static CSP`);
        }
        if (attributes.get("type") !== "module") {
          throw new Error(`${name} build entry script is not an ES module`);
        }
        if (node.childNodes?.some((child) => child.nodeName === "#text" && child.value.trim() !== "")) {
          throw new Error(`${name} build contains script body bytes outside the asset closure`);
        }
        entryScripts.push(validateHTMLReference(owner, name, attributes.get("src"), assetPaths));
      }
      for (const attributeName of resourceAttributes.get(tagName) ?? []) {
        const reference = attributes.get(attributeName);
        if (reference !== undefined) {
          if ((tagName === "image" || tagName === "use") && reference.startsWith("#")) {
            continue;
          }
          const embeddedPath = validateHTMLReference(owner, name, reference, assetPaths);
          if (tagName === "link" && attributeName === "href" &&
              attributes.get("rel")?.toLowerCase().split(/\s+/u).includes("stylesheet")) {
            entryStylesheets.push(embeddedPath);
          }
        }
      }
      if ((tagName === "img" || tagName === "source") && attributes.has("srcset")) {
        validateSourceSet(owner, name, attributes.get("srcset"), assetPaths);
      }
      if (tagName === "link" && attributes.has("imagesrcset")) {
        validateSourceSet(owner, name, attributes.get("imagesrcset"), assetPaths);
      }
    }
    for (const child of node.childNodes ?? []) visit(child);
    if (node.content !== undefined) visit(node.content);
  }

  visit(root);
  for (const [kind, entries] of [["script", entryScripts], ["stylesheet", entryStylesheets]]) {
    if (entries.length === 0) {
      throw new Error(`${name} build contains no active ${kind} entrypoint`);
    }
    if (entries.some((entry) => !entry.startsWith(`${owner}/assets/`))) {
      throw new Error(`${name} build ${kind} entrypoint is outside its fixed assets base`);
    }
  }
}

function validateStylesheet(path, stylesheet, assetPaths) {
  const comments = Array.from(stylesheet.matchAll(/\/\*[\s\S]*?\*\//gu));
  const commentMarkers = Array.from(stylesheet.matchAll(/\/\*|\*\//gu));
  if (commentMarkers.length !== comments.length * 2) {
    throw new Error(`stylesheet contains an unterminated comment: ${path}`);
  }
  for (const comment of comments) {
    if (!/^\/\*! tailwindcss v[0-9]+\.[0-9]+\.[0-9]+ \| MIT License \| https:\/\/tailwindcss\.com \*\/$/u.test(comment[0])) {
      throw new Error(`stylesheet contains a comment outside the closed CSS grammar: ${path}`);
    }
  }
  const activeStylesheet = stylesheet.replace(/\/\*[\s\S]*?\*\//gu, "");
  if (/@import\b/iu.test(activeStylesheet)) {
    throw new Error(`stylesheet contains @import outside the asset closure: ${path}`);
  }
  for (let index = 0; index < activeStylesheet.length; index += 1) {
    if (activeStylesheet[index] !== "\\") continue;
    const escaped = activeStylesheet.codePointAt(index + 1);
    const isASCIIPunctuation = escaped !== undefined && (
      (escaped >= 0x21 && escaped <= 0x2f)
      || (escaped >= 0x3a && escaped <= 0x40)
      || (escaped >= 0x5b && escaped <= 0x60)
      || (escaped >= 0x7b && escaped <= 0x7e)
    );
    if (!isASCIIPunctuation) {
      throw new Error(`stylesheet contains an encoded token outside the closed CSS grammar: ${path}`);
    }
    index += 1;
  }
  if (/(?:-webkit-)?image-set\s*\(/iu.test(activeStylesheet)) {
    throw new Error(`stylesheet contains image-set outside the closed CSS grammar: ${path}`);
  }
  const functionCount = Array.from(activeStylesheet.matchAll(/\burl\s*\(/giu)).length;
  const matches = Array.from(activeStylesheet.matchAll(/\burl\s*\(\s*(["']?)([^"')]+)\1\s*\)/giu));
  if (matches.length !== functionCount) {
    throw new Error(`stylesheet contains an invalid url() outside the closed CSS grammar: ${path}`);
  }
  for (const match of matches) {
    const embeddedPath = embeddedPathForStylesheetReference(path, match[2].trim());
    if (embeddedPath !== null && !assetPaths.has(embeddedPath)) {
      throw new Error(`stylesheet references a missing asset: ${path}`);
    }
  }
}

function validateAssetPath(path) {
  if (
    path.length === 0
    || path.length > 512
    || path.includes("\\")
    || path.startsWith("/")
    || path.split("/").some((component) => component === "" || component === "." || component === "..")
    || !/^[0-9A-Za-z._/-]+$/.test(path)
    || !["site/", "app/", "admin/"].some((prefix) => path.startsWith(prefix))
  ) {
    throw new Error(`asset path is outside the closed contract: ${path}`);
  }
  const allowedExtensions = new Set([
    ".css",
    ".html",
    ".ico",
    ".js",
    ".json",
    ".png",
    ".svg",
    ".ttf",
    ".webp",
    ".woff",
    ".woff2",
  ]);
  if (!allowedExtensions.has(extname(path))) {
    throw new Error(`asset path has an unsupported content type: ${path}`);
  }
}

function cacheClass(path) {
  if (!path.includes("/assets/")) return "revalidate";
  const filename = path.slice(path.lastIndexOf("/") + 1);
  if (!/-[0-9A-Za-z_-]{8,}[.][0-9A-Za-z]+$/.test(filename)) {
    throw new Error(`immutable asset lacks a Vite content hash: ${path}`);
  }
  return "immutable";
}

export async function assertBuildBaseContracts(root) {
  const siteIndex = await readFile(join(root, "site", "index.html"), "utf8");
  const appIndex = await readFile(join(root, "app", "index.html"), "utf8");
  const adminIndex = await readFile(join(root, "admin", "index.html"), "utf8");

  const assetPaths = new Set(await listRegularFiles(root));
  for (const [owner, name, document] of [
    ["site", "site", siteIndex],
    ["app", "Agent frontend", appIndex],
    ["admin", "import console", adminIndex],
  ]) {
    validateHTMLDocument(owner, name, document, assetPaths);
  }

  for (const path of assetPaths) {
    if (extname(path) !== ".css") continue;
    const stylesheet = await readFile(join(root, path), "utf8");
    validateStylesheet(path, stylesheet, assetPaths);
  }
}

export async function composeAssetTree(root, sourceDefinitions, sourceRoot = repositoryRoot) {
  if (sourceDefinitions === undefined) {
    throw new Error("public asset source definitions are required");
  }
  await chmod(root, 0o755);
  for (const source of sourceDefinitions) {
    await copyClosedTree(
      resolve(sourceRoot, source.source),
      join(root, source.target),
    );
  }
  await assertBuildBaseContracts(root);

  const paths = await listRegularFiles(root);
  if (paths.length === 0 || paths.length > maximumFiles) {
    throw new Error(`public asset count ${paths.length} exceeds the closed limit`);
  }

  let totalBytes = 0;
  const files = [];
  for (const path of paths) {
    validateAssetPath(path);
    const body = await readFile(join(root, path));
    if (body.length > maximumAssetBytes) {
      throw new Error(`public asset exceeds the per-file byte limit: ${path}`);
    }
    totalBytes += body.length;
    files.push({
      path,
      sha256: createHash("sha256").update(body).digest("hex"),
      size: body.length,
      cache: cacheClass(path),
    });
  }
  if (totalBytes > maximumTotalBytes) {
    throw new Error(`public asset bytes ${totalBytes} exceed the closed limit`);
  }

  const manifest = {
    schema: manifestSchema,
    routes: {
      site: "/",
      studentWeb: "/app/",
      importConsole: "/admin/",
    },
    files,
  };
  const manifestBody = `${JSON.stringify(manifest, null, 2)}\n`;
  if (Buffer.byteLength(manifestBody, "utf8") > maximumManifestBytes) {
    throw new Error("public asset manifest exceeds the compiled byte limit");
  }
  await writeFile(
    join(root, "manifest.json"),
    manifestBody,
    { encoding: "utf8", mode: 0o644 },
  );
}

export function advisoryLockArguments(lockPath, command, args = []) {
  return [
    "--exclusive",
    "--nonblock",
    "--conflict-exit-code",
    "75",
    lockPath,
    command,
    ...args,
  ];
}

async function compareTrees(expectedRoot, actualRoot) {
  const expected = await listRegularFiles(expectedRoot);
  const actual = await listRegularFiles(actualRoot);
  if (JSON.stringify(actual) !== JSON.stringify(expected)) {
    throw new Error(
      `committed public asset paths differ\nexpected=${expected.join(",")}\nactual=${actual.join(",")}`,
    );
  }
  for (const path of expected) {
    const [expectedBody, actualBody] = await Promise.all([
      readFile(join(expectedRoot, path)),
      readFile(join(actualRoot, path)),
    ]);
    if (!expectedBody.equals(actualBody)) {
      throw new Error(`committed public asset bytes differ: ${path}`);
    }
  }
}

export async function publishGeneratedTree(
  generatedRoot,
  destinationRoot = committedAssets,
  operations = { rename, rm },
) {
  const oldRoot = join(dirname(destinationRoot), `.assets-old-${process.pid}`);
  let movedOld = false;
  try {
    try {
      await operations.rename(destinationRoot, oldRoot);
      movedOld = true;
    } catch (error) {
      if (error?.code !== "ENOENT") throw error;
    }
    await operations.rename(generatedRoot, destinationRoot);
    if (movedOld) await operations.rm(oldRoot, { recursive: true, force: false });
  } catch (error) {
    if (movedOld) {
      await operations.rm(destinationRoot, { recursive: true, force: true });
      await operations.rename(oldRoot, destinationRoot);
    }
    throw error;
  }
}

async function main() {
  const mode = process.argv[2];
  if (process.argv.length !== 3 || (mode !== "--write" && mode !== "--check")) {
    usage();
    process.exitCode = 2;
    return;
  }

  await mkdir(packageRoot, { recursive: true, mode: 0o755 });
  if (process.env[lockHeldEnvironmentName] !== "1") {
    const scriptPath = fileURLToPath(import.meta.url);
    const result = spawnSync(
      flockBinary,
      advisoryLockArguments(packageRoot, process.execPath, [scriptPath, mode]),
      {
        cwd: repositoryRoot,
        env: { ...process.env, [lockHeldEnvironmentName]: "1" },
        stdio: "inherit",
      },
    );
    if (result.error) throw result.error;
    if (result.status === 75) {
      throw new Error("public asset generator is already running");
    }
    if (result.status === null) {
      throw new Error(`locked public asset generator terminated by ${result.signal ?? "an unknown signal"}`);
    }
    process.exitCode = result.status;
    return;
  }

  const applicationBuildRoot = await mkdtemp(
    join(packageRoot, ".application-build-"),
  );
  try {
    const generatedRoot = await mkdtemp(join(packageRoot, ".assets-build-"));
    try {
      const builtSources = buildApplications(join(applicationBuildRoot, "app"));
      await composeAssetTree(generatedRoot, builtSources);
      if (mode === "--check") {
        await compareTrees(generatedRoot, committedAssets);
        process.stdout.write("AscendAny v2 public assets match the canonical builds.\n");
        return;
      }
      await publishGeneratedTree(generatedRoot);
      process.stdout.write("AscendAny v2 public assets regenerated.\n");
    } finally {
      await rm(generatedRoot, { recursive: true, force: true });
    }
  } finally {
    await rm(applicationBuildRoot, { recursive: true, force: true });
  }
}

if (resolve(process.argv[1] ?? "") === fileURLToPath(import.meta.url)) {
  await main();
}

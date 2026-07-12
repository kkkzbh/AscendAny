import { createReadStream } from "node:fs";
import { access, readFile, stat } from "node:fs/promises";
import { createServer } from "node:http";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

function fail(message) {
  throw new Error(message);
}

function parseArguments(argv) {
  if (argv.length !== 2 || argv[0] !== "--server-base-url") {
    fail("usage: node tools/v2-full-e2e-app-smoke.mjs --server-base-url http://loopback:port");
  }
  const baseURL = new URL(argv[1]);
  if (
    baseURL.protocol !== "http:"
    || baseURL.username !== ""
    || baseURL.password !== ""
    || baseURL.pathname !== "/"
    || baseURL.search !== ""
    || baseURL.hash !== ""
  ) {
    fail("server base URL must be one canonical loopback HTTP origin");
  }
  return baseURL;
}

function referencedAssets(html) {
  const references = new Set();
  const expression = /(?:src|href)="([^"?#]+\.(?:css|js))"/gu;
  for (const match of html.matchAll(expression)) {
    references.add(match[1]);
  }
  if (references.size === 0) {
    fail("application HTML has no JavaScript or CSS entrypoint");
  }
  return [...references].sort();
}

async function smokeDocument(baseURL, route, label) {
  const documentURL = new URL(route, baseURL);
  const response = await fetch(documentURL, {
    headers: {
      Origin: baseURL.origin,
      "CF-Connecting-IP": "203.0.113.19",
    },
    redirect: "error",
  });
  if (!response.ok || !(response.headers.get("content-type") ?? "").startsWith("text/html")) {
    fail(`${label} index did not return HTML over HTTP`);
  }
  const html = await response.text();
  if (!html.includes('id="root"')) {
    fail(`${label} index is missing the React mount point`);
  }
  for (const reference of referencedAssets(html)) {
    const asset = await fetch(new URL(reference, documentURL), {
      headers: {
        Origin: baseURL.origin,
        "CF-Connecting-IP": "203.0.113.19",
      },
      redirect: "error",
    });
    if (!asset.ok || (await asset.arrayBuffer()).byteLength === 0) {
      fail(`${label} asset is unavailable: ${reference}`);
    }
  }
}

function contentType(filePath) {
  switch (path.extname(filePath)) {
    case ".html":
      return "text/html; charset=utf-8";
    case ".js":
      return "text/javascript; charset=utf-8";
    case ".css":
      return "text/css; charset=utf-8";
    case ".png":
      return "image/png";
    case ".svg":
      return "image/svg+xml";
    default:
      return "application/octet-stream";
  }
}

async function withStaticServer(root, operation) {
  const canonicalRoot = path.resolve(root);
  await access(path.join(canonicalRoot, "index.html"));
  const server = createServer(async (request, response) => {
    try {
      const requestURL = new URL(request.url ?? "/", "http://127.0.0.1");
      const decoded = decodeURIComponent(requestURL.pathname);
      const relative = decoded === "/" ? "index.html" : decoded.slice(1);
      const filePath = path.resolve(canonicalRoot, relative);
      if (!filePath.startsWith(`${canonicalRoot}${path.sep}`)) {
        response.writeHead(400).end();
        return;
      }
      const metadata = await stat(filePath);
      if (!metadata.isFile()) {
        response.writeHead(404).end();
        return;
      }
      response.writeHead(200, {
        "Content-Type": contentType(filePath),
        "Content-Length": String(metadata.size),
        "Cache-Control": "no-store",
      });
      createReadStream(filePath).pipe(response);
    } catch {
      response.writeHead(404).end();
    }
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  if (address === null || typeof address === "string") {
    server.close();
    fail("static smoke server did not expose a TCP address");
  }
  try {
    await operation(new URL(`http://127.0.0.1:${address.port}/`));
  } finally {
    await new Promise((resolve, reject) => {
      server.close((error) => (error === undefined ? resolve() : reject(error)));
    });
  }
}

async function assertElectronRuntimeClosure() {
  for (const relative of ["main.js", "preload.js"]) {
    const filePath = path.join(repositoryRoot, "apps/desktop/dist-electron", relative);
    const metadata = await stat(filePath);
    if (!metadata.isFile() || metadata.size === 0) {
      fail(`Electron runtime output is unavailable: ${relative}`);
    }
    const bytes = await readFile(filePath);
    if (bytes.includes(Buffer.from("sourceMappingURL=data:"))) {
      fail(`Electron runtime output contains an inline source map: ${relative}`);
    }
  }
}

const serverBaseURL = parseArguments(process.argv.slice(2));
await smokeDocument(serverBaseURL, "/", "site");
await smokeDocument(serverBaseURL, "/app/", "student web");
await smokeDocument(serverBaseURL, "/admin/", "import console");
await withStaticServer(path.join(repositoryRoot, "apps/mobile/dist"), async (baseURL) => {
  await smokeDocument(baseURL, "/", "mobile preview");
});
await withStaticServer(path.join(repositoryRoot, "apps/desktop/dist"), async (baseURL) => {
  await smokeDocument(baseURL, "/", "Electron renderer");
});
await assertElectronRuntimeClosure();

process.stdout.write(`${JSON.stringify({
  schema: "ascendany.full-e2e.app-smoke.v1",
  site: true,
  web: true,
  importConsole: true,
  mobilePreview: true,
  electronRenderer: true,
})}\n`);

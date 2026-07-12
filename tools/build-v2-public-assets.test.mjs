import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { once } from "node:events";
import {
  mkdir,
  mkdtemp,
  readFile,
  rename,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  advisoryLockArguments,
  composeAssetTree,
  publishGeneratedTree,
} from "./build-v2-public-assets.mjs";

const flockBinary = "/usr/bin/flock";

const sourceDefinitions = [
  { source: "site", target: "site" },
  { source: "app", target: "app" },
  { source: "admin", target: "admin" },
];

function htmlDocument(content) {
  return `<!doctype html><html lang="en"><head>${content}</head><body></body></html>`;
}

async function createFixture() {
  const root = await mkdtemp(join(tmpdir(), "ascendany-public-assets-test-"));
  const sourceRoot = join(root, "source");
  const outputRoot = join(root, "output");
  await mkdir(outputRoot);

  const configurations = [
    { owner: "site", base: "/" },
    { owner: "app", base: "/app/" },
    { owner: "admin", base: "/admin/" },
  ];
  for (const configuration of configurations) {
    const packageRoot = join(sourceRoot, configuration.owner);
    await mkdir(join(packageRoot, "assets"), { recursive: true });
    await writeFile(
      join(packageRoot, "index.html"),
      htmlDocument(
        `<script type="module" src="${configuration.base}assets/main-12345678.js"></script>`
          + `<link rel="stylesheet" href="${configuration.base}assets/main-12345678.css">`,
      ),
    );
    await writeFile(join(packageRoot, "assets", "main-12345678.js"), "export {};\n");
    await writeFile(join(packageRoot, "assets", "main-12345678.css"), "body {}\n");
  }

  return {
    root,
    sourceRoot,
    outputRoot,
    async cleanup() {
      await rm(root, { recursive: true, force: true });
    },
  };
}

test("canonical fixture produces an exact manifest", async () => {
  const fixture = await createFixture();
  try {
    await composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot);
    const manifest = JSON.parse(await readFile(join(fixture.outputRoot, "manifest.json"), "utf8"));
    assert.equal(manifest.schema, "ascendany.public-assets.v1");
    assert.equal(manifest.files.length, 9);
  } finally {
    await fixture.cleanup();
  }
});

test("missing HTML resource fails the closed build", async () => {
  const fixture = await createFixture();
  try {
    await rm(join(fixture.sourceRoot, "site", "assets", "main-12345678.css"));
    await assert.rejects(
      composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot),
      /references a missing asset/u,
    );
  } finally {
    await fixture.cleanup();
  }
});

test("missing stylesheet resource fails the closed build", async () => {
  const fixture = await createFixture();
  try {
    await writeFile(
      join(fixture.sourceRoot, "site", "assets", "main-12345678.css"),
      "body { background: url('/assets/missing.png'); }\n",
    );
    await assert.rejects(
      composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot),
      /stylesheet references a missing asset/u,
    );
  } finally {
    await fixture.cleanup();
  }
});

test("inline script and external resource references fail closed", async (t) => {
  await t.test("inline script", async () => {
    const fixture = await createFixture();
    try {
      await writeFile(
        join(fixture.sourceRoot, "site", "index.html"),
        htmlDocument('<script>globalThis.compromised = true;</script><script src="/assets/main-12345678.js"></script><link href="/assets/main-12345678.css">'),
      );
      await assert.rejects(
        composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot),
        /inline script/u,
      );
    } finally {
      await fixture.cleanup();
    }
  });

  await t.test("external HTML resource", async () => {
    const fixture = await createFixture();
    try {
      await writeFile(
        join(fixture.sourceRoot, "site", "index.html"),
        htmlDocument('<script type="module" src="/assets/main-12345678.js"></script><link rel="stylesheet" href="/assets/main-12345678.css"><link href="https://example.test/external.css">'),
      );
      await assert.rejects(
        composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot),
        /external HTML resource/u,
      );
    } finally {
      await fixture.cleanup();
    }
  });

  await t.test("external stylesheet resource", async () => {
    const fixture = await createFixture();
    try {
      await writeFile(
        join(fixture.sourceRoot, "site", "assets", "main-12345678.css"),
        "body { background: url('https://example.test/image.png'); }\n",
      );
      await assert.rejects(
        composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot),
        /stylesheet references an external resource/u,
      );
    } finally {
      await fixture.cleanup();
    }
  });
});

test("HTML parser closes active resource and exact attribute bypasses", async (t) => {
  await t.test("data attributes cannot satisfy active entrypoints", async () => {
    const fixture = await createFixture();
    try {
      await writeFile(
        join(fixture.sourceRoot, "site", "index.html"),
        htmlDocument('<div data-src="/assets/main-12345678.js" data-href="/assets/main-12345678.css"></div>'),
      );
      await assert.rejects(
        composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot),
        /contains no active script entrypoint/u,
      );
    } finally {
      await fixture.cleanup();
    }
  });

  await t.test("entrypoints must remain under the fixed assets base", async () => {
    const fixture = await createFixture();
    try {
      await writeFile(join(fixture.sourceRoot, "site", "entry.js"), "export {};\n");
      await writeFile(join(fixture.sourceRoot, "site", "entry.css"), "body {}\n");
      await writeFile(
        join(fixture.sourceRoot, "site", "index.html"),
        htmlDocument('<script type="module" src="/entry.js"></script><link rel="stylesheet" href="/entry.css">'),
      );
      await assert.rejects(
        composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot),
        /entrypoint is outside its fixed assets base/u,
      );
    } finally {
      await fixture.cleanup();
    }
  });

  await t.test("data-src cannot authorize an inline script", async () => {
    const fixture = await createFixture();
    try {
      await writeFile(
        join(fixture.sourceRoot, "site", "index.html"),
        htmlDocument('<script data-src="/assets/main-12345678.js">globalThis.compromised = true;</script><script src="/assets/main-12345678.js"></script><link href="/assets/main-12345678.css">'),
      );
      await assert.rejects(
        composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot),
        /inline script/u,
      );
    } finally {
      await fixture.cleanup();
    }
  });

  await t.test("missing image source", async () => {
    const fixture = await createFixture();
    try {
      await writeFile(
        join(fixture.sourceRoot, "site", "index.html"),
        htmlDocument('<script type="module" src="/assets/main-12345678.js"></script><link rel="stylesheet" href="/assets/main-12345678.css"><img src="/assets/missing.png" alt="missing">'),
      );
      await assert.rejects(
        composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot),
        /references a missing asset/u,
      );
    } finally {
      await fixture.cleanup();
    }
  });

  await t.test("missing srcset candidate", async () => {
    const fixture = await createFixture();
    try {
      await writeFile(
        join(fixture.sourceRoot, "site", "index.html"),
        htmlDocument('<script type="module" src="/assets/main-12345678.js"></script><link rel="stylesheet" href="/assets/main-12345678.css"><img srcset="/assets/missing.png 1x" alt="missing">'),
      );
      await assert.rejects(
        composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot),
        /references a missing asset/u,
      );
    } finally {
      await fixture.cleanup();
    }
  });
});

test("CSS import is outside the closed production grammar", async () => {
  const fixture = await createFixture();
  try {
    await writeFile(
      join(fixture.sourceRoot, "site", "assets", "main-12345678.css"),
      '@import "/assets/missing.css";\nbody {}\n',
    );
    await assert.rejects(
      composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot),
      /contains @import outside the asset closure/u,
    );
  } finally {
    await fixture.cleanup();
  }
});

test("CSS comment cannot conceal a resource function", async () => {
  const fixture = await createFixture();
  try {
    await writeFile(
      join(fixture.sourceRoot, "site", "assets", "main-12345678.css"),
      "body { background: u/**/rl('/assets/missing.png'); }\n",
    );
    await assert.rejects(
      composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot),
      /comment outside the closed CSS grammar/u,
    );
  } finally {
    await fixture.cleanup();
  }
});

test("symlink and oversized file fail the closed build", async (t) => {
  await t.test("symlink", async () => {
    const fixture = await createFixture();
    try {
      await symlink(
        join(fixture.sourceRoot, "site", "index.html"),
        join(fixture.sourceRoot, "site", "linked.html"),
      );
      await assert.rejects(
        composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot),
        /symbolic link/u,
      );
    } finally {
      await fixture.cleanup();
    }
  });

  await t.test("oversized regular file", async () => {
    const fixture = await createFixture();
    try {
      await writeFile(
        join(fixture.sourceRoot, "site", "oversized.json"),
        Buffer.alloc((4 * 1024 * 1024) + 1),
      );
      await assert.rejects(
        composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot),
        /per-file byte limit/u,
      );
    } finally {
      await fixture.cleanup();
    }
  });
});

test("oversized manifest fails before publication", async () => {
  const fixture = await createFixture();
  try {
    const directory = "d".repeat(220);
    const dataRoot = join(fixture.sourceRoot, "site", "data", directory);
    await mkdir(dataRoot, { recursive: true });
    for (let index = 0; index < 247; index += 1) {
      const filename = `${String(index).padStart(3, "0")}-${"f".repeat(205)}.json`;
      await writeFile(join(dataRoot, filename), "{}\n");
    }
    await assert.rejects(
      composeAssetTree(fixture.outputRoot, sourceDefinitions, fixture.sourceRoot),
      /manifest exceeds the compiled byte limit/u,
    );
  } finally {
    await fixture.cleanup();
  }
});

test("kernel lock serializes owners and releases after a crash", async () => {
  const lockPath = await mkdtemp(join(tmpdir(), "ascendany-public-assets-lock-test-"));
  try {
    const holder = spawn(
      flockBinary,
      advisoryLockArguments(lockPath, process.execPath, [
        "-e",
        'process.stdout.write("locked\\n"); setTimeout(() => process.kill(process.pid, "SIGKILL"), 250);',
      ]),
      { stdio: ["ignore", "pipe", "inherit"] },
    );
    const holderExited = once(holder, "exit");
    await once(holder.stdout, "data");

    const contender = spawnSync(
      flockBinary,
      advisoryLockArguments(lockPath, process.execPath, ["-e", ""]),
    );
    assert.equal(contender.status, 75);

    await holderExited;
    const recovered = spawnSync(
      flockBinary,
      advisoryLockArguments(lockPath, process.execPath, ["-e", ""]),
    );
    assert.equal(recovered.status, 0);
  } finally {
    await rm(lockPath, { recursive: true, force: true });
  }
});

test("failed publication restores the exact committed tree", async () => {
  const root = await mkdtemp(join(tmpdir(), "ascendany-public-assets-publish-test-"));
  const destinationRoot = join(root, "assets");
  const generatedRoot = join(root, "generated");
  try {
    await mkdir(destinationRoot);
    await mkdir(generatedRoot);
    await writeFile(join(destinationRoot, "identity.txt"), "committed\n");
    await writeFile(join(generatedRoot, "identity.txt"), "generated\n");

    let renameCalls = 0;
    await assert.rejects(
      publishGeneratedTree(generatedRoot, destinationRoot, {
        async rename(source, target) {
          renameCalls += 1;
          if (renameCalls === 2) throw new Error("injected publication failure");
          await rename(source, target);
        },
        rm,
      }),
      /injected publication failure/u,
    );
    assert.equal(await readFile(join(destinationRoot, "identity.txt"), "utf8"), "committed\n");
    assert.equal(await readFile(join(generatedRoot, "identity.txt"), "utf8"), "generated\n");
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

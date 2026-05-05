import { mkdir, readdir, copyFile, readFile, stat, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const root = path.resolve(__dirname, '..')

const srcRoot = path.resolve(root, 'node_modules', 'mathjax', 'es5')
const dstRoot = path.resolve(root, 'public', 'vendor', 'mathjax')

const vendorRoot = path.resolve(root, 'public', 'vendor')

async function exists(p) {
  try {
    await stat(p)
    return true
  } catch {
    return false
  }
}

async function copyDir(srcDir, dstDir) {
  await mkdir(dstDir, { recursive: true })
  const entries = await readdir(srcDir, { withFileTypes: true })
  for (const ent of entries) {
    const src = path.join(srcDir, ent.name)
    const dst = path.join(dstDir, ent.name)
    if (ent.isDirectory()) {
      await copyDir(src, dst)
    } else if (ent.isFile()) {
      await mkdir(path.dirname(dst), { recursive: true })
      await copyFile(src, dst)
    }
  }
}

async function main() {
  const srcScript = path.resolve(srcRoot, 'tex-mml-chtml.js')
  const dstScript = path.resolve(dstRoot, 'tex-mml-chtml.js')

  if (!(await exists(srcScript))) {
    throw new Error(`MathJax not found: ${srcScript}. Did you run npm install?`)
  }

  await mkdir(dstRoot, { recursive: true })
  await copyFile(srcScript, dstScript)

  // Patch CDN fallbacks inside the MathJax bundle so it stays offline-friendly.
  // (These are used by optional accessibility/speech features.)
  try {
    let content = await readFile(dstScript, 'utf8')
    const before = content

    content = content.replace(
      'r.url="https://cdn.jsdelivr.net/npm/speech-rule-engine@"+r.VERSION+"/lib/mathmaps"',
      'r.url=__MJ_BASE__+"vendor/speech-rule-engine@"+r.VERSION+"/lib/mathmaps"',
    )
    content = content.replace(
      'r.WGXpath="https://cdn.jsdelivr.net/npm/wicked-good-xpath@1.3.0/dist/wgxpath.install.js"',
      'r.WGXpath=__MJ_BASE__+"vendor/wicked-good-xpath@1.3.0/dist/wgxpath.install.js"',
    )
    // This path is only used in legacy environments; keep it local as well.
    content = content.replace(
      'SystemExternal.mathmapsIePath="https://cdn.jsdelivr.net/npm/sre-mathmaps-ie@"+variables_1.Variables.VERSION+"mathmaps_ie.js"',
      'SystemExternal.mathmapsIePath=__MJ_BASE__+"vendor/sre-mathmaps-ie/mathmaps_ie.js"',
    )

    if (content !== before) {
      await writeFile(dstScript, content, 'utf8')
    }
  } catch {
    // ignore
  }

  const srcFonts = path.resolve(srcRoot, 'output', 'chtml', 'fonts', 'woff-v2')
  const dstFonts = path.resolve(dstRoot, 'output', 'chtml', 'fonts', 'woff-v2')
  if (await exists(srcFonts)) {
    await copyDir(srcFonts, dstFonts)
  }

  // Vendor Speech Rule Engine mathmaps locally (MathJax a11y can lazy-load these).
  const sreMathmapsSrc = path.resolve(root, 'node_modules', 'speech-rule-engine', 'lib', 'mathmaps')
  const srePkgPath = path.resolve(root, 'node_modules', 'speech-rule-engine', 'package.json')
  if ((await exists(sreMathmapsSrc)) && (await exists(srePkgPath))) {
    try {
      const pkg = JSON.parse(await readFile(srePkgPath, 'utf8'))
      const sreVersion = String(pkg?.version || '').trim() || '4.0.6'
      const sreMathmapsDst = path.resolve(vendorRoot, `speech-rule-engine@${sreVersion}`, 'lib', 'mathmaps')
      await copyDir(sreMathmapsSrc, sreMathmapsDst)
    } catch {
      // ignore
    }
  }

  // Vendor wicked-good-xpath for legacy XPath support (used by SRE in non-standard environments).
  const wgxpathSrc = path.resolve(root, 'node_modules', 'wicked-good-xpath', 'dist', 'wgxpath.install.js')
  if (await exists(wgxpathSrc)) {
    const wgxpathDst = path.resolve(vendorRoot, 'wicked-good-xpath@1.3.0', 'dist', 'wgxpath.install.js')
    await mkdir(path.dirname(wgxpathDst), { recursive: true })
    await copyFile(wgxpathSrc, wgxpathDst)
  }

  // Stub for sre-mathmaps-ie (legacy only). We only need a local file to avoid accidental CDN fetches.
  const ieStub = path.resolve(vendorRoot, 'sre-mathmaps-ie', 'mathmaps_ie.js')
  if (!(await exists(ieStub))) {
    await mkdir(path.dirname(ieStub), { recursive: true })
    await writeFile(ieStub, '/* stub: sre-mathmaps-ie (legacy only) */\n', 'utf8')
  }

  // eslint-disable-next-line no-console
  console.log('[sync-mathjax] OK:', path.relative(root, dstScript))
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error('[sync-mathjax] FAILED:', err && err.message ? err.message : err)
  process.exit(1)
})

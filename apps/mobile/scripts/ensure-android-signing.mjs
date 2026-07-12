import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const mobileRoot = path.resolve(scriptDir, "..");
const appGradlePath = path.join(mobileRoot, "android", "app", "build.gradle");
const signingGradlePath = path.join(mobileRoot, "android-signing.gradle");
const applyLine = "apply from: '../../android-signing.gradle'";

function slashyLiteralCanStart(activePrefix) {
  return /(?:^|[=(:,[\]!&|?{};]|\b(?:case|return|throw))\s*$/u.test(activePrefix);
}

function analyzeActiveLines(source) {
  let state = "code";
  let escaped = false;
  const result = [];

  for (const line of source.split(/\r?\n/u)) {
    let emitLiteral = state === "code";
    let active = "";
    for (let index = 0; index < line.length; index += 1) {
      const current = line[index];
      const next = line[index + 1];

      if (state === "block-comment") {
        if (current === "*" && next === "/") {
          state = "code";
          index += 1;
        }
        continue;
      }

      if (state === "single-quote" || state === "double-quote") {
        if (emitLiteral) active += current;
        const closingQuote = state === "single-quote" ? "'" : '"';
        if (escaped) {
          escaped = false;
        } else if (current === "\\") {
          escaped = true;
        } else if (current === closingQuote) {
          state = "code";
        }
        continue;
      }

      if (state === "triple-single" || state === "triple-double") {
        const delimiter = state === "triple-single" ? "'''" : '\"\"\"';
        if (line.startsWith(delimiter, index)) {
          if (emitLiteral) active += delimiter;
          state = "code";
          index += 2;
        } else if (emitLiteral) {
          active += current;
        }
        continue;
      }

      if (state === "slashy") {
        if (emitLiteral) active += current;
        if (escaped) {
          escaped = false;
        } else if (current === "\\") {
          escaped = true;
        } else if (current === "/") {
          state = "code";
        }
        continue;
      }

      if (state === "dollar-slashy") {
        if (current === "/" && next === "$") {
          if (emitLiteral) active += "/$";
          state = "code";
          index += 1;
        } else if (emitLiteral) {
          active += current;
        }
        continue;
      }

      if (current === "/" && next === "/") {
        break;
      }
      if (current === "/" && next === "*") {
        state = "block-comment";
        index += 1;
        continue;
      }
      if (line.startsWith("'''", index)) {
        active += "'''";
        state = "triple-single";
        emitLiteral = true;
        index += 2;
        continue;
      }
      if (line.startsWith('\"\"\"', index)) {
        active += '\"\"\"';
        state = "triple-double";
        emitLiteral = true;
        index += 2;
        continue;
      }
      if (current === "$" && next === "/") {
        active += "$/";
        state = "dollar-slashy";
        emitLiteral = true;
        index += 1;
        continue;
      }
      if (current === "/" && slashyLiteralCanStart(active)) {
        active += current;
        state = "slashy";
        emitLiteral = true;
        escaped = false;
        continue;
      }
      if (current === "'") {
        state = "single-quote";
        emitLiteral = true;
        escaped = false;
      } else if (current === '"') {
        state = "double-quote";
        emitLiteral = true;
        escaped = false;
      }
      active += current;
    }
    result.push(active.trim());
    if (state !== "code" && state !== "block-comment") {
      emitLiteral = false;
    }
  }

  return { activeLines: result, finalState: state };
}

if (!fs.existsSync(signingGradlePath)) {
  console.error(`Signing config file not found: ${signingGradlePath}`);
  process.exit(1);
}

if (!fs.existsSync(appGradlePath)) {
  console.error(`Android app build.gradle not found: ${appGradlePath}`);
  process.exit(1);
}

const originalContent = fs.readFileSync(appGradlePath, "utf8");
const analysis = analyzeActiveLines(originalContent);
if (analysis.finalState !== "code") {
  console.error(`Android app build.gradle ends inside ${analysis.finalState}: ${appGradlePath}`);
  process.exit(1);
}
const matchingActiveLineIndexes = analysis.activeLines.flatMap((line, index) => (
  line === applyLine ? [index] : []
));
if (matchingActiveLineIndexes.length > 1) {
  console.error(`Android signing patch is applied more than once: ${appGradlePath}`);
  process.exit(1);
}
const conflictingSigningApply = analysis.activeLines.find((line) => (
  line !== applyLine
  && /^apply\s+from\s*:/u.test(line)
  && line.includes("android-signing.gradle")
));
if (conflictingSigningApply) {
  console.error(`Android signing apply line must exactly equal ${JSON.stringify(applyLine)}: ${appGradlePath}`);
  process.exit(1);
}
if (matchingActiveLineIndexes.length === 1) {
  const lastActiveLineIndex = analysis.activeLines.findLastIndex((line) => line.length > 0);
  if (matchingActiveLineIndexes[0] !== lastActiveLineIndex) {
    console.error(`Android signing apply line must be the final active statement: ${appGradlePath}`);
    process.exit(1);
  }
  console.log("Android signing patch already applied.");
  process.exit(0);
}

const contentWithTrailingNewline = originalContent.endsWith("\n")
  ? originalContent
  : `${originalContent}\n`;
const patchedContent = `${contentWithTrailingNewline}\n${applyLine}\n`;
fs.writeFileSync(appGradlePath, patchedContent, "utf8");

console.log("Applied Android signing patch to app/build.gradle.");

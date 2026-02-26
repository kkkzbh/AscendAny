import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const mobileRoot = path.resolve(scriptDir, "..");
const appGradlePath = path.join(mobileRoot, "android", "app", "build.gradle");
const signingGradlePath = path.join(mobileRoot, "android-signing.gradle");
const applyLine = "apply from: '../../android-signing.gradle'";

if (!fs.existsSync(signingGradlePath)) {
  console.error(`Signing config file not found: ${signingGradlePath}`);
  process.exit(1);
}

if (!fs.existsSync(appGradlePath)) {
  console.error(`Android app build.gradle not found: ${appGradlePath}`);
  process.exit(1);
}

const originalContent = fs.readFileSync(appGradlePath, "utf8");
if (originalContent.includes(applyLine)) {
  console.log("Android signing patch already applied.");
  process.exit(0);
}

const contentWithTrailingNewline = originalContent.endsWith("\n")
  ? originalContent
  : `${originalContent}\n`;
const patchedContent = `${contentWithTrailingNewline}\n${applyLine}\n`;
fs.writeFileSync(appGradlePath, patchedContent, "utf8");

console.log("Applied Android signing patch to app/build.gradle.");

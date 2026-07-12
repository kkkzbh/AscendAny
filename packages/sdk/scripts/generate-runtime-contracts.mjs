#!/usr/bin/env node

import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { generateRuntimeContracts } from "./runtime-contracts-generator.mjs";

const packageRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
await generateRuntimeContracts(resolve(packageRoot, "src/generated"));

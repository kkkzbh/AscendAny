import type {
  RecommendationKnowledgeCatalogV1,
  RecommendationKnowledgePointV1,
  RecommendationKnowledgeWeightV1,
  RecommendationProblemAssignmentV1,
} from "@ascendany/sdk";

const CONFIGURATION_KEY = /^[a-z][a-z0-9_.-]{0,127}$/;
const JSON_NUMBER = /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/;
const DECIMAL_NUMBER = /^(-?)([0-9]+)(?:\.([0-9]+))?(?:[eE]([+-]?[0-9]+))?$/;
const SHA256 = /^[0-9a-f]{64}$/;
const CANONICAL_WEIGHT = /^(?:1|0\.[0-9]*[1-9])$/;
const PINTIA_ID = /^[A-Za-z0-9][A-Za-z0-9._:-]*$/;
const MAXIMUM_DOCUMENT_BYTES = 256 << 10;
const MAXIMUM_JSON_DEPTH = 64;
const MAXIMUM_NUMBER_BYTES = 128;
const MAXIMUM_NUMBER_EXPONENT = 8192;
const MAXIMUM_NUMBER_PRECISION = 4096;
const MAXIMUM_CANONICAL_NUMBER_BYTES = 8192;
const utf8 = new TextEncoder();
const powersOfTen = new Map<number, bigint>([[0, 1n]]);

type JsonValue = JsonObjectValue | JsonArrayValue | JsonNumberValue | string | boolean | null;

interface JsonObjectValue {
  readonly kind: "object";
  readonly fields: Map<string, JsonValue>;
}

interface JsonArrayValue {
  readonly kind: "array";
  readonly items: JsonValue[];
}

interface JsonNumberValue {
  readonly kind: "number";
  readonly raw: string;
  readonly rational: DecimalRational;
}

interface DecimalRational {
  readonly coefficient: bigint;
  readonly scale: number;
}

function powerOfTen(exponent: number): bigint {
  const existing = powersOfTen.get(exponent);
  if (existing !== undefined) return existing;
  const value = 10n ** BigInt(exponent);
  powersOfTen.set(exponent, value);
  return value;
}

function canonicalDecimal(rational: DecimalRational): string {
  const negative = rational.coefficient < 0n;
  const digits = (negative ? -rational.coefficient : rational.coefficient).toString();
  let body: string;
  if (rational.scale === 0) {
    body = digits;
  } else if (digits.length > rational.scale) {
    body = `${digits.slice(0, digits.length - rational.scale)}.${digits.slice(digits.length - rational.scale)}`;
  } else {
    body = `0.${"0".repeat(rational.scale - digits.length)}${digits}`;
  }
  return negative && rational.coefficient !== 0n ? `-${body}` : body;
}

function decimalRational(raw: string): DecimalRational {
  const match = DECIMAL_NUMBER.exec(raw);
  if (match === null) throw new Error("JSON number grammar 无效。");
  const fraction = match[3] ?? "";
  const exponentText = match[4] ?? "0";
  const exponent = Number(exponentText);
  if (!Number.isInteger(exponent) || exponent < -MAXIMUM_NUMBER_EXPONENT || exponent > MAXIMUM_NUMBER_EXPONENT) {
    throw new Error(`JSON number exponent 必须在 -${MAXIMUM_NUMBER_EXPONENT}..${MAXIMUM_NUMBER_EXPONENT}。`);
  }

  const coefficientText = `${match[2] ?? ""}${fraction}`;
  let coefficient = BigInt(coefficientText);
  if (match[1] === "-") coefficient = -coefficient;
  let scale = fraction.length - exponent;
  if (scale < 0) {
    coefficient *= powerOfTen(-scale);
    scale = 0;
  }
  while (scale > 0 && coefficient % 10n === 0n) {
    coefficient /= 10n;
    scale -= 1;
  }
  if (scale > MAXIMUM_NUMBER_PRECISION) {
    throw new Error(`JSON number precision 禁止超过 ${MAXIMUM_NUMBER_PRECISION} decimal places。`);
  }
  const rational = { coefficient, scale };
  if (utf8.encode(canonicalDecimal(rational)).byteLength > MAXIMUM_CANONICAL_NUMBER_BYTES) {
    throw new Error(`canonical JSON number 禁止超过 ${MAXIMUM_CANONICAL_NUMBER_BYTES} bytes。`);
  }
  return rational;
}

class StrictJsonParser {
  private index = 0;

  constructor(private readonly source: string) {}

  parseDocument(): JsonValue {
    const byteLength = utf8.encode(this.source).byteLength;
    if (byteLength < 1 || byteLength > MAXIMUM_DOCUMENT_BYTES) {
      throw new Error(`knowledge catalog 必须包含 1..${MAXIMUM_DOCUMENT_BYTES} UTF-8 bytes。`);
    }
    this.skipWhitespace();
    const value = this.parseValue(0);
    this.skipWhitespace();
    if (this.index !== this.source.length) throw new Error("JSON document 包含 trailing token。");
    return value;
  }

  private parseValue(depth: number): JsonValue {
    if (depth > MAXIMUM_JSON_DEPTH) {
      throw new Error(`JSON document nesting 禁止超过 ${MAXIMUM_JSON_DEPTH} levels。`);
    }
    const current = this.source[this.index];
    switch (current) {
      case "{":
        return this.parseObject(depth);
      case "[":
        return this.parseArray(depth);
      case "\"":
        return this.parseString();
      case "t":
        this.consumeLiteral("true");
        return true;
      case "f":
        this.consumeLiteral("false");
        return false;
      case "n":
        this.consumeLiteral("null");
        return null;
      default:
        if (current === "-" || (current !== undefined && current >= "0" && current <= "9")) {
          return this.parseNumber();
        }
        throw new Error(`JSON value 在 byte offset ${this.index} 无效。`);
    }
  }

  private parseObject(depth: number): JsonObjectValue {
    this.index += 1;
    this.skipWhitespace();
    const fields = new Map<string, JsonValue>();
    if (this.source[this.index] === "}") {
      this.index += 1;
      return { kind: "object", fields };
    }
    while (true) {
      if (this.source[this.index] !== "\"") throw new Error("JSON object key 必须是 string。");
      const key = this.parseString();
      if (fields.has(key)) throw new Error(`JSON object 包含 decoded duplicate key ${JSON.stringify(key)}。`);
      this.skipWhitespace();
      this.consumeCharacter(":", "JSON object key 后必须是冒号。");
      this.skipWhitespace();
      fields.set(key, this.parseValue(depth + 1));
      this.skipWhitespace();
      const delimiter = this.source[this.index];
      if (delimiter === "}") {
        this.index += 1;
        return { kind: "object", fields };
      }
      this.consumeCharacter(",", "JSON object member 必须由逗号分隔。");
      this.skipWhitespace();
    }
  }

  private parseArray(depth: number): JsonArrayValue {
    this.index += 1;
    this.skipWhitespace();
    const items: JsonValue[] = [];
    if (this.source[this.index] === "]") {
      this.index += 1;
      return { kind: "array", items };
    }
    while (true) {
      items.push(this.parseValue(depth + 1));
      this.skipWhitespace();
      const delimiter = this.source[this.index];
      if (delimiter === "]") {
        this.index += 1;
        return { kind: "array", items };
      }
      this.consumeCharacter(",", "JSON array item 必须由逗号分隔。");
      this.skipWhitespace();
    }
  }

  private parseString(): string {
    const start = this.index;
    this.index += 1;
    while (this.index < this.source.length) {
      const codeUnit = this.source.charCodeAt(this.index);
      if (codeUnit === 0x22) {
        this.index += 1;
        const value = JSON.parse(this.source.slice(start, this.index)) as unknown;
        if (typeof value !== "string") throw new Error("JSON string decode 失败。");
        if (value.includes("\0")) throw new Error("JSON string 禁止包含 NUL。");
        return value;
      }
      if (codeUnit === 0x5c) {
        const escape = this.source[this.index + 1];
        if (escape === "u") {
          const hex = this.source.slice(this.index + 2, this.index + 6);
          if (hex.length !== 4 || !/^[0-9a-fA-F]{4}$/.test(hex)) throw new Error("JSON Unicode escape 无效。");
          this.index += 6;
          continue;
        }
        if (escape === undefined || !"\"\\/bfnrt".includes(escape)) throw new Error("JSON string escape 无效。");
        this.index += 2;
        continue;
      }
      if (codeUnit <= 0x1f) throw new Error("JSON string 包含 unescaped control character。");
      this.index += 1;
    }
    throw new Error("JSON string 未终止。");
  }

  private parseNumber(): JsonNumberValue {
    const match = JSON_NUMBER.exec(this.source.slice(this.index));
    if (match === null) throw new Error("JSON number grammar 无效。");
    const raw = match[0];
    const trailer = this.source[this.index + raw.length];
    if (trailer !== undefined && !/[\t\n\r ,\]}]/.test(trailer)) throw new Error("JSON number trailer 无效。");
    if (utf8.encode(raw).byteLength > MAXIMUM_NUMBER_BYTES) {
      throw new Error(`JSON number 禁止超过 ${MAXIMUM_NUMBER_BYTES} bytes。`);
    }
    this.index += raw.length;
    return { kind: "number", raw, rational: decimalRational(raw) };
  }

  private consumeLiteral(literal: string): void {
    if (!this.source.startsWith(literal, this.index)) throw new Error(`JSON literal ${literal} 无效。`);
    const trailer = this.source[this.index + literal.length];
    if (trailer !== undefined && !/[\t\n\r ,\]}]/.test(trailer)) throw new Error(`JSON literal ${literal} trailer 无效。`);
    this.index += literal.length;
  }

  private consumeCharacter(expected: string, message: string): void {
    if (this.source[this.index] !== expected) throw new Error(message);
    this.index += 1;
  }

  private skipWhitespace(): void {
    while (this.index < this.source.length && /[\t\n\r ]/.test(this.source[this.index] ?? "")) this.index += 1;
  }
}

function record(value: JsonValue, keys: readonly string[], label: string): Map<string, JsonValue> {
  if (typeof value !== "object" || value === null || !("kind" in value) || value.kind !== "object") {
    throw new Error(`${label} 必须是 JSON object。`);
  }
  const actual = [...value.fields.keys()].sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) {
    throw new Error(`${label} 字段必须严格为 ${expected.join(", ")}。`);
  }
  return value.fields;
}

function field(fields: Map<string, JsonValue>, key: string): JsonValue {
  const value = fields.get(key);
  if (value === undefined) throw new Error(`required field ${key} 缺失。`);
  return value;
}

function array(value: JsonValue, minimum: number, maximum: number | null, label: string): JsonValue[] {
  if (typeof value !== "object" || value === null || !("kind" in value) || value.kind !== "array"
    || value.items.length < minimum || (maximum !== null && value.items.length > maximum)) {
    throw new Error(maximum === null
      ? `${label} 数量必须至少为 ${minimum}。`
      : `${label} 数量必须在 ${minimum}..${maximum}。`);
  }
  return value.items;
}

function text(value: JsonValue, minimum: number, maximum: number, label: string): string {
  if (typeof value !== "string") {
    throw new Error(`${label} 必须是长度 ${minimum}..${maximum} UTF-8 bytes 的 canonical text。`);
  }
  const byteLength = utf8.encode(value).byteLength;
  if (byteLength < minimum || byteLength > maximum || value.trim() !== value || value.includes("\0")) {
    throw new Error(`${label} 必须是长度 ${minimum}..${maximum} UTF-8 bytes 的 canonical text。`);
  }
  return value;
}

function configurationKey(value: JsonValue, label: string): string {
  if (typeof value !== "string" || !CONFIGURATION_KEY.test(value)) {
    throw new Error(`${label} 必须是 canonical configuration key。`);
  }
  return value;
}

function parseKnowledgePoint(value: JsonValue, index: number): RecommendationKnowledgePointV1 {
  const label = `knowledgePoints[${index}]`;
  const source = record(value, ["id", "label", "description", "prerequisiteIds"], label);
  const id = configurationKey(field(source, "id"), `${label}.id`);
  const prerequisiteIds = array(field(source, "prerequisiteIds"), 0, null, `${label}.prerequisiteIds`)
    .map((item, prerequisiteIndex) => configurationKey(item, `${label}.prerequisiteIds[${prerequisiteIndex}]`));
  for (let prerequisiteIndex = 0; prerequisiteIndex < prerequisiteIds.length; prerequisiteIndex += 1) {
    const prerequisiteId = prerequisiteIds[prerequisiteIndex];
    if (prerequisiteId === id || (prerequisiteIndex > 0 && prerequisiteId! <= prerequisiteIds[prerequisiteIndex - 1]!)) {
      throw new Error(`${label}.prerequisiteIds 必须严格排序、唯一且禁止引用自身。`);
    }
  }
  return {
    id,
    label: text(field(source, "label"), 1, 256, `${label}.label`),
    description: text(field(source, "description"), 1, 4096, `${label}.description`),
    prerequisiteIds,
  };
}

function parseKnowledgeWeight(
  value: JsonValue,
  assignmentIndex: number,
  index: number,
  knowledgePointIds: ReadonlySet<string>,
): { dto: RecommendationKnowledgeWeightV1; rational: DecimalRational } {
  const label = `problemAssignments[${assignmentIndex}].knowledge[${index}]`;
  const source = record(value, ["knowledgePointId", "weight"], label);
  const knowledgePointId = configurationKey(field(source, "knowledgePointId"), `${label}.knowledgePointId`);
  if (!knowledgePointIds.has(knowledgePointId)) throw new Error(`${label}.knowledgePointId 引用不存在的 knowledge point。`);
  const weight = field(source, "weight");
  if (typeof weight !== "string" || utf8.encode(weight).byteLength > 128 || !CANONICAL_WEIGHT.test(weight)) {
    throw new Error(`${label}.weight 必须是大于 0 且不大于 1 的 canonical decimal string。`);
  }
  return { dto: { knowledgePointId, weight }, rational: decimalRational(weight) };
}

function exactUnitWeightSum(values: readonly DecimalRational[]): boolean {
  const scale = values.reduce((maximum, value) => Math.max(maximum, value.scale), 0);
  const total = values.reduce(
    (sum, value) => sum + value.coefficient * powerOfTen(scale - value.scale),
    0n,
  );
  return total === powerOfTen(scale);
}

function parseProblemAssignment(
  value: JsonValue,
  index: number,
  knowledgePointIds: ReadonlySet<string>,
): RecommendationProblemAssignmentV1 {
  const label = `problemAssignments[${index}]`;
  const source = record(value, ["platform", "problemId", "problemFactSha256", "knowledge"], label);
  if (field(source, "platform") !== "pintia") throw new Error(`${label}.platform 必须是 pintia。`);
  const problemId = text(field(source, "problemId"), 1, 256, `${label}.problemId`);
  if (!PINTIA_ID.test(problemId)) {
    throw new Error(`${label}.problemId 必须符合 authoritative Pintia snapshot v2 identity contract。`);
  }
  const problemFactSha256 = field(source, "problemFactSha256");
  if (typeof problemFactSha256 !== "string" || !SHA256.test(problemFactSha256)) {
    throw new Error(`${label}.problemFactSha256 必须是 lowercase SHA-256。`);
  }
  const parsedKnowledge = array(field(source, "knowledge"), 1, null, `${label}.knowledge`)
    .map((item, weightIndex) => parseKnowledgeWeight(item, index, weightIndex, knowledgePointIds));
  for (let weightIndex = 1; weightIndex < parsedKnowledge.length; weightIndex += 1) {
    if (parsedKnowledge[weightIndex]!.dto.knowledgePointId <= parsedKnowledge[weightIndex - 1]!.dto.knowledgePointId) {
      throw new Error(`${label}.knowledge 必须按 knowledgePointId 严格排序且唯一。`);
    }
  }
  if (!exactUnitWeightSum(parsedKnowledge.map((value) => value.rational))) {
    throw new Error(`${label}.knowledge weights 必须以 exact decimal rational 求和为 1。`);
  }
  return {
    platform: "pintia",
    problemId,
    problemFactSha256,
    knowledge: parsedKnowledge.map((value) => value.dto),
  };
}

function compareUtf8(left: string, right: string): number {
  const leftBytes = utf8.encode(left);
  const rightBytes = utf8.encode(right);
  const sharedLength = Math.min(leftBytes.length, rightBytes.length);
  for (let index = 0; index < sharedLength; index += 1) {
    if (leftBytes[index] !== rightBytes[index]) return leftBytes[index]! < rightBytes[index]! ? -1 : 1;
  }
  return Math.sign(leftBytes.length - rightBytes.length);
}

function compareAssignment(left: RecommendationProblemAssignmentV1, right: RecommendationProblemAssignmentV1): number {
  return compareUtf8(left.platform, right.platform)
    || compareUtf8(left.problemId, right.problemId)
    || compareUtf8(left.problemFactSha256, right.problemFactSha256);
}

function validateKnowledgeGraph(
  points: readonly RecommendationKnowledgePointV1[],
  pointIndex: ReadonlyMap<string, number>,
): void {
  for (const point of points) {
    for (const prerequisiteId of point.prerequisiteIds) {
      if (!pointIndex.has(prerequisiteId)) {
        throw new Error(`knowledge point ${JSON.stringify(point.id)} 引用不存在的 prerequisite ${JSON.stringify(prerequisiteId)}。`);
      }
    }
  }

  const state = new Uint8Array(points.length);
  const visit = (index: number): void => {
    if (state[index] === 1) throw new Error("knowledge prerequisite graph 包含 cycle。");
    if (state[index] === 2) return;
    state[index] = 1;
    for (const prerequisiteId of points[index]!.prerequisiteIds) visit(pointIndex.get(prerequisiteId)!);
    state[index] = 2;
  };
  for (let index = 0; index < points.length; index += 1) visit(index);
}

export function parseRecommendationKnowledgeCatalogV1(value: string): RecommendationKnowledgeCatalogV1 {
  const source = record(
    new StrictJsonParser(value).parseDocument(),
    ["taxonomyId", "knowledgePoints", "problemAssignments"],
    "knowledge catalog v1",
  );
  const knowledgePoints = array(field(source, "knowledgePoints"), 1, 1024, "knowledgePoints").map(parseKnowledgePoint);
  const pointIndex = new Map<string, number>();
  for (let index = 0; index < knowledgePoints.length; index += 1) {
    const point = knowledgePoints[index]!;
    if (index > 0 && point.id <= knowledgePoints[index - 1]!.id) {
      throw new Error("knowledgePoints 必须按 id 严格排序且唯一。");
    }
    pointIndex.set(point.id, index);
  }
  validateKnowledgeGraph(knowledgePoints, pointIndex);

  const knowledgePointIds = new Set(pointIndex.keys());
  const problemAssignments = array(field(source, "problemAssignments"), 0, null, "problemAssignments")
    .map((item, index) => parseProblemAssignment(item, index, knowledgePointIds));
  for (let index = 1; index < problemAssignments.length; index += 1) {
    if (compareAssignment(problemAssignments[index - 1]!, problemAssignments[index]!) >= 0) {
      throw new Error("problemAssignments 必须按 platform、problemId、problemFactSha256 严格排序且唯一。");
    }
  }
  return {
    taxonomyId: configurationKey(field(source, "taxonomyId"), "taxonomyId"),
    knowledgePoints,
    problemAssignments,
  };
}

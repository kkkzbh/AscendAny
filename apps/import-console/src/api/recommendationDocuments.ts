import type {
  RecommendationKnowledgeCatalogV1,
  RecommendationKnowledgePointV1,
  RecommendationKnowledgeWeightV1,
  RecommendationProblemAssignmentV1,
  RecommendationTrainingConfigurationV2,
  RecommendationTrainingPathPolicyV2,
  RecommendationTrainingRankingWeightsV2,
  RecommendationTrainingValidationV2,
} from "@ascendany/sdk";

const CONFIGURATION_KEY = /^[a-z][a-z0-9_.-]{0,127}$/;
const SHA256 = /^[0-9a-f]{64}$/;
const POSITIVE_INT64 = /^(?:[1-9][0-9]{0,18})$/;
const MAX_INT64 = "9223372036854775807";
const utf8 = new TextEncoder();

function record(value: unknown, keys: readonly string[], label: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${label} 必须是 JSON object。`);
  }
  const candidate = value as Record<string, unknown>;
  const actual = Object.keys(candidate).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) {
    throw new Error(`${label} 字段必须严格为 ${expected.join(", ")}。`);
  }
  return candidate;
}

function array(value: unknown, minimum: number, maximum: number | null, label: string): unknown[] {
  if (!Array.isArray(value) || value.length < minimum || (maximum !== null && value.length > maximum)) {
    throw new Error(maximum === null
      ? `${label} 数量必须至少为 ${minimum}。`
      : `${label} 数量必须在 ${minimum}..${maximum}。`);
  }
  return value;
}

function text(value: unknown, minimum: number, maximum: number, label: string): string {
  if (typeof value !== "string") {
    throw new Error(`${label} 必须是长度 ${minimum}..${maximum} UTF-8 bytes 的 canonical text。`);
  }
  const byteLength = utf8.encode(value).byteLength;
  if (byteLength < minimum || byteLength > maximum || value.trim() !== value || value.includes("\0")) {
    throw new Error(`${label} 必须是长度 ${minimum}..${maximum} UTF-8 bytes 的 canonical text。`);
  }
  return value;
}

function configurationKey(value: unknown, label: string): string {
  if (typeof value !== "string" || !CONFIGURATION_KEY.test(value)) {
    throw new Error(`${label} 必须是 canonical configuration key。`);
  }
  return value;
}

function finiteNumber(
  value: unknown,
  minimum: number,
  maximum: number,
  minimumInclusive: boolean,
  maximumInclusive: boolean,
  label: string,
): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw new Error(`${label} 必须是 finite number。`);
  }
  if ((minimumInclusive ? value < minimum : value <= minimum)
    || (maximumInclusive ? value > maximum : value >= maximum)) {
    throw new Error(`${label} 超出 contract 范围。`);
  }
  return value;
}

function integer(value: unknown, minimum: number, maximum: number, label: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < minimum || value > maximum) {
    throw new Error(`${label} 必须是 ${minimum}..${maximum} 的 safe integer。`);
  }
  return value;
}

function positiveInt64(value: unknown, label: string): string {
  if (typeof value !== "string" || !POSITIVE_INT64.test(value)
    || (value.length === MAX_INT64.length && value > MAX_INT64)) {
    throw new Error(`${label} 必须是 canonical positive int64 string。`);
  }
  return value;
}

function parseKnowledgePoint(value: unknown, index: number): RecommendationKnowledgePointV1 {
  const label = `knowledgePoints[${index}]`;
  const source = record(value, ["id", "label", "description", "prerequisiteIds"], label);
  const prerequisiteIds = array(source.prerequisiteIds, 0, null, `${label}.prerequisiteIds`)
    .map((item, prerequisiteIndex) => configurationKey(item, `${label}.prerequisiteIds[${prerequisiteIndex}]`));
  if (new Set(prerequisiteIds).size !== prerequisiteIds.length) {
    throw new Error(`${label}.prerequisiteIds 必须唯一。`);
  }
  return {
    id: configurationKey(source.id, `${label}.id`),
    label: text(source.label, 1, 256, `${label}.label`),
    description: text(source.description, 1, 4096, `${label}.description`),
    prerequisiteIds,
  };
}

function parseKnowledgeWeight(value: unknown, assignmentIndex: number, index: number): RecommendationKnowledgeWeightV1 {
  const label = `problemAssignments[${assignmentIndex}].knowledge[${index}]`;
  const source = record(value, ["knowledgePointId", "weight"], label);
  return {
    knowledgePointId: configurationKey(source.knowledgePointId, `${label}.knowledgePointId`),
    weight: finiteNumber(source.weight, 0, 1, false, true, `${label}.weight`),
  };
}

function parseProblemAssignment(value: unknown, index: number): RecommendationProblemAssignmentV1 {
  const label = `problemAssignments[${index}]`;
  const source = record(value, ["platform", "problemId", "problemFactSha256", "knowledge"], label);
  if (source.platform !== "pintia") throw new Error(`${label}.platform 必须是 pintia。`);
  const problemId = text(source.problemId, 1, 256, `${label}.problemId`);
  if (problemId.includes(":")) throw new Error(`${label}.problemId 禁止包含冒号。`);
  if (typeof source.problemFactSha256 !== "string" || !SHA256.test(source.problemFactSha256)) {
    throw new Error(`${label}.problemFactSha256 必须是 lowercase SHA-256。`);
  }
  return {
    platform: "pintia",
    problemId,
    problemFactSha256: source.problemFactSha256,
    knowledge: array(source.knowledge, 1, null, `${label}.knowledge`)
      .map((item, weightIndex) => parseKnowledgeWeight(item, index, weightIndex)),
  };
}

export function parseRecommendationKnowledgeCatalogV1(value: string): RecommendationKnowledgeCatalogV1 {
  const source = record(JSON.parse(value) as unknown, ["taxonomyId", "knowledgePoints", "problemAssignments"], "knowledge catalog v1");
  return {
    taxonomyId: configurationKey(source.taxonomyId, "taxonomyId"),
    knowledgePoints: array(source.knowledgePoints, 1, 1024, "knowledgePoints").map(parseKnowledgePoint),
    problemAssignments: array(source.problemAssignments, 0, null, "problemAssignments").map(parseProblemAssignment),
  };
}

function parseValidation(value: unknown): RecommendationTrainingValidationV2 {
  const source = record(value, ["minActors", "minInteractions", "minRelativeLogLossImprovement"], "validation");
  return {
    minActors: integer(source.minActors, 1, 20000, "validation.minActors"),
    minInteractions: integer(source.minInteractions, 1, 200000, "validation.minInteractions"),
    minRelativeLogLossImprovement: finiteNumber(source.minRelativeLogLossImprovement, 0, 1, true, false, "validation.minRelativeLogLossImprovement"),
  };
}

function parsePathPolicy(value: unknown): RecommendationTrainingPathPolicyV2 {
  const source = record(value, ["targetMastery", "maxKnowledgeTargets", "minSteps", "maxSteps", "problemsPerStep", "targetSuccessProbability"], "pathPolicy");
  const result: RecommendationTrainingPathPolicyV2 = {
    targetMastery: finiteNumber(source.targetMastery, 0, 1, false, false, "pathPolicy.targetMastery"),
    maxKnowledgeTargets: integer(source.maxKnowledgeTargets, 1, 1024, "pathPolicy.maxKnowledgeTargets"),
    minSteps: integer(source.minSteps, 2, 8, "pathPolicy.minSteps"),
    maxSteps: integer(source.maxSteps, 2, 8, "pathPolicy.maxSteps"),
    problemsPerStep: integer(source.problemsPerStep, 1, 20, "pathPolicy.problemsPerStep"),
    targetSuccessProbability: finiteNumber(source.targetSuccessProbability, 0, 1, false, false, "pathPolicy.targetSuccessProbability"),
  };
  if (result.minSteps > result.maxSteps) throw new Error("pathPolicy.minSteps 禁止大于 maxSteps。");
  return result;
}

function parseRankingWeights(value: unknown): RecommendationTrainingRankingWeightsV2 {
  const source = record(value, ["knowledgeGap", "successDistance"], "rankingWeights");
  return {
    knowledgeGap: finiteNumber(source.knowledgeGap, 0, 100, false, true, "rankingWeights.knowledgeGap"),
    successDistance: finiteNumber(source.successDistance, 0, 100, false, true, "rankingWeights.successDistance"),
  };
}

export function parseRecommendationTrainingConfigurationV2(value: string): RecommendationTrainingConfigurationV2 {
  const source = record(JSON.parse(value) as unknown, [
    "algorithm", "knowledgeCatalogVersionId", "accelerator", "seed", "epochs", "patience", "batchSize",
    "learningRate", "weightDecay", "minTrainInteractions", "minActorInteractions", "minProblemInteractions",
    "validation", "pathPolicy", "rankingWeights",
  ], "recommendation training configuration v2");
  if (source.algorithm !== "knowledge_mirt_v1") throw new Error("algorithm 必须是 knowledge_mirt_v1。");
  if (source.accelerator !== "cuda") throw new Error("accelerator 必须是 cuda。");
  const result: RecommendationTrainingConfigurationV2 = {
    algorithm: "knowledge_mirt_v1",
    knowledgeCatalogVersionId: positiveInt64(source.knowledgeCatalogVersionId, "knowledgeCatalogVersionId"),
    accelerator: "cuda",
    seed: integer(source.seed, 0, 2147483647, "seed"),
    epochs: integer(source.epochs, 1, 10000, "epochs"),
    patience: integer(source.patience, 1, 10000, "patience"),
    batchSize: integer(source.batchSize, 1, 200000, "batchSize"),
    learningRate: finiteNumber(source.learningRate, 0, 1, false, true, "learningRate"),
    weightDecay: finiteNumber(source.weightDecay, 0, 1, true, false, "weightDecay"),
    minTrainInteractions: integer(source.minTrainInteractions, 2, 200000, "minTrainInteractions"),
    minActorInteractions: integer(source.minActorInteractions, 2, 200000, "minActorInteractions"),
    minProblemInteractions: integer(source.minProblemInteractions, 1, 200000, "minProblemInteractions"),
    validation: parseValidation(source.validation),
    pathPolicy: parsePathPolicy(source.pathPolicy),
    rankingWeights: parseRankingWeights(source.rankingWeights),
  };
  if (result.patience > result.epochs) throw new Error("patience 禁止大于 epochs。");
  if (result.batchSize > result.minTrainInteractions) throw new Error("batchSize 禁止大于 minTrainInteractions。");
  return result;
}

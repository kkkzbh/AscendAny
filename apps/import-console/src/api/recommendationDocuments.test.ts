import { describe, expect, it } from "vitest";
import type { RecommendationKnowledgeCatalogV1, RecommendationProblemAssignmentV1 } from "@ascendany/sdk";
import { parseRecommendationKnowledgeCatalogV1 } from "./recommendationDocuments";

function catalog(label = "Fundamentals"): RecommendationKnowledgeCatalogV1 {
  return {
    taxonomyId: "recommendation.catalog.default",
    knowledgePoints: [{
      id: "fundamentals",
      label,
      description: "Reviewed fundamentals",
      prerequisiteIds: [],
    }],
    problemAssignments: [{
      platform: "pintia",
      problemId: "7",
      problemFactSha256: "a".repeat(64),
      knowledge: [{ knowledgePointId: "fundamentals", weight: "1" }],
    }],
  };
}

function weightedCatalog(): RecommendationKnowledgeCatalogV1 {
  return {
    taxonomyId: "recommendation.catalog.weighted",
    knowledgePoints: [
      { id: "arrays", label: "Arrays", description: "Array fundamentals", prerequisiteIds: [] },
      { id: "fundamentals", label: "Fundamentals", description: "Programming fundamentals", prerequisiteIds: [] },
      { id: "graphs", label: "Graphs", description: "Graph algorithms", prerequisiteIds: ["arrays"] },
    ],
    problemAssignments: [{
      platform: "pintia",
      problemId: "problem-7",
      problemFactSha256: "a".repeat(64),
      knowledge: [
        { knowledgePointId: "arrays", weight: "0.1" },
        { knowledgePointId: "fundamentals", weight: "0.2" },
        { knowledgePointId: "graphs", weight: "0.7" },
      ],
    }],
  };
}

function assignment(problemId: string, problemFactSha256: string): RecommendationProblemAssignmentV1 {
  return {
    platform: "pintia",
    problemId,
    problemFactSha256,
    knowledge: [{ knowledgePointId: "fundamentals", weight: "1" }],
  };
}

describe("recommendation knowledge catalog JSON boundary", () => {
  it("rejects decoded duplicate object keys before last-write-wins", () => {
    const encoded = JSON.stringify(catalog());
    const duplicate = `{"taxonom\\u0079Id":"shadow",${encoded.slice(1)}`;
    expect(() => parseRecommendationKnowledgeCatalogV1(duplicate))
      .toThrow(/decoded duplicate key "taxonomyId"/);
  });

  it("accepts only one closed JSON value", () => {
    expect(() => parseRecommendationKnowledgeCatalogV1(`${JSON.stringify(catalog())} null`))
      .toThrow(/trailing token/);
    expect(() => parseRecommendationKnowledgeCatalogV1(`{/*comment*/${JSON.stringify(catalog()).slice(1)}`))
      .toThrow(/object key/);
  });

  it("enforces the 256 KiB document and 64-level nesting bounds", () => {
    const oversized = catalog();
    oversized.knowledgePoints[0]!.description = "x".repeat((256 << 10) + 1);
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(oversized)))
      .toThrow(/1\.\.262144 UTF-8 bytes/);

    const nested = `${"[".repeat(65)}null${"]".repeat(65)}`;
    const tooDeep = `{"taxonomyId":"depth","knowledgePoints":${nested},"problemAssignments":[]}`;
    expect(() => parseRecommendationKnowledgeCatalogV1(tooDeep)).toThrow(/64 levels/);
  });

  it("measures canonical text limits in UTF-8 bytes", () => {
    const atLimit = catalog("中".repeat(85));
    expect(parseRecommendationKnowledgeCatalogV1(JSON.stringify(atLimit))).toEqual(atLimit);

    const overLimit = catalog("中".repeat(86));
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(overLimit)))
      .toThrow(/1\.\.256 UTF-8 bytes/);
  });

  it("rejects unknown fields before publish", () => {
    const value = { ...catalog(), unexpected: true };
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(value)))
      .toThrow(/字段必须严格为/);
  });
});

describe("recommendation knowledge catalog graph semantics", () => {
  it("requires knowledge points to be strictly sorted and unique", () => {
    const value = weightedCatalog();
    value.knowledgePoints = [value.knowledgePoints[1]!, value.knowledgePoints[0]!, value.knowledgePoints[2]!];
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(value)))
      .toThrow(/knowledgePoints 必须按 id 严格排序且唯一/);
  });

  it("requires prerequisites to be strictly sorted, unique, and non-self", () => {
    const unordered = weightedCatalog();
    unordered.knowledgePoints[2]!.prerequisiteIds = ["fundamentals", "arrays"];
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(unordered)))
      .toThrow(/prerequisiteIds 必须严格排序/);

    const duplicate = weightedCatalog();
    duplicate.knowledgePoints[2]!.prerequisiteIds = ["arrays", "arrays"];
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(duplicate)))
      .toThrow(/prerequisiteIds 必须严格排序/);

    const self = weightedCatalog();
    self.knowledgePoints[0]!.prerequisiteIds = ["arrays"];
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(self)))
      .toThrow(/禁止引用自身/);
  });

  it("rejects missing prerequisite references", () => {
    const value = weightedCatalog();
    value.knowledgePoints[2]!.prerequisiteIds = ["missing"];
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(value)))
      .toThrow(/引用不存在的 prerequisite "missing"/);
  });

  it("rejects prerequisite cycles", () => {
    const value = weightedCatalog();
    value.knowledgePoints[0]!.prerequisiteIds = ["graphs"];
    value.knowledgePoints[2]!.prerequisiteIds = ["arrays"];
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(value)))
      .toThrow(/graph 包含 cycle/);
  });
});

describe("recommendation problem assignment semantics", () => {
  it("accepts the complete authoritative Pintia identity domain", () => {
    const value = catalog();
    value.problemAssignments[0]!.problemId = "problem:7";
    expect(parseRecommendationKnowledgeCatalogV1(JSON.stringify(value))).toEqual(value);
  });

  it.each([":problem", "problem/7", "problem 7", "题目7"])(
    "rejects non-canonical Pintia identity %s before publish",
    (problemId) => {
      const value = catalog();
      value.problemAssignments[0]!.problemId = problemId;
      expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(value)))
        .toThrow(/authoritative Pintia snapshot v2 identity contract/);
    },
  );

  it("requires assignments to be strictly sorted by the full identity tuple", () => {
    const problemOrder = catalog();
    problemOrder.problemAssignments = [assignment("b", "a".repeat(64)), assignment("a", "b".repeat(64))];
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(problemOrder)))
      .toThrow(/problemAssignments 必须按 platform、problemId、problemFactSha256 严格排序/);

    const factOrder = catalog();
    factOrder.problemAssignments = [assignment("same", "b".repeat(64)), assignment("same", "a".repeat(64))];
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(factOrder)))
      .toThrow(/problemAssignments 必须按 platform、problemId、problemFactSha256 严格排序/);
  });

  it("uses Go-compatible ASCII byte ordering for assignment identities", () => {
    const value = catalog();
    value.problemAssignments = [assignment("A", "a".repeat(64)), assignment("a", "a".repeat(64))];
    expect(parseRecommendationKnowledgeCatalogV1(JSON.stringify(value)).problemAssignments)
      .toEqual(value.problemAssignments);
  });

  it("rejects duplicate assignments", () => {
    const value = catalog();
    value.problemAssignments.push({ ...value.problemAssignments[0]!, knowledge: [{ knowledgePointId: "fundamentals", weight: "1" }] });
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(value)))
      .toThrow(/problemAssignments 必须按 platform、problemId、problemFactSha256 严格排序且唯一/);
  });

  it("requires sorted unique knowledge references that exist", () => {
    const unordered = weightedCatalog();
    unordered.problemAssignments[0]!.knowledge.reverse();
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(unordered)))
      .toThrow(/knowledge 必须按 knowledgePointId 严格排序且唯一/);

    const duplicate = weightedCatalog();
    duplicate.problemAssignments[0]!.knowledge = [
      { knowledgePointId: "arrays", weight: "0.5" },
      { knowledgePointId: "arrays", weight: "0.5" },
    ];
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(duplicate)))
      .toThrow(/knowledge 必须按 knowledgePointId 严格排序且唯一/);

    const missing = catalog();
    missing.problemAssignments[0]!.knowledge[0]!.knowledgePointId = "missing";
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(missing)))
      .toThrow(/引用不存在的 knowledge point/);
  });

  it("preserves canonical decimal strings through exact-rational validation", () => {
    const value = weightedCatalog();
    value.problemAssignments[0]!.knowledge = [
      { knowledgePointId: "arrays", weight: "0.3333333333333333333333333333333333" },
      { knowledgePointId: "fundamentals", weight: "0.3333333333333333333333333333333333" },
      { knowledgePointId: "graphs", weight: "0.3333333333333333333333333333333334" },
    ];
    expect(parseRecommendationKnowledgeCatalogV1(JSON.stringify(value))).toEqual(value);
  });

  it("rejects noncanonical, out-of-range, and numeric weights", () => {
    const zero = catalog();
    zero.problemAssignments[0]!.knowledge[0]!.weight = "0";
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(zero)))
      .toThrow(/canonical decimal string/);

    const over = JSON.stringify(catalog()).replace('"weight":"1"', '"weight":"1.0001"');
    expect(() => parseRecommendationKnowledgeCatalogV1(over))
      .toThrow(/canonical decimal string/);

    const exponent = JSON.stringify(catalog()).replace('"weight":"1"', '"weight":"1e-1"');
    expect(() => parseRecommendationKnowledgeCatalogV1(exponent))
      .toThrow(/canonical decimal string/);

    const numeric = JSON.stringify(catalog()).replace('"weight":"1"', '"weight":1');
    expect(() => parseRecommendationKnowledgeCatalogV1(numeric))
      .toThrow(/canonical decimal string/);
  });
});

import { describe, expect, it } from "vitest";
import {
  createClient,
  uploadOjProblemVersion,
  uploadOjSubmission,
  type OjProblemVersionMetadata,
  type OjRunSubmissionMetadata,
  type OjSubmitSubmissionMetadata,
} from "../src";

const PROBLEM_ID = "123e4567-e89b-42d3-a456-426614174040";
const SUBMISSION_ID = "123e4567-e89b-42d3-a456-426614174041";
const REQUEST_ID = "123e4567-e89b-42d3-a456-426614174042";
const JSON_MEDIA_TYPE = "application/json";
const BUNDLE_MEDIA_TYPE = "application/vnd.ascendany.oj-test-bundle.v1+tar";
const SOURCE_MEDIA_TYPE = "text/x-c++src; charset=utf-8";
const STDIN_MEDIA_TYPE = "text/plain; charset=utf-8";

const problemMetadata: OjProblemVersionMetadata = {
  slug: "two_sum",
  expectedHeadRevision: 0,
  lifecycle: "active",
  title: "Two Sum",
  statementMarkdown: "Find two values.",
  solutionMarkdown: "Use one pass.",
  knowledgeTags: ["array", "sum"],
  timeLimitMs: 1000,
  memoryLimitBytes: 268435456,
  outputLimitBytes: 1048576,
  problemSpec: { comparison: "tokens" },
};

const runMetadata: OjRunSubmissionMetadata = {
  clientRequestId: REQUEST_ID,
  problemId: PROBLEM_ID,
  expectedProblemHeadRevision: 1,
  mode: "run",
  languageId: "cpp20",
};

const submitMetadata: OjSubmitSubmissionMetadata = {
  ...runMetadata,
  mode: "submit",
};

describe("closed OJ multipart uploads", () => {
  it("encodes typed problem metadata without a filename and the test bundle second", async () => {
    const requests: Request[] = [];
    const client = testClient(requests, {
      problem: { id: PROBLEM_ID },
      idempotent: false,
    });
    const bundle = new Uint8Array([98, 117, 110, 100, 108, 101, 0, 45, 45]);

    const result = await uploadOjProblemVersion({
      client,
      metadata: problemMetadata,
      testBundle: { data: new Blob([bundle]), filename: "tests.tar" },
    });

    expect(result.error).toBeUndefined();
    expect(requests).toHaveLength(1);
    const request = requests[0]!;
    expect(request?.url).toBe("https://ascendany.invalid/api/v2/admin/oj/problems/versions");
    expect(request?.headers.get("Authorization")).toBe("Bearer oj-token");
    const boundary = requireBoundary(request);
    const expected = buildMultipart(boundary, [
      { name: "metadata", mediaType: JSON_MEDIA_TYPE, bytes: jsonBytes(problemMetadata) },
      { name: "testBundle", filename: "tests.tar", mediaType: BUNDLE_MEDIA_TYPE, bytes: bundle },
    ]);
    expect(new Uint8Array(await request.arrayBuffer())).toEqual(expected);
  });

  it("encodes run and submit bodies with their exact closed part sets", async () => {
    const source = new TextEncoder().encode("int main() { return 0; }\n");
    const stdin = new TextEncoder().encode("1 2\n");
    const requests: Request[] = [];
    const client = testClient(requests, {
      submission: { id: SUBMISSION_ID },
      created: true,
    });

    await uploadOjSubmission({
      client,
      metadata: runMetadata,
      source: { data: new Blob([source]), filename: "main.cpp" },
      stdin: { data: new Blob([stdin]), filename: "stdin.txt" },
    });
    await uploadOjSubmission({
      client,
      metadata: submitMetadata,
      source: { data: new Blob([source]), filename: "main.cpp" },
    });

    expect(requests).toHaveLength(2);
    const runRequest = requests[0]!;
    const runBoundary = requireBoundary(runRequest);
    expect(new Uint8Array(await runRequest.arrayBuffer())).toEqual(buildMultipart(runBoundary, [
      { name: "metadata", mediaType: JSON_MEDIA_TYPE, bytes: jsonBytes(runMetadata) },
      { name: "source", filename: "main.cpp", mediaType: SOURCE_MEDIA_TYPE, bytes: source },
      { name: "stdin", filename: "stdin.txt", mediaType: STDIN_MEDIA_TYPE, bytes: stdin },
    ]));
    const submitRequest = requests[1]!;
    const submitBoundary = requireBoundary(submitRequest);
    expect(new Uint8Array(await submitRequest.arrayBuffer())).toEqual(buildMultipart(submitBoundary, [
      { name: "metadata", mediaType: JSON_MEDIA_TYPE, bytes: jsonBytes(submitMetadata) },
      { name: "source", filename: "main.cpp", mediaType: SOURCE_MEDIA_TYPE, bytes: source },
    ]));
  });

  it("rejects unsafe filenames before issuing a request", async () => {
    const requests: Request[] = [];
    const client = testClient(requests, {});

    await expect(uploadOjSubmission({
      client,
      metadata: submitMetadata,
      source: { data: new Blob(["code"]), filename: "../main.cpp" },
    })).rejects.toThrow("safe ASCII filename");
    expect(requests).toHaveLength(0);
  });
});

interface ExpectedPart {
  name: string;
  mediaType: string;
  filename?: string;
  bytes: Uint8Array;
}

function testClient(requests: Request[], responseBody: unknown) {
  const fetchMock: typeof fetch = async (input) => {
    const request = input instanceof Request ? input : new Request(input);
    requests.push(request);
    return new Response(JSON.stringify(responseBody), {
      status: request.url.endsWith("/versions") ? 201 : 202,
      headers: { "Content-Type": "application/json" },
    });
  };
  return createClient({
    baseUrl: "https://ascendany.invalid",
    auth: "oj-token",
    fetch: fetchMock,
  });
}

function requireBoundary(request: Request | undefined): string {
  const contentType = request?.headers.get("Content-Type") ?? "";
  const match = /^multipart\/form-data; boundary=(ascendany-v2-[0-9a-f]{48})$/.exec(contentType);
  expect(match).not.toBeNull();
  return match?.[1] ?? "";
}

function jsonBytes(value: object): Uint8Array {
  return new TextEncoder().encode(JSON.stringify(value));
}

function buildMultipart(boundary: string, parts: readonly ExpectedPart[]): Uint8Array {
  const chunks: Uint8Array[] = [];
  for (const part of parts) {
    let disposition = `Content-Disposition: form-data; name="${part.name}"`;
    if (part.filename !== undefined) {
      disposition += `; filename="${part.filename}"`;
    }
    chunks.push(
      new TextEncoder().encode(`--${boundary}\r\n${disposition}\r\nContent-Type: ${part.mediaType}\r\n\r\n`),
      part.bytes,
      new TextEncoder().encode("\r\n"),
    );
  }
  chunks.push(new TextEncoder().encode(`--${boundary}--\r\n`));
  const length = chunks.reduce((total, chunk) => total + chunk.byteLength, 0);
  const result = new Uint8Array(length);
  let offset = 0;
  for (const chunk of chunks) {
    result.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return result;
}

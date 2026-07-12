import {
  createOjProblemVersion,
  createOjSubmission,
  type CreateOjProblemVersionErrors,
  type CreateOjProblemVersionResponses,
  type CreateOjSubmissionErrors,
  type CreateOjSubmissionResponses,
  type OjProblemVersionMetadata,
  type OjRunSubmissionMetadata,
  type OjSubmitSubmissionMetadata,
} from "./generated";
import type { Client, RequestResult } from "./generated/client";

const JSON_MEDIA_TYPE = "application/json";
const TEST_BUNDLE_MEDIA_TYPE = "application/vnd.ascendany.oj-test-bundle.v1+tar";
const CPP20_SOURCE_MEDIA_TYPE = "text/x-c++src; charset=utf-8";
const STDIN_MEDIA_TYPE = "text/plain; charset=utf-8";
const SAFE_FILENAME = /^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$/;
const BOUNDARY_PREFIX = "ascendany-v2-";
const BOUNDARY_DIGEST_HEX_LENGTH = 48;

export interface OjBinaryUpload {
  data: Blob;
  filename: string;
}

export interface OjProblemVersionUpload {
  client: Client;
  metadata: OjProblemVersionMetadata;
  testBundle: OjBinaryUpload;
}

export type OjSubmissionUpload =
  | {
      client: Client;
      metadata: OjRunSubmissionMetadata;
      source: OjBinaryUpload;
      stdin: OjBinaryUpload;
    }
  | {
      client: Client;
      metadata: OjSubmitSubmissionMetadata;
      source: OjBinaryUpload;
    };

interface MultipartPart {
  name: "metadata" | "testBundle" | "source" | "stdin";
  mediaType: string;
  filename?: string;
  bytes: Uint8Array;
}

interface EncodedMultipart {
  body: Blob;
  contentType: string;
}

export async function uploadOjProblemVersion(
  input: OjProblemVersionUpload,
): RequestResult<CreateOjProblemVersionResponses, CreateOjProblemVersionErrors, false> {
  const metadataBytes = encodeMetadata(input.metadata);
  const bundleBytes = await readBinaryUpload(input.testBundle);
  const encoded = await encodeClosedMultipart([
    { name: "metadata", mediaType: JSON_MEDIA_TYPE, bytes: metadataBytes },
    {
      name: "testBundle",
      mediaType: TEST_BUNDLE_MEDIA_TYPE,
      filename: input.testBundle.filename,
      bytes: bundleBytes,
    },
  ]);
  return createOjProblemVersion({
    client: input.client,
    body: { metadata: input.metadata, testBundle: input.testBundle.data },
    bodySerializer: () => encoded.body,
    headers: { "Content-Type": encoded.contentType },
  });
}

export async function uploadOjSubmission(
  input: OjSubmissionUpload,
): RequestResult<CreateOjSubmissionResponses, CreateOjSubmissionErrors, false> {
  const metadataBytes = encodeMetadata(input.metadata);
  const sourceBytes = await readBinaryUpload(input.source);
  const parts: MultipartPart[] = [
    { name: "metadata", mediaType: JSON_MEDIA_TYPE, bytes: metadataBytes },
    {
      name: "source",
      mediaType: CPP20_SOURCE_MEDIA_TYPE,
      filename: input.source.filename,
      bytes: sourceBytes,
    },
  ];
  if ("stdin" in input) {
    const stdinBytes = await readBinaryUpload(input.stdin);
    parts.push({
      name: "stdin",
      mediaType: STDIN_MEDIA_TYPE,
      filename: input.stdin.filename,
      bytes: stdinBytes,
    });
  }
  const encoded = await encodeClosedMultipart(parts);
  const body = "stdin" in input
    ? {
        metadata: input.metadata,
        source: input.source.data,
        stdin: input.stdin.data,
      }
    : { metadata: input.metadata, source: input.source.data };
  return createOjSubmission({
    client: input.client,
    body,
    bodySerializer: () => encoded.body,
    headers: { "Content-Type": encoded.contentType },
  });
}

function encodeMetadata(metadata: object): Uint8Array {
  const value = JSON.stringify(metadata);
  if (value === undefined) {
    throw new TypeError("OJ metadata must be a JSON object.");
  }
  return new TextEncoder().encode(value);
}

async function readBinaryUpload(upload: OjBinaryUpload): Promise<Uint8Array> {
  validateFilename(upload.filename);
  if (!(upload.data instanceof Blob)) {
    throw new TypeError("OJ binary upload data must be a Blob.");
  }
  const bytes = new Uint8Array(await upload.data.arrayBuffer());
  if (bytes.byteLength === 0) {
    throw new TypeError("OJ binary upload data must be non-empty.");
  }
  return bytes;
}

function validateFilename(filename: string): void {
  if (!SAFE_FILENAME.test(filename)) {
    throw new TypeError("OJ upload filenames must use 1-255 safe ASCII filename characters.");
  }
}

async function encodeClosedMultipart(parts: readonly MultipartPart[]): Promise<EncodedMultipart> {
  const boundary = await selectBoundary(parts);
  const chunks: BlobPart[] = [];
  for (const part of parts) {
    let disposition = `Content-Disposition: form-data; name="${part.name}"`;
    if (part.filename !== undefined) {
      disposition += `; filename="${part.filename}"`;
    }
    chunks.push(
      `--${boundary}\r\n${disposition}\r\nContent-Type: ${part.mediaType}\r\n\r\n`,
      exactArrayBuffer(part.bytes),
      "\r\n",
    );
  }
  chunks.push(`--${boundary}--\r\n`);
  const contentType = `multipart/form-data; boundary=${boundary}`;
  return { body: new Blob(chunks, { type: contentType }), contentType };
}

async function selectBoundary(parts: readonly MultipartPart[]): Promise<string> {
  const subtle = globalThis.crypto?.subtle;
  if (subtle === undefined) {
    throw new TypeError("Web Crypto is required to encode OJ multipart uploads.");
  }
  const digests = await Promise.all(parts.map(async (part) =>
    new Uint8Array(await subtle.digest("SHA-256", exactArrayBuffer(part.bytes)))));
  const seedLength = digests.reduce((total, digest) => total + digest.byteLength, 0);
  const seed = new Uint8Array(seedLength + 8);
  let offset = 0;
  for (const digest of digests) {
    seed.set(digest, offset);
    offset += digest.byteLength;
  }
  const view = new DataView(seed.buffer, seedLength, 8);
  for (let counter = 0; counter < Number.MAX_SAFE_INTEGER; counter += 1) {
    view.setBigUint64(0, BigInt(counter), false);
    const digest = new Uint8Array(await subtle.digest("SHA-256", seed));
    const boundary = BOUNDARY_PREFIX + bytesToHex(digest).slice(0, BOUNDARY_DIGEST_HEX_LENGTH);
    const boundaryBytes = new TextEncoder().encode(boundary);
    if (parts.every((part) => !containsBytes(part.bytes, boundaryBytes))) {
      return boundary;
    }
  }
  throw new TypeError("A collision-free OJ multipart boundary could not be constructed.");
}

function exactArrayBuffer(bytes: Uint8Array): ArrayBuffer {
  return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;
}

function bytesToHex(bytes: Uint8Array): string {
  let result = "";
  for (const byte of bytes) {
    result += byte.toString(16).padStart(2, "0");
  }
  return result;
}

function containsBytes(haystack: Uint8Array, needle: Uint8Array): boolean {
  if (needle.byteLength === 0 || needle.byteLength > haystack.byteLength) {
    return false;
  }
  const lastStart = haystack.byteLength - needle.byteLength;
  for (let start = 0; start <= lastStart; start += 1) {
    let equal = true;
    for (let index = 0; index < needle.byteLength; index += 1) {
      if (haystack[start + index] !== needle[index]) {
        equal = false;
        break;
      }
    }
    if (equal) {
      return true;
    }
  }
  return false;
}

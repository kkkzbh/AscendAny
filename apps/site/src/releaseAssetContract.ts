import androidReleaseContract from "../../../contracts/release-assets/android.v1.json";

type AndroidReleaseAssetContract = {
  schema: "ascendany.release-assets.android.v1";
  fileNamePattern: string;
  maximumVersionLength: number;
};

const candidate: unknown = androidReleaseContract;

if (
  typeof candidate !== "object"
  || candidate === null
  || Array.isArray(candidate)
) {
  throw new Error("Android release asset contract is invalid.");
}

const fields = candidate as Partial<AndroidReleaseAssetContract>;
if (
  Object.keys(fields).sort().join(",") !== "fileNamePattern,maximumVersionLength,schema"
  || fields.schema !== "ascendany.release-assets.android.v1"
  || typeof fields.fileNamePattern !== "string"
  || fields.fileNamePattern.length === 0
  || typeof fields.maximumVersionLength !== "number"
  || !Number.isSafeInteger(fields.maximumVersionLength)
  || fields.maximumVersionLength < 1
) {
  throw new Error("Android release asset contract is invalid.");
}

const contract: AndroidReleaseAssetContract = {
  schema: fields.schema,
  fileNamePattern: fields.fileNamePattern,
  maximumVersionLength: fields.maximumVersionLength,
};

export const ANDROID_RELEASE_ASSET_PATTERN = new RegExp(contract.fileNamePattern, "u");

const ANDROID_RELEASE_ASSET_PREFIX_LENGTH = "AscendAny-Android-".length;
const ANDROID_RELEASE_ASSET_SUFFIX_LENGTH = ".apk".length;

export function isVersionedAndroidReleaseAsset(name: unknown): name is string {
  return typeof name === "string"
    && name.length <= (
      ANDROID_RELEASE_ASSET_PREFIX_LENGTH
      + contract.maximumVersionLength
      + ANDROID_RELEASE_ASSET_SUFFIX_LENGTH
    )
    && ANDROID_RELEASE_ASSET_PATTERN.test(name);
}

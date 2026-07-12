import { MAX_DECIMAL, MIN_POSITIVE_DECIMAL } from "./limits";

export function nullableDecimal(value: unknown, field: string): number | null {
  if (value === undefined || value === null) {
    return null;
  }
  if (
    typeof value !== "number" ||
    !Number.isFinite(value) ||
    value < 0 ||
    value > MAX_DECIMAL ||
    (value > 0 && value < MIN_POSITIVE_DECIMAL)
  ) {
    throw new Error(
      `${field} must be null, zero, or a finite number between ${MIN_POSITIVE_DECIMAL} and ${MAX_DECIMAL}.`,
    );
  }
  return value;
}

export function nullableFiniteNonnegative(value: unknown, field: string): number | null {
  if (value === undefined || value === null) {
    return null;
  }
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) {
    throw new Error(`${field} must be a finite non-negative number when present.`);
  }
  return value;
}

export function nullableSafeInteger(value: unknown, field: string): number | null {
  if (value === undefined || value === null) {
    return null;
  }
  return requiredSafeInteger(value, field, 0);
}

export function requiredSafeInteger(value: unknown, field: string, minimum: number): number {
  if (
    typeof value !== "number" ||
    !Number.isSafeInteger(value) ||
    value < minimum
  ) {
    throw new Error(`${field} must be a safe integer greater than or equal to ${minimum}.`);
  }
  return value;
}

export function roundedSafeNonnegativeInteger(
  value: number | null,
  multiplier: number,
  field: string,
): number | null {
  if (value === null) {
    return null;
  }
  const scaled = value * multiplier;
  const rounded = Math.round(scaled);
  if (!Number.isFinite(scaled) || !Number.isSafeInteger(rounded) || rounded < 0) {
    throw new Error(`${field} cannot be represented as a safe non-negative integer.`);
  }
  return rounded;
}

export function sumDecimals(values: ReadonlyArray<number | null>, field: string): number | null {
  if (values.some((value) => value === null)) {
    return null;
  }
  let total = 0;
  for (const value of values) {
    total += value as number;
    if (!Number.isFinite(total) || total > MAX_DECIMAL) {
      throw new Error(`${field} exceeds the maximum decimal ${MAX_DECIMAL}.`);
    }
  }
  return nullableDecimal(total, field);
}

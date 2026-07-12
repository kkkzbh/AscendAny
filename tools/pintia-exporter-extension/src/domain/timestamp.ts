const RFC3339_INSTANT = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})$/;

export function normalizedUTCDateTime(value: unknown, field: string): string {
  if (typeof value !== "string") {
    throw new Error(`${field} must be an RFC 3339 timestamp.`);
  }
  const match = RFC3339_INSTANT.exec(value);
  if (match === null) {
    throw new Error(`${field} must be an RFC 3339 timestamp.`);
  }
  const [, yearText, monthText, dayText, hourText, minuteText, secondText, fraction = "", zone] = match;
  if (
    yearText === undefined || monthText === undefined || dayText === undefined ||
    hourText === undefined || minuteText === undefined || secondText === undefined ||
    zone === undefined
  ) {
    throw new Error(`${field} did not produce the required RFC 3339 fields.`);
  }
  const year = Number(yearText);
  const month = Number(monthText);
  const day = Number(dayText);
  const hour = Number(hourText);
  const minute = Number(minuteText);
  const second = Number(secondText);
  const millisecond = Number(fraction.padEnd(3, "0").slice(0, 3));
  if (
    month < 1 || month > 12 ||
    day < 1 || day > 31 ||
    hour > 23 || minute > 59 || second > 59
  ) {
    throw new Error(`${field} must be a valid RFC 3339 calendar timestamp.`);
  }

  const local = new Date(0);
  local.setUTCFullYear(year, month - 1, day);
  local.setUTCHours(hour, minute, second, millisecond);
  if (
    local.getUTCFullYear() !== year ||
    local.getUTCMonth() !== month - 1 ||
    local.getUTCDate() !== day ||
    local.getUTCHours() !== hour ||
    local.getUTCMinutes() !== minute ||
    local.getUTCSeconds() !== second
  ) {
    throw new Error(`${field} must be a valid RFC 3339 calendar timestamp.`);
  }

  let offsetMinutes = 0;
  if (zone !== "Z") {
    const offsetHour = Number(zone.slice(1, 3));
    const offsetMinute = Number(zone.slice(4, 6));
    if (offsetHour > 23 || offsetMinute > 59) {
      throw new Error(`${field} must use a valid RFC 3339 offset.`);
    }
    offsetMinutes = (offsetHour * 60 + offsetMinute) * (zone[0] === "+" ? 1 : -1);
  }
  const normalized = new Date(local.getTime() - offsetMinutes * 60_000).toISOString();
  if (!/^\d{4}-/.test(normalized)) {
    throw new Error(`${field} normalizes outside the supported RFC 3339 year range.`);
  }
  return normalized;
}

const ALLOWED_URL_SCHEMES = ["http:", "https:", "data:"];

export function safeMarkdownUrl(url: string): string {
  if (!url) {
    return "";
  }
  const trimmed = url.trim();
  if (!trimmed) {
    return "";
  }
  if (trimmed.startsWith("/") || trimmed.startsWith("#") || trimmed.startsWith("?")) {
    return trimmed;
  }
  let parsed: URL;
  try {
    parsed = new URL(trimmed, "https://invalid.local/");
  } catch {
    return "";
  }
  return ALLOWED_URL_SCHEMES.includes(parsed.protocol) ? trimmed : "";
}

export function formatRelativeTime(timestamp: number, now: number = Date.now()): string {
  const diff = Math.max(0, now - timestamp);
  const seconds = Math.floor(diff / 1000);
  if (seconds < 60) {
    return "刚刚";
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${minutes} 分钟前`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours} 小时前`;
  }
  const days = Math.floor(hours / 24);
  if (days < 30) {
    return `${days} 天前`;
  }
  const date = new Date(timestamp);
  const month = date.getMonth() + 1;
  const day = date.getDate();
  const year = date.getFullYear();
  const currentYear = new Date(now).getFullYear();
  return year === currentYear ? `${month}.${day}` : `${year}.${month}.${day}`;
}

const STRIP_MD_PATTERNS: Array<[RegExp, string]> = [
  [/^#{1,6}\s+/m, ""],
  [/^>\s+/gm, ""],
  [/^[-*+]\s+/gm, ""],
  [/^\d+\.\s+/gm, ""],
  [/`{1,3}([^`]*)`{1,3}/g, "$1"],
  [/!\[[^\]]*\]\([^)]*\)/g, ""],
  [/\[([^\]]+)\]\([^)]*\)/g, "$1"],
  [/[*_~]+/g, ""],
];

export function summarizeNoteContent(content: string, max: number = 80): string {
  const text = (content || "").trim();
  if (!text) {
    return "";
  }
  const paragraphs = text.split(/\r?\n+/);
  const lines: string[] = [];
  for (const raw of paragraphs) {
    let line = raw;
    for (const [pattern, replacement] of STRIP_MD_PATTERNS) {
      line = line.replace(pattern, replacement);
    }
    line = line.trim();
    if (line) {
      lines.push(line);
    }
    if (lines.join(" ").length >= max) {
      break;
    }
  }
  const merged = lines.join(" ");
  if (merged.length <= max) {
    return merged;
  }
  return `${merged.slice(0, max).trimEnd()}…`;
}

export function plainTextFromMarkdown(content: string): string {
  let text = content;
  for (const [pattern, replacement] of STRIP_MD_PATTERNS) {
    text = text.replace(pattern, replacement);
  }
  return text.trim();
}

export function formatExportFileName(title: string, fallback: string = "笔记"): string {
  const trimmed = (title || "").trim();
  const base = trimmed || fallback;
  return base.replace(/[\\/:*?"<>|\n\r\t]+/g, "_");
}

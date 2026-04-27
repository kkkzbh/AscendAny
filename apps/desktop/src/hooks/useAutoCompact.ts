import { useCallback } from "react";
import { useChatStore } from "@/stores/chatStore";

/** Estimated tokens per message (rough heuristic: 1 token ~ 2 Chinese chars or 4 English chars) */
function estimateTokens(text: string): number {
  // Rough: count Chinese chars * 0.5 + ASCII chars * 0.25
  let tokens = 0;
  for (const ch of text) {
    tokens += ch.charCodeAt(0) > 127 ? 0.5 : 0.25;
  }
  return Math.ceil(tokens);
}

/**
 * Hook that provides a function to compact chat history when the token
 * budget is exceeded.
 *
 * Strategy:
 * - Summarize oldest messages into `summary`
 * - Keep the most recent N messages as-is
 *
 * Currently a stub -- actual summarization requires LLM API call.
 */
export function useAutoCompact(options?: {
  maxTokens?: number;
  keepRecent?: number;
}) {
  const { maxTokens = 4000, keepRecent = 6 } = options ?? {};
  const session = useChatStore((s) => {
    return (
      s.sessions.find((item) => item.id === s.activeSessionId) ??
      s.sessions[0]
    );
  });
  const setSummary = useChatStore((s) => s.setSummary);

  const checkAndCompact = useCallback(() => {
    if (!session) return false;
    const totalTokens = session.messages.reduce(
      (sum, m) => sum + estimateTokens(m.content),
      0,
    );

    if (totalTokens <= maxTokens) return false;
    if (session.messages.length <= keepRecent) return false;

    // Messages to summarize (oldest batch)
    const toSummarize = session.messages.slice(
      0,
      session.messages.length - keepRecent,
    );

    // TODO: Call LLM to generate actual summary
    // For now, create a simple text summary
    const summaryText =
      session.summary +
      "\n---\n" +
      `[已压缩 ${toSummarize.length} 条历史消息]`;

    setSummary(summaryText.trim());

    return true;
  }, [session, maxTokens, keepRecent, setSummary]);

  return { checkAndCompact };
}

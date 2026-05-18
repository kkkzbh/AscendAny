import { useEffect, useLayoutEffect, useRef } from "react";
import { useChatStore } from "@/stores/chatStore";
import { MessageBubble } from "./MessageBubble";
import { AssistantWorkingCard } from "./AssistantWorkingCard";
import type { ChatMessage } from "@/types/chat";

const EMPTY_MESSAGES: ChatMessage[] = [];

export function MessageList() {
  const messages = useChatStore((s) => {
    const activeSession = s.activeSessionId
      ? s.sessions.find((session) => session.id === s.activeSessionId)
      : null;
    return activeSession?.messages ?? EMPTY_MESSAGES;
  });
  const isAiWorking = useChatStore((s) => s.isAiWorking);
  const containerRef = useRef<HTMLDivElement>(null);
  const contentRef = useRef<HTMLDivElement>(null);
  const shouldStickToBottomRef = useRef(true);

  const isNearBottom = () => {
    const container = containerRef.current;
    if (!container) return true;
    return (
      container.scrollHeight - container.scrollTop - container.clientHeight < 96
    );
  };

  const scrollToBottom = (behavior: ScrollBehavior) => {
    const container = containerRef.current;
    if (!container) return;
    if (typeof container.scrollTo !== "function") return;
    container.scrollTo({ top: container.scrollHeight, behavior });
  };

  const scrollToBottomSettled = (behavior: ScrollBehavior) => {
    scrollToBottom(behavior);

    let rafId1 = 0;
    let rafId2 = 0;
    const timeoutId = window.setTimeout(() => {
      scrollToBottom("auto");
    }, 220);

    rafId1 = window.requestAnimationFrame(() => {
      scrollToBottom("auto");
      rafId2 = window.requestAnimationFrame(() => {
        scrollToBottom("auto");
      });
    });

    return () => {
      window.cancelAnimationFrame(rafId1);
      window.cancelAnimationFrame(rafId2);
      window.clearTimeout(timeoutId);
    };
  };

  useLayoutEffect(() => {
    // Ensure initial restore/login render lands at exact bottom.
    return scrollToBottomSettled("auto");
  }, []);

  useEffect(() => {
    return scrollToBottomSettled("smooth");
  }, [messages.length, isAiWorking]);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const handleScroll = () => {
      shouldStickToBottomRef.current = isNearBottom();
    };
    container.addEventListener("scroll", handleScroll, { passive: true });
    handleScroll();

    return () => {
      container.removeEventListener("scroll", handleScroll);
    };
  }, []);

  useEffect(() => {
    const content = contentRef.current;
    if (!content || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => {
      if (!shouldStickToBottomRef.current) return;
      scrollToBottom("auto");
    });
    observer.observe(content);
    return () => observer.disconnect();
  }, []);

  const latestMessage = messages.length > 0 ? messages[messages.length - 1] : undefined;
  const latestContentLength = latestMessage?.content.length ?? 0;
  const latestReasoningLength = latestMessage?.reasoningContent?.length ?? 0;

  useEffect(() => {
    if (!shouldStickToBottomRef.current) return;
    if (!latestMessage?.streaming && !latestMessage?.reasoningStreaming) return;
    scrollToBottom("auto");
  }, [
    latestContentLength,
    latestReasoningLength,
    latestMessage?.streaming,
    latestMessage?.reasoningStreaming,
  ]);

  if (messages.length === 0 && !isAiWorking) {
    return (
      <div className="message-list-shell animate-fade-in flex h-full flex-col items-center justify-center gap-4 text-[var(--text-muted)]">
        <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-[var(--surface-soft)] ring-1 ring-[var(--border-subtle)]">
          <svg
            width="24"
            height="24"
            viewBox="0 0 24 24"
            fill="none"
            stroke="var(--accent-600)"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
          </svg>
        </div>
        <div className="space-y-1 text-center">
          <p className="text-sm font-semibold text-[var(--text-strong)]">
            开始对话
          </p>
          <p className="text-xs text-[var(--text-soft)]">
            问我考试表现、能力变化和下次训练重点
          </p>
        </div>
      </div>
    );
  }

  const contentClassName = messages.length === 0
    ? "flex min-h-full flex-col items-center justify-center"
    : "space-y-1.5";

  return (
    <div ref={containerRef} className="message-list-shell flex-1 overflow-y-auto">
      <div ref={contentRef} className={contentClassName}>
        {messages.map((msg) => (
          <MessageBubble key={msg.id} message={msg} />
        ))}
        {isAiWorking ? <AssistantWorkingCard /> : null}
      </div>
    </div>
  );
}

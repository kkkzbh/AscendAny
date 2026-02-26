import { useEffect, useLayoutEffect, useRef } from "react";
import { useChatStore } from "@/stores/chatStore";
import { MessageBubble } from "./MessageBubble";
import { AssistantWorkingCard } from "./AssistantWorkingCard";

export function MessageList() {
  const messages = useChatStore((s) => s.session.messages);
  const isAiWorking = useChatStore((s) => s.isAiWorking);
  const containerRef = useRef<HTMLDivElement>(null);

  const scrollToBottom = (behavior: ScrollBehavior) => {
    const container = containerRef.current;
    if (!container) return;
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
    if (!container || typeof ResizeObserver === "undefined") return;

    const content = container.firstElementChild;
    if (!(content instanceof HTMLElement)) return;

    const observer = new ResizeObserver(() => {
      scrollToBottom("auto");
    });
    observer.observe(content);

    return () => {
      observer.disconnect();
    };
  }, [messages.length]);

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
    : "space-y-1";

  return (
    <div ref={containerRef} className="message-list-shell flex-1 overflow-y-auto">
      <div className={contentClassName}>
        {messages.map((msg) => (
          <MessageBubble key={msg.id} message={msg} />
        ))}
        {isAiWorking ? <AssistantWorkingCard /> : null}
      </div>
    </div>
  );
}

import type { ChatBlock, ChatMessage, ChatToolActivity } from "@/types/chat";
import { DEFAULT_ROLE_ID, findRole } from "@/types/role";
import { useAuthStore } from "@/stores/authStore";
import { useAvatarStore } from "@/stores/avatarStore";
import { useCustomRoleStore } from "@/stores/customRoleStore";
import { AvatarDisplay } from "@/components/common/AvatarDisplay";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkBreaks from "remark-breaks";
import { memo, useState } from "react";

const ASSISTANT_MARKDOWN_PLUGINS = [remarkGfm, remarkBreaks];
const USER_MARKDOWN_PLUGINS = [remarkGfm];

interface MessageBubbleProps {
  message: ChatMessage;
}

function formatMessageTime(date: Date): string {
  if (Number.isNaN(date.getTime())) return "";
  const month = date.getMonth() + 1;
  const day = date.getDate();
  const hour = String(date.getHours()).padStart(2, "0");
  const minute = String(date.getMinutes()).padStart(2, "0");
  return `${month}.${day} ${hour}:${minute}`;
}

function formatReasoningStatus(message: ChatMessage): string {
  if (message.reasoningStreaming) {
    return "思考中";
  }
  const startedAt = message.reasoningStartedAt ?? message.timestamp;
  const endedAt = message.reasoningEndedAt ?? Date.now();
  const durationSeconds = Math.max(
    0,
    Math.round((endedAt - startedAt) / 1000),
  );
  return `思考结束(${durationSeconds}s)`;
}

function formatToolActivity(activity: ChatToolActivity): string {
  if (activity.status === "running") {
    return `正在${activity.label}...`;
  }
  if (activity.status === "error") {
    return `${activity.label}失败`;
  }
  return activity.label;
}

function MessageBubbleComponent({ message }: MessageBubbleProps) {
  const [reasoningExpanded, setReasoningExpanded] = useState(false);
  const isUser = message.role === "user";
  const isSystem = message.role === "system";
  const account = useAuthStore((s) => s.account);
  const avatarUrl = useAvatarStore((s) => s.avatarUrl);
  const customRoles = useCustomRoleStore((s) => s.customRoles);
  const role = findRole(
    message.role === "assistant" ? (message.roleId ?? DEFAULT_ROLE_ID) : DEFAULT_ROLE_ID,
    customRoles,
  );
  const messageDate = new Date(message.timestamp);
  const timeStr = formatMessageTime(messageDate);
  const reasoningContent = message.reasoningContent ?? "";
  const hasReasoning = Boolean(reasoningContent.trim()) || Boolean(message.reasoningStreaming);
  const reasoningStatus = hasReasoning ? formatReasoningStatus(message) : "";
  const blocks: ChatBlock[] = (() => {
    if (message.blocks && message.blocks.length > 0) return message.blocks;
    const fallback: ChatBlock[] = [];
    for (const a of message.toolActivities ?? []) {
      fallback.push({ kind: "tool", activity: a });
    }
    if (message.content) {
      fallback.push({ kind: "text", text: message.content });
    }
    return fallback;
  })();
  const lastBlock = blocks[blocks.length - 1];
  const isLastBlockText = lastBlock?.kind === "text";

  if (isSystem) {
    return (
      <div className="message-row flex justify-center py-3">
        <span className="rounded-full bg-[var(--surface-soft)] px-3.5 py-1 text-[11px] font-medium text-[var(--text-soft)] ring-1 ring-[var(--border-subtle)]">
          {message.content}
        </span>
      </div>
    );
  }

  if (!isUser) {
    return (
      <div className="message-row assistant-message-row flex w-full items-start gap-3 py-3">
        <img
          src={role.avatarUrl}
          alt={role.name}
          className="assistant-avatar mt-0.5 h-8 w-8 shrink-0 rounded-full object-cover"
        />
        <div className="assistant-message-body flex min-w-0 max-w-[78%] flex-col gap-1">
          <div className="assistant-message-meta flex items-center gap-2 text-[10px] text-[var(--text-soft)]">
            <span>{role.name}</span>
            <time
              dateTime={!Number.isNaN(messageDate.getTime()) ? messageDate.toISOString() : undefined}
            >
              {timeStr}
            </time>
          </div>
          {hasReasoning ? (
            <div className="assistant-reasoning">
              <button
                type="button"
                className="assistant-reasoning-status"
                aria-expanded={reasoningExpanded}
                onClick={() => setReasoningExpanded((expanded) => !expanded)}
              >
                {reasoningStatus}
              </button>
              <div
                className={`assistant-reasoning-body${reasoningExpanded ? " is-expanded" : ""}`}
                aria-hidden={!reasoningExpanded}
              >
                <div className="assistant-reasoning-body-inner">
                  {reasoningContent}
                  {message.reasoningStreaming ? (
                    <span className="streaming-caret" aria-hidden="true" />
                  ) : null}
                </div>
              </div>
            </div>
          ) : null}
          {blocks.map((block, idx) => {
            if (block.kind === "tool") {
              return (
                <div
                  key={`tool-${block.activity.id}`}
                  className="assistant-tool-activities"
                  aria-label="工具调用摘要"
                >
                  <span
                    className={`assistant-tool-activity assistant-tool-activity-${block.activity.status}`}
                  >
                    {formatToolActivity(block.activity)}
                  </span>
                </div>
              );
            }
            const isStreamingText =
              message.streaming && idx === blocks.length - 1 && isLastBlockText;
            return (
              <div
                key={`text-${idx}`}
                className="assistant-message-text chat-markdown chat-markdown-assistant break-words"
              >
                {isStreamingText ? (
                  <div className="streaming-message-text">
                    {block.text}
                    <span className="streaming-caret" aria-hidden="true" />
                  </div>
                ) : (
                  <ReactMarkdown remarkPlugins={ASSISTANT_MARKDOWN_PLUGINS}>
                    {block.text}
                  </ReactMarkdown>
                )}
              </div>
            );
          })}
          {message.streaming && !isLastBlockText ? (
            <div className="assistant-message-text chat-markdown chat-markdown-assistant break-words">
              <div className="streaming-message-text">
                <span className="streaming-caret" aria-hidden="true" />
              </div>
            </div>
          ) : null}
        </div>
      </div>
    );
  }

  return (
    <div className="message-row user-message-row flex w-full items-start justify-end gap-2.5 py-2">
      <div className="flex max-w-[72%] flex-col gap-1">
        <div className="message-bubble message-bubble-user rounded-[18px] text-[13px] leading-5 text-white">
          <div className="chat-markdown chat-markdown-user break-words leading-5">
            <ReactMarkdown remarkPlugins={USER_MARKDOWN_PLUGINS}>
              {message.content}
            </ReactMarkdown>
          </div>
        </div>
        <time
          dateTime={!Number.isNaN(messageDate.getTime()) ? messageDate.toISOString() : undefined}
          className="px-1 text-right text-[10px] leading-none text-[var(--text-soft)]"
        >
          {timeStr}
        </time>
      </div>

      <div className="mt-1">
        <AvatarDisplay
          size={30}
          avatarUrl={avatarUrl}
          username={account?.username ?? ""}
          className="ring-1 ring-[var(--border-subtle)]"
        />
      </div>
    </div>
  );
}

export const MessageBubble = memo(MessageBubbleComponent);

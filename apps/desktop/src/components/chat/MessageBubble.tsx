import type { ChatMessage } from "@/types/chat";
import { DEFAULT_ROLE_ID, findRole } from "@/types/role";
import { useAuthStore } from "@/stores/authStore";
import { useAvatarStore } from "@/stores/avatarStore";
import { useCustomRoleStore } from "@/stores/customRoleStore";
import { AvatarDisplay } from "@/components/common/AvatarDisplay";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { memo } from "react";

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

function MessageBubbleComponent({ message }: MessageBubbleProps) {
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
          <div className="assistant-message-text chat-markdown chat-markdown-assistant break-words leading-6">
            {message.streaming ? (
              <div className="streaming-message-text">
                {message.content}
                <span className="streaming-caret" aria-hidden="true" />
              </div>
            ) : (
              <ReactMarkdown remarkPlugins={[remarkGfm]}>
                {message.content}
              </ReactMarkdown>
            )}
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="message-row user-message-row flex w-full items-start justify-end gap-2.5 py-2">
      <div className="flex max-w-[72%] flex-col gap-1">
        <div className="message-bubble message-bubble-user rounded-[18px] text-[13px] leading-relaxed text-white">
          <div className="chat-markdown chat-markdown-user break-words leading-6">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>
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

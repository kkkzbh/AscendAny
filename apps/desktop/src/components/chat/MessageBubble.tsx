import type { ChatMessage } from "@/types/chat";
import { DEFAULT_ROLE_ID, findRole } from "@/types/role";
import { useAuthStore } from "@/stores/authStore";
import { useAvatarStore } from "@/stores/avatarStore";
import { useCustomRoleStore } from "@/stores/customRoleStore";
import { AvatarDisplay } from "@/components/common/AvatarDisplay";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

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

export function MessageBubble({ message }: MessageBubbleProps) {
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

  return (
    <div
      className={`message-row flex w-full items-start gap-2.5 py-1.5 ${isUser ? "justify-end" : "justify-start"}`}
    >
      {!isUser && (
        <img
          src={role.avatarUrl}
          alt={role.name}
          className="mt-1 h-7 w-7 shrink-0 rounded-full object-cover shadow-[0_8px_20px_rgba(3,105,161,0.24)]"
        />
      )}

      <div className="flex max-w-[72%] flex-col gap-1">
        <div
          className={`message-bubble rounded-[18px] text-[13px] leading-relaxed ${
            isUser
              ? "bg-gradient-to-br from-[var(--accent-600)] to-[var(--accent-500)] text-white shadow-[0_10px_26px_rgba(3,105,161,0.24)]"
              : "bg-[var(--surface-raised)] text-[var(--text-strong)] ring-1 ring-[var(--border-subtle)]"
          }`}
        >
          <div
            className={`chat-markdown break-words leading-6 ${
              isUser ? "chat-markdown-user" : "chat-markdown-assistant"
            }`}
          >
            <ReactMarkdown remarkPlugins={[remarkGfm]}>
              {message.content}
            </ReactMarkdown>
          </div>
        </div>
        <time
          dateTime={!Number.isNaN(messageDate.getTime()) ? messageDate.toISOString() : undefined}
          className={`px-1 text-[10px] leading-none ${
            isUser ? "text-right text-[var(--text-soft)]" : "text-left text-[var(--text-soft)]"
          }`}
        >
          {timeStr}
        </time>
      </div>

      {isUser && (
        <div className="mt-1">
          <AvatarDisplay
            size={28}
            avatarUrl={avatarUrl}
            username={account?.username ?? ""}
            className="ring-1 ring-[var(--border-subtle)]"
          />
        </div>
      )}
    </div>
  );
}

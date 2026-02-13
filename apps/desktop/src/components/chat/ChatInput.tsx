import { useState, useRef, useCallback } from "react";
import {
  getApiErrorMessage,
  postChatReply,
  type ChatMessagePayload,
  type ClientProviderConfigPayload,
} from "@/lib/api";
import { useChatStore } from "@/stores/chatStore";
import { useSettingsStore } from "@/stores/settingsStore";
import type { ProviderType } from "@/types/settings";

function normalizeIdentifier(value: string): string | undefined {
  const trimmed = value.trim();
  return trimmed ? trimmed : undefined;
}

function resolveProviderMode(
  providerType: ProviderType,
): ClientProviderConfigPayload["mode"] {
  return providerType === "anthropic" ? "anthropic" : "openai_compatible";
}

export function ChatInput() {
  const [text, setText] = useState("");
  const [isSending, setIsSending] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const addMessage = useChatStore((s) => s.addMessage);
  const clearContext = useChatStore((s) => s.clearContext);
  const setSummary = useChatStore((s) => s.setSummary);

  const studentId = useSettingsStore((s) => s.studentId);
  const ptaNickname = useSettingsStore((s) => s.ptaNickname);
  const activeProvider = useSettingsStore((s) => s.activeProvider);
  const providers = useSettingsStore((s) => s.providers);

  const handleSend = useCallback(async () => {
    const trimmed = text.trim();
    if (!trimmed || isSending) return;

    const provider = providers[activeProvider];
    if (!provider) {
      addMessage("system", "当前模型配置不可用，请在设置里重新选择提供商。");
      return;
    }
    if (!provider.enabled) {
      addMessage("system", "当前模型提供商已被服务器禁用，请在设置中切换到可用选项。");
      return;
    }

    let providerConfig: ClientProviderConfigPayload | undefined;
    if (!provider.usesServerConfig) {
      const baseUrl = provider.baseUrl.trim();
      const model = provider.model.trim();
      const apiKey = provider.apiKey.trim();

      if (!baseUrl || !model || !apiKey) {
        addMessage(
          "system",
          "请先在设置中完善当前模型的 Base URL、模型名称和 API Key。",
        );
        return;
      }

      providerConfig = {
        baseUrl,
        model,
        apiKey,
        mode: resolveProviderMode(activeProvider),
      };
    }

    setIsSending(true);

    addMessage("user", trimmed);
    setText("");

    // Reset textarea height
    if (textareaRef.current) {
      textareaRef.current.style.height = "auto";
    }

    try {
      const latestSession = useChatStore.getState().session;
      const messages: ChatMessagePayload[] = latestSession.messages
        .filter((message) => message.role !== "system")
        .map((message) => ({
          role: message.role,
          content: message.content.trim(),
        }))
        .filter((message) => message.content.length > 0);

      const response = await postChatReply({
        studentId: normalizeIdentifier(studentId),
        ptaNickname: normalizeIdentifier(ptaNickname),
        messages,
        summary: latestSession.summary,
        providerType: activeProvider,
        providerConfig,
      });

      addMessage("assistant", response.reply);
      if (response.summary !== latestSession.summary) {
        setSummary(response.summary);
      }
    } catch (error) {
      addMessage(
        "system",
        getApiErrorMessage(error, "请求失败，请检查后端服务后重试。"),
      );
    } finally {
      setIsSending(false);
    }
  }, [
    text,
    isSending,
    providers,
    activeProvider,
    addMessage,
    studentId,
    ptaNickname,
    setSummary,
  ]);

  const handleClear = useCallback(() => {
    if (isSending) return;
    clearContext();
  }, [clearContext, isSending]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const handleInput = () => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 120)}px`;
  };

  return (
    <div className="chat-input-wrap shrink-0">
      <div className="mb-3 h-px bg-gradient-to-r from-transparent via-[var(--border)] to-transparent" />

      <div className="input-shell chat-input-shell flex items-end gap-2 rounded-[18px]">
        <textarea
          ref={textareaRef}
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            handleInput();
          }}
          onKeyDown={handleKeyDown}
          placeholder="输入消息，按 Enter 发送..."
          rows={1}
          className="chat-textarea max-h-[120px] min-h-[40px] flex-1 resize-none bg-transparent text-[13px] leading-relaxed text-[var(--text-strong)] outline-none placeholder:text-[var(--text-soft)]"
        />
        <div className="flex shrink-0 items-center gap-1.5 pb-0.5">
          <button
            onClick={handleClear}
            disabled={isSending}
            className="ui-icon-button"
            title="清空上下文"
          >
            <svg
              width="15"
              height="15"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.8"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <polyline points="3 6 5 6 21 6" />
              <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
            </svg>
          </button>
          <button
            onClick={handleSend}
            disabled={!text.trim() || isSending}
            className="send-button flex h-8 w-8 items-center justify-center rounded-lg text-white disabled:opacity-30 disabled:shadow-none"
            title={isSending ? "发送中" : "发送"}
          >
            <svg
              width="15"
              height="15"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <line x1="22" y1="2" x2="11" y2="13" />
              <polygon points="22 2 15 22 11 13 2 9 22 2" />
            </svg>
          </button>
        </div>
      </div>

      <p className="chat-input-hint mt-1.5 text-[10px] text-[var(--text-soft)]">
        {isSending ? "助手正在回复..." : "Shift + Enter 换行"}
      </p>
    </div>
  );
}

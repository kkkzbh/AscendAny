import { useChatStreamSender } from "@/hooks/useChatStreamSender";
import { useChatStore } from "@/stores/chatStore";
import type { ChatChoiceOption } from "@/types/chat";

interface ChoiceBlockProps {
  messageId: string;
  blockIndex: number;
  question: string;
  options: ChatChoiceOption[];
  answerIdx?: number;
  explanation?: string;
}

export function ChoiceBlock({
  messageId,
  blockIndex,
  question,
  options,
  answerIdx,
  explanation,
}: ChoiceBlockProps) {
  const interactionLocked = useChatStore((s) => {
    const activeSession = s.getActiveSession();
    return Boolean(
      s.isAiWorking ||
        activeSession?.messages.some(
          (message) => message.streaming || message.reasoningStreaming,
        ),
    );
  });
  const { sendChoiceAnswer } = useChatStreamSender();
  const answered = typeof answerIdx === "number";

  return (
    <section className="chat-choice-block" aria-label="选择题">
      <p className="chat-choice-block__question">{question}</p>
      <div className="chat-choice-block__options" role="radiogroup">
        {options.map((option, idx) => {
          const isSelected = answered && answerIdx === idx;
          const className = [
            "chat-choice-block__option",
            answered ? "is-locked" : "",
            isSelected ? "is-selected" : "",
          ]
            .filter(Boolean)
            .join(" ");
          return (
            <button
              key={option.id}
              type="button"
              className={className}
              role="radio"
              aria-checked={isSelected}
              disabled={interactionLocked || (answered && !isSelected)}
              onClick={() => {
                if (answered || interactionLocked) return;
                void sendChoiceAnswer({
                  assistantMessageId: messageId,
                  blockIndex,
                  optionIndex: idx,
                });
              }}
            >
              <span className="chat-choice-block__option-id">{option.id}</span>
              <span className="chat-choice-block__option-label">
                {option.label}
              </span>
            </button>
          );
        })}
      </div>
      {answered && explanation ? (
        <p className="chat-choice-block__explanation">{explanation}</p>
      ) : null}
    </section>
  );
}

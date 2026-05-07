import { useChatStore } from "@/stores/chatStore";
import type { ChatProblemRef } from "@/types/chat";

interface ProblemBlockProps {
  problem: ChatProblemRef;
}

export function ProblemBlock({ problem }: ProblemBlockProps) {
  const setCurrentDraft = useChatStore((s) => s.setCurrentDraft);
  const title = problem.title ?? problem.problemId;
  const difficultyText =
    typeof problem.difficulty === "number"
      ? `难度 ${problem.difficulty.toFixed(1)}`
      : null;

  return (
    <article className="chat-problem-block">
      <header className="chat-problem-block__head">
        <span className="chat-problem-block__icon" aria-hidden>
          ▮
        </span>
        <div className="chat-problem-block__title-wrap">
          <h4 className="chat-problem-block__title">{title}</h4>
          <span className="chat-problem-block__pid">{problem.problemId}</span>
        </div>
        {difficultyText ? (
          <span className="chat-problem-block__difficulty">
            {difficultyText}
          </span>
        ) : null}
      </header>
      {problem.knowledgePoints.length > 0 ? (
        <div className="chat-problem-block__tags">
          {problem.knowledgePoints.map((tag) => (
            <span key={tag} className="chat-problem-block__tag">
              {tag}
            </span>
          ))}
        </div>
      ) : null}
      {problem.reason ? (
        <p className="chat-problem-block__reason">{problem.reason}</p>
      ) : null}
      <div className="chat-problem-block__actions">
        <button
          type="button"
          className="chat-problem-block__action"
          onClick={() =>
            setCurrentDraft(
              `我想做这道：${title}（${problem.problemId}）。请帮我讲讲解题思路。`,
            )
          }
        >
          在聊天里讨论
        </button>
      </div>
    </article>
  );
}

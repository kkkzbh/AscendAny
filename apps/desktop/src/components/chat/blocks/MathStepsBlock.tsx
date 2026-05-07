import { useState } from "react";

import type { ChatMathStep } from "@/types/chat";

interface MathStepsBlockProps {
  steps: ChatMathStep[];
}

export function MathStepsBlock({ steps }: MathStepsBlockProps) {
  const [expanded, setExpanded] = useState<Set<number>>(
    () => new Set(steps.map((_, idx) => idx)),
  );

  const toggle = (idx: number) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(idx)) next.delete(idx);
      else next.add(idx);
      return next;
    });
  };

  return (
    <section className="chat-math-steps-block">
      <ol className="chat-math-steps-block__list">
        {steps.map((step, idx) => {
          const isOpen = expanded.has(idx);
          return (
            <li
              key={`${idx}-${step.title ?? step.tex.slice(0, 12)}`}
              className={`chat-math-steps-block__item ${isOpen ? "is-open" : ""}`}
            >
              <button
                type="button"
                className="chat-math-steps-block__head"
                onClick={() => toggle(idx)}
                aria-expanded={isOpen}
              >
                <span className="chat-math-steps-block__index">第 {idx + 1} 步</span>
                <span className="chat-math-steps-block__title">
                  {step.title ?? "推导"}
                </span>
                <span className="chat-math-steps-block__chevron" aria-hidden>
                  {isOpen ? "−" : "+"}
                </span>
              </button>
              {isOpen ? (
                <div className="chat-math-steps-block__body">
                  <pre className="chat-math-steps-block__tex">{step.tex}</pre>
                  {step.note ? (
                    <p className="chat-math-steps-block__note">{step.note}</p>
                  ) : null}
                </div>
              ) : null}
            </li>
          );
        })}
      </ol>
    </section>
  );
}

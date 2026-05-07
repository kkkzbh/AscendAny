import ReactMarkdown from "react-markdown";
import remarkBreaks from "remark-breaks";
import remarkGfm from "remark-gfm";

import type { ChatCalloutTone } from "@/types/chat";

const PLUGINS = [remarkGfm, remarkBreaks];

const TONE_GLYPHS: Record<ChatCalloutTone, string> = {
  info: "ℹ",
  warn: "!",
  tip: "✦",
};

interface CalloutBlockProps {
  tone: ChatCalloutTone;
  markdown: string;
}

export function CalloutBlock({ tone, markdown }: CalloutBlockProps) {
  return (
    <aside
      className={`chat-callout-block chat-callout-block--${tone}`}
      role="note"
    >
      <span className="chat-callout-block__glyph" aria-hidden>
        {TONE_GLYPHS[tone]}
      </span>
      <div className="chat-callout-block__body chat-markdown chat-markdown-assistant">
        <ReactMarkdown remarkPlugins={PLUGINS}>{markdown}</ReactMarkdown>
      </div>
    </aside>
  );
}

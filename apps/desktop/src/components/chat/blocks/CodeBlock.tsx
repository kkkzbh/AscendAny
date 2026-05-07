import { useState } from "react";

interface CodeBlockProps {
  lang: string;
  code: string;
}

export function CodeBlock({ lang, code }: CodeBlockProps) {
  const [copied, setCopied] = useState(false);

  const handleCopy = () => {
    if (typeof navigator === "undefined" || !navigator.clipboard) {
      return;
    }
    navigator.clipboard
      .writeText(code)
      .then(() => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1400);
      })
      .catch(() => {
        // Best-effort copy; fall back silently when permission is denied.
      });
  };

  return (
    <div className="chat-code-block">
      <header className="chat-code-block__head">
        <span className="chat-code-block__lang">{lang}</span>
        <button
          type="button"
          className="chat-code-block__copy"
          onClick={handleCopy}
        >
          {copied ? "已复制" : "复制"}
        </button>
      </header>
      <pre className="chat-code-block__pre">
        <code>{code}</code>
      </pre>
    </div>
  );
}

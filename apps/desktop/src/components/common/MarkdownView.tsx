import { cjk } from "@streamdown/cjk";
import { code } from "@streamdown/code";
import { createMathPlugin } from "@streamdown/math";
import { Streamdown, type UrlTransform } from "streamdown";
import { memo, useMemo } from "react";

type MarkdownVariant = "assistant" | "user" | "callout" | "note";

interface MarkdownViewProps {
  markdown: string;
  variant: MarkdownVariant;
  streaming?: boolean;
  className?: string;
  urlTransform?: UrlTransform;
}

const math = createMathPlugin({ singleDollarTextMath: true });

const plugins = {
  cjk,
  code,
  math,
};

const translations = {
  copyCode: "复制代码",
  copied: "已复制",
  downloadFile: "下载文件",
  copyTable: "复制表格",
  copyTableAsCsv: "复制为 CSV",
  copyTableAsMarkdown: "复制为 Markdown",
  copyTableAsTsv: "复制为 TSV",
  openLink: "打开链接",
  openExternalLink: "打开外部链接",
  externalLinkWarning: "即将打开外部链接",
};

const controls = {
  code: {
    copy: true,
    download: false,
  },
  table: {
    copy: true,
    download: false,
    fullscreen: false,
  },
  mermaid: false,
};

function classNames(...items: Array<string | false | undefined>): string {
  return items.filter(Boolean).join(" ");
}

function shouldAnimateText(markdown: string, streaming: boolean): boolean {
  if (!streaming) return false;
  return markdown.length <= 6000;
}

function MarkdownViewComponent({
  markdown,
  variant,
  streaming = false,
  className,
  urlTransform,
}: MarkdownViewProps) {
  const trimmed = markdown.trim();
  const isAnimating = shouldAnimateText(markdown, streaming);
  const animated = useMemo(
    () =>
      isAnimating
        ? {
            animation: "blurIn" as const,
            duration: 180,
            easing: "cubic-bezier(0.16, 1, 0.3, 1)",
            sep: "word" as const,
          }
        : false,
    [isAnimating],
  );

  if (!trimmed && !streaming) {
    return null;
  }

  return (
    <Streamdown
      className={classNames(
        "chat-markdown",
        `chat-markdown-${variant}`,
        streaming && "chat-markdown-streaming",
        className,
      )}
      mode={streaming ? "streaming" : "static"}
      parseIncompleteMarkdown={streaming}
      isAnimating={isAnimating}
      animated={animated}
      controls={controls}
      plugins={plugins}
      translations={translations}
      urlTransform={urlTransform}
      linkSafety={{ enabled: false }}
      lineNumbers={false}
    >
      {markdown}
    </Streamdown>
  );
}

export const MarkdownView = memo(MarkdownViewComponent);

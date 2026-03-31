"use client";

import { useCallback, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";

interface MarkdownContentProps {
  content: string;
}

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(async () => {
    await navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }, [text]);

  return (
    <button
      onClick={handleCopy}
      className="absolute right-2 top-2 rounded-md bg-secondary/80 px-2 py-1 text-[11px] font-medium text-muted-foreground opacity-0 transition-opacity hover:bg-secondary hover:text-foreground group-hover/code:opacity-100"
    >
      {copied ? "Copied!" : "Copy"}
    </button>
  );
}

export function MarkdownContent({ content }: MarkdownContentProps) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      rehypePlugins={[rehypeHighlight]}
      components={{
        pre({ children, ...props }) {
          // Extract raw text from code block
          const codeEl = props.node?.children?.find(
            (c: any) => c.tagName === "code"
          );
          const raw = codeEl
            ? extractText(codeEl)
            : "";

          return (
            <div className="group/code relative my-3">
              <CopyButton text={raw} />
              <pre className="overflow-x-auto rounded-lg bg-[hsl(222_22%_6%)] p-4 text-[13px] leading-relaxed" {...props}>
                {children}
              </pre>
            </div>
          );
        },
        code({ children, className, ...props }) {
          const isInline = !className;
          if (isInline) {
            return (
              <code className="rounded bg-[hsl(222_22%_6%)] px-1.5 py-0.5 text-[13px] text-accent-foreground" {...props}>
                {children}
              </code>
            );
          }
          return <code className={className} {...props}>{children}</code>;
        },
        a({ children, ...props }) {
          return (
            <a className="text-primary underline underline-offset-2 hover:text-primary/80" target="_blank" rel="noopener noreferrer" {...props}>
              {children}
            </a>
          );
        },
        table({ children, ...props }) {
          return (
            <div className="my-3 overflow-x-auto">
              <table className="w-full border-collapse text-[13px]" {...props}>{children}</table>
            </div>
          );
        },
        th({ children, ...props }) {
          return <th className="border border-border/40 bg-secondary/50 px-3 py-1.5 text-left font-semibold" {...props}>{children}</th>;
        },
        td({ children, ...props }) {
          return <td className="border border-border/40 px-3 py-1.5" {...props}>{children}</td>;
        },
        blockquote({ children, ...props }) {
          return <blockquote className="my-2 border-l-2 border-primary/40 pl-4 text-muted-foreground" {...props}>{children}</blockquote>;
        },
        ul({ children, ...props }) {
          return <ul className="my-2 list-disc pl-6 space-y-1" {...props}>{children}</ul>;
        },
        ol({ children, ...props }) {
          return <ol className="my-2 list-decimal pl-6 space-y-1" {...props}>{children}</ol>;
        },
        hr() {
          return <hr className="my-4 border-border/30" />;
        },
      }}
    >
      {content}
    </ReactMarkdown>
  );
}

/** Recursively extract text content from a hast node */
function extractText(node: any): string {
  if (node.type === "text") return node.value || "";
  if (node.children) return node.children.map(extractText).join("");
  return "";
}

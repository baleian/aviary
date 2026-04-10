"use client";

import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";
import { CodeBlock } from "./code-block";

interface MarkdownContentProps {
  content: string;
}

/**
 * MarkdownContent — renders ReactMarkdown with theming hooks consistent
 * with the Raycast surface system. Code blocks are delegated to CodeBlock
 * which adds a copy button and syntax highlighting.
 */
export function MarkdownContent({ content }: MarkdownContentProps) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      rehypePlugins={[rehypeHighlight]}
      components={{
        pre: ({ children, node, ...props }) => (
          <CodeBlock node={node} {...props}>
            {children}
          </CodeBlock>
        ),
        code: ({ children, className, ...props }) => {
          const isInline = !className;
          if (isInline) {
            return (
              <code
                className="rounded-xs bg-canvas px-1.5 py-0.5 type-code-sm text-info"
                {...props}
              >
                {children}
              </code>
            );
          }
          return (
            <code className={className} {...props}>
              {children}
            </code>
          );
        },
        a: ({ children, ...props }) => (
          <a
            className="text-info underline underline-offset-2 hover:opacity-80"
            target="_blank"
            rel="noopener noreferrer"
            {...props}
          >
            {children}
          </a>
        ),
        table: ({ children, ...props }) => (
          <div className="my-3 overflow-x-auto">
            <table className="w-full border-collapse type-body-tight" {...props}>
              {children}
            </table>
          </div>
        ),
        th: ({ children, ...props }) => (
          <th
            className="border border-white/[0.06] bg-raised px-3 py-1.5 text-left type-caption-bold"
            {...props}
          >
            {children}
          </th>
        ),
        td: ({ children, ...props }) => (
          <td className="border border-white/[0.06] px-3 py-1.5" {...props}>
            {children}
          </td>
        ),
        blockquote: ({ children, ...props }) => (
          <blockquote
            className="my-2 border-l-2 border-info/40 pl-4 text-fg-muted"
            {...props}
          >
            {children}
          </blockquote>
        ),
        ul: ({ children, ...props }) => (
          <ul className="my-2 list-disc pl-6 space-y-1" {...props}>
            {children}
          </ul>
        ),
        ol: ({ children, ...props }) => (
          <ol className="my-2 list-decimal pl-6 space-y-1" {...props}>
            {children}
          </ol>
        ),
        hr: () => <hr className="my-4 border-white/[0.06]" />,
      }}
    >
      {content}
    </ReactMarkdown>
  );
}

"use client";

import { useCallback, useState } from "react";
import { Check, Copy } from "@/components/icons";

interface CodeBlockProps extends React.HTMLAttributes<HTMLPreElement> {
  // node is provided by react-markdown but typed as any in their types
  node?: { children?: Array<{ tagName?: string; children?: unknown }> };
}

/** Recursively extract text content from a hast node tree. */
function extractText(node: { type?: string; value?: string; children?: unknown[] }): string {
  if (node.type === "text") return node.value || "";
  if (node.children)
    return node.children
      .map((c) => extractText(c as { type?: string; value?: string; children?: unknown[] }))
      .join("");
  return "";
}

/**
 * CodeBlock — wraps `<pre>` with a hover-revealed copy button and the
 * canvas surface background. Used as the `pre` renderer in markdown.
 */
export function CodeBlock({ children, node, ...props }: CodeBlockProps) {
  const [copied, setCopied] = useState(false);

  const codeEl = node?.children?.find((c) => c?.tagName === "code");
  const raw = codeEl ? extractText(codeEl as { children?: unknown[] }) : "";

  const handleCopy = useCallback(async () => {
    await navigator.clipboard.writeText(raw);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }, [raw]);

  return (
    <div className="group/code relative my-3">
      <button
        type="button"
        onClick={handleCopy}
        className="absolute right-2 top-2 inline-flex items-center gap-1 rounded-xs bg-raised px-2 py-1 type-caption text-fg-muted opacity-0 transition-opacity hover:text-fg-primary group-hover/code:opacity-100"
      >
        {copied ? (
          <>
            <Check size={11} strokeWidth={2.5} /> Copied
          </>
        ) : (
          <>
            <Copy size={11} strokeWidth={2} /> Copy
          </>
        )}
      </button>
      <pre
        className="overflow-x-auto rounded-md bg-canvas p-4 type-code"
        {...props}
      >
        {children}
      </pre>
    </div>
  );
}

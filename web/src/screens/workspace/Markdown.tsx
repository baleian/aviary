import { memo, type ReactElement, type ReactNode } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

// CodeBlock is a block of code. The lines of a diff are coloured.
export function CodeBlock({
  text,
  language,
  bare,
}: {
  text: string;
  language?: string;
  // Whether a diff is shown without the lines that name its files.
  bare?: boolean;
}) {
  const diff = language === "diff" || /^(@@ |\+\+\+ |--- )/m.test(text);
  if (!diff) return <pre className="code">{text}</pre>;
  const lines = text.split("\n");
  // A diff names its files before its first hunk. From there on a line that
  // begins with "-" or "+" was taken out or put in, however it goes on.
  const first = lines.findIndex((line) => line.startsWith("@@"));
  return (
    <pre className="code diff">
      <code>
        {lines.map((line, index) => {
          const names =
            index < first || (first < 0 && /^(\+\+\+|---)/.test(line));
          // A hunk that says no lines is one that the console made.
          if (bare && (names || line === "@@")) return null;
          const kind =
            names || line.startsWith("@@")
              ? "hk"
              : line.startsWith("+")
                ? "ad"
                : line.startsWith("-")
                  ? "dl"
                  : "";
          return kind ? (
            <span key={index} className={kind}>
              {line}
            </span>
          ) : (
            line + "\n"
          );
        })}
      </code>
    </pre>
  );
}

// Markdown shows the Markdown that an agent wrote. The text of an agent is
// not trusted: its HTML is not rendered, its images are not loaded, and only
// a link to a web or mail address is a link.
export const Markdown = memo(function Markdown({ text }: { text: string }) {
  return (
    <div className="md">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        disallowedElements={["img"]}
        urlTransform={(url) => (/^(https?:|mailto:)/i.test(url) ? url : "")}
        components={{
          a: ({ href, children }) =>
            href ? (
              <a href={href} target="_blank" rel="noopener noreferrer">
                {children}
              </a>
            ) : (
              <>{children}</>
            ),
          pre: ({ children }) => {
            const code = children as ReactElement<{
              className?: string;
              children?: ReactNode;
            }>;
            const language = /language-(\S+)/.exec(
              code.props?.className ?? "",
            )?.[1];
            const text = String(code.props?.children ?? "").replace(/\n$/, "");
            return <CodeBlock text={text} language={language} />;
          },
          h4: ({ children }) => <h3>{children}</h3>,
          h5: ({ children }) => <h3>{children}</h3>,
          h6: ({ children }) => <h3>{children}</h3>,
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
});

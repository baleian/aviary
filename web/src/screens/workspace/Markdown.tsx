import { memo, type ReactElement, type ReactNode } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

// CodeBlock is a block of code. The lines of a diff are coloured.
export function CodeBlock({
  text,
  language,
}: {
  text: string;
  language?: string;
}) {
  const diff = language === "diff" || /^(@@ |\+\+\+ |--- )/m.test(text);
  if (!diff) return <pre className="code">{text}</pre>;
  return (
    <pre className="code">
      {text.split("\n").map((line, index) => {
        const kind = /^(\+\+\+|---|@@)/.test(line)
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

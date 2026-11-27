import { useQuery } from "@tanstack/react-query";
import {
  memo,
  type ReactElement,
  type ReactNode,
  useLayoutEffect,
  useMemo,
  useRef,
} from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { readFile } from "../../../api/workspace.ts";
import { kindOf } from "../../../lib/files.ts";
import { CodeBlock } from "../Markdown.tsx";
import { beside, nameOf } from "./state.ts";

// Shot is a picture of the workspace in a document.
function Shot({
  session,
  path,
  alt,
}: {
  session: string;
  path: string;
  alt: string;
}) {
  const { data } = useQuery({
    queryKey: ["file-shown", session, path],
    queryFn: () => readFile(session, path),
    staleTime: 60000,
    retry: false,
  });
  const picture = useRef<HTMLImageElement>(null);
  // The picture is given the address of the file before it is first shown,
  // and the address is let go of with it.
  useLayoutEffect(() => {
    if (!data || !picture.current) return;
    const type = kindOf(nameOf(path)).type;
    const made = URL.createObjectURL(data.slice(0, data.size, type));
    picture.current.src = made;
    return () => URL.revokeObjectURL(made);
  }, [data, path]);
  return data ? <img ref={picture} alt={alt} /> : <span>{alt}</span>;
}

// Doc shows a Markdown file of a workspace as the document that it makes.
// Its pictures are those of the workspace, a link to another file of the
// workspace opens that file, and nothing is loaded from elsewhere. The HTML
// in it is not rendered.
export const Doc = memo(function Doc({
  session,
  path,
  text,
  onOpen,
}: {
  session: string;
  // The path of the file, which what it names is beside.
  path: string;
  text: string;
  onOpen: (path: string) => void;
}) {
  // What draws the links and the pictures stays the same while the file
  // does: drawn anew, a link would lose the click that is on its way.
  const components = useMemo(
    (): Components => ({
      a: ({ href = "", children }) => {
        const file = beside(path, href);
        if (file) {
          return (
            <a
              href={"#" + file}
              onClick={(event) => {
                event.preventDefault();
                onOpen(file);
              }}
            >
              {children}
            </a>
          );
        }
        return /^(https?:|mailto:)/i.test(href) ? (
          <a href={href} target="_blank" rel="noopener noreferrer">
            {children}
          </a>
        ) : (
          <>{children}</>
        );
      },
      img: ({ src, alt = "" }) => {
        const file = typeof src === "string" && beside(path, src);
        return file ? (
          <Shot session={session} path={file} alt={alt} />
        ) : (
          <span>{alt}</span>
        );
      },
      pre: ({ children }) => {
        const code = children as ReactElement<{
          className?: string;
          children?: ReactNode;
        }>;
        const language = /language-(\S+)/.exec(
          code.props?.className ?? "",
        )?.[1];
        const said = String(code.props?.children ?? "").replace(/\n$/, "");
        return <CodeBlock text={said} language={language} />;
      },
    }),
    [session, path, onOpen],
  );
  return (
    <div className="md fv-doc">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {text}
      </ReactMarkdown>
    </div>
  );
});

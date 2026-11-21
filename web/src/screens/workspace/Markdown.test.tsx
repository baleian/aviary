// @vitest-environment happy-dom
import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { CodeBlock, Markdown } from "./Markdown.tsx";

afterEach(cleanup);

const html = (text: string) =>
  render(<Markdown text={text} />).container.querySelector(".md")!.innerHTML;

it("renders what agents commonly write", () => {
  const md = html(
    [
      "# Title",
      "Some **bold**, some _emphasis_ and `code`.",
      "",
      "- one",
      "- two",
      "",
      "| a | b |",
      "| - | - |",
      "| 1 | 2 |",
      "",
      "> quoted",
    ].join("\n"),
  );
  expect(md).toContain("<h1>Title</h1>");
  expect(md).toContain("<strong>bold</strong>");
  expect(md).toContain("<em>emphasis</em>");
  expect(md).toContain("<code>code</code>");
  expect(md).toContain("<li>one</li>");
  expect(md).toContain("<td>2</td>");
  expect(md).toContain("<blockquote>");
});

it("keeps small headings from growing smaller than the text", () => {
  const md = html("#### Fourth\n\n###### Sixth");
  expect(md).toContain("<h3>Fourth</h3>");
  expect(md).toContain("<h3>Sixth</h3>");
});

it("shows a block of code as it is, and colours the lines of a diff", () => {
  const plain = html("```ts\nconst a = 1;\n```");
  expect(plain).toContain('<pre class="code">const a = 1;</pre>');
  const diff = html("```diff\n@@ -1 +1 @@\n-old\n+new\n same\n```");
  expect(diff).toContain('<span class="hk">@@ -1 +1 @@</span>');
  expect(diff).toContain('<span class="dl">-old</span>');
  expect(diff).toContain('<span class="ad">+new</span>');
  // A block that is not closed yet, as while it is being written.
  expect(html("```\nstill writing")).toContain(
    '<pre class="code">still writing</pre>',
  );
});

it("shows the HTML of an agent as text, and renders none of it", () => {
  const { container } = render(
    <Markdown text={'<script>alert(1)</script> <b onclick="x()">bold</b>'} />,
  );
  expect(container.querySelector("script, b")).toBeNull();
  expect(container.textContent).toContain("<script>alert(1)</script>");
  expect(container.textContent).toContain('<b onclick="x()">bold</b>');
});

it("loads no images", () => {
  const md = html("before ![alt](https://example.com/pixel.png) after");
  expect(md).not.toContain("<img");
  expect(md).toContain("before");
});

it("makes a link only of a web or mail address, and opens it apart", () => {
  const md = html(
    "[web](https://example.com/a) [mail](mailto:a@example.com) " +
      "[script](javascript:alert(1)) [file](docs/readme.md)",
  );
  expect(md).toContain(
    '<a href="https://example.com/a" target="_blank" rel="noopener noreferrer">web</a>',
  );
  expect(md).toContain('href="mailto:a@example.com"');
  expect(md).not.toContain("javascript:");
  expect(md).not.toContain('href="docs/readme.md"');
  expect(md).toContain("script");
  expect(md).toContain("file");
});

it("takes a diff for one without being told", () => {
  const { container } = render(<CodeBlock text={"--- a\n+++ b\n-x\n+y"} />);
  expect(container.querySelectorAll("span.hk").length).toBe(2);
  expect(container.querySelector("span.ad")!.textContent).toBe("+y");
});

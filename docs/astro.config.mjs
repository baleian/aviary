import { defineConfig } from "astro/config";
import { satteri } from "@astrojs/markdown-satteri";
import starlight from "@astrojs/starlight";
import starlightLinksValidator from "starlight-links-validator";

// Pages link to each other by the paths of their files, which also lead to
// the page where the files are read as they are. A page is served at the
// path of its file under this directory, without the extension.
const pages = new URL("./src/content/docs/", import.meta.url).href;

function route(url, fileURL) {
  const [path, anchor] = url.split("#");
  if (!fileURL || !/\.mdx?$/.test(path) || /^[a-z][a-z0-9+.-]*:/i.test(path))
    return url;
  const file = new URL(path, fileURL).href;
  if (!file.startsWith(pages)) return url;
  const page = file
    .slice(pages.length)
    .replace(/(^|\/)index\.mdx?$|\.mdx?$/, "$1");
  const slash = page && !page.endsWith("/") ? "/" : "";
  return `/${page}${slash}${anchor ? `#${anchor}` : ""}`;
}

const fileLinks = ({ fileURL }) => {
  const visit = (node, ctx) =>
    ctx.setProperty(node, "url", route(node.url, fileURL));
  return { name: "file-links", link: visit, definition: visit };
};

export default defineConfig({
  markdown: { processor: satteri({ mdastPlugins: [fileLinks] }) },
  integrations: [
    starlight({
      title: "Aviary",
      description:
        "Aviary runs AI agent harnesses on Kubernetes, each session in a sandbox of its own.",
      logo: { src: "./src/assets/logo.svg" },
      favicon: "/favicon.svg",
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/baleian/aviary",
        },
      ],
      editLink: {
        baseUrl: "https://github.com/baleian/aviary/edit/main/docs/",
      },
      customCss: [
        "@fontsource-variable/plus-jakarta-sans",
        "@fontsource-variable/jetbrains-mono",
        "./src/styles/theme.css",
      ],
      // The build fails if a link between the pages leads nowhere. The
      // pages name addresses of the local cluster, which are at localhost.
      plugins: [starlightLinksValidator({ errorOnLocalLinks: false })],
      sidebar: [
        {
          label: "Start here",
          items: [
            "start/overview",
            "start/quick-start",
            "start/concepts",
            "start/how-it-works",
          ],
        },
        {
          label: "Use",
          items: [
            "use/web-ui",
            "use/projects",
            "use/model-providers",
            "use/mcp-servers",
            "use/secret-stores",
            "use/agents",
            "use/sessions",
            "use/approvals",
          ],
        },
        {
          label: "Operate",
          items: [
            "operate/installation",
            "operate/authentication",
            "operate/egress",
            "operate/storage",
            "operate/capacity",
            "operate/web-ui",
            "operate/observability",
            "operate/upgrades",
            "operate/harness-images",
            "operate/plugins",
            "operate/mirrors",
          ],
        },
        {
          label: "Contribute",
          items: [
            "contribute/overview",
            "contribute/development",
            "contribute/testing",
            "contribute/database-migrations",
            "contribute/documentation",
          ],
        },
        {
          label: "Reference",
          items: [
            {
              label: "API",
              items: [
                "reference/api/overview",
                "reference/api/projects",
                "reference/api/connections",
                "reference/api/agents",
                "reference/api/sessions",
                "reference/api/turns",
                "reference/api/events",
              ],
            },
            "reference/configuration",
            "reference/metrics",
          ],
        },
      ],
    }),
  ],
});

# Documentation

The documentation of Aviary. Its pages are the Markdown files under
[`src/content/docs`](src/content/docs), which read well as they are and are
built into a site with [Starlight](https://starlight.astro.build).

| Section | For |
| --- | --- |
| [Start here](src/content/docs/start/overview.md) | Everyone: what Aviary is, how it works, and the quick start. |
| [Use](src/content/docs/use/web-ui.md) | Those who work with agents. |
| [Operate](src/content/docs/operate/installation.md) | Those who install and run Aviary on a cluster. |
| [Contribute](src/content/docs/contribute/overview.md) | Those who change Aviary. |
| [Reference](src/content/docs/reference/api/overview.md) | The API, the values of the chart and the metrics. |

From the root of the repository, `make docs-dev` serves the site at
http://localhost:4321, and `make docs` builds it into `docs/dist`.
[Documentation](src/content/docs/contribute/documentation.md) describes how
the pages are written and checked.

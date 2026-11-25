---
title: Documentation
description: How these pages are written, checked and built.
---

The documentation is in `docs/`. Its pages are Markdown files, and
[Starlight](https://starlight.astro.build) builds them into a site.

```sh
make docs-dev   # serve the site at http://localhost:4321, from its source
make docs       # build the site into docs/dist
```

## Pages

A page is a file under `docs/src/content/docs`, and is served at the path of
the file without its extension. Its first lines give it a title and a
description, and the text follows without a heading of its own:

```md
---
title: Sessions and turns
description: How a conversation with an agent runs.
---

A session is one conversation with an agent.
```

The directories are the sections of the site:

| Directory | For |
| --- | --- |
| `start/` | Everyone: what Aviary is, how it works, and the quick start. |
| `use/` | Those who work with agents: the web UI, and how each part of a project behaves. |
| `operate/` | Those who install and run Aviary on a cluster. |
| `contribute/` | Those who change Aviary. |
| `reference/` | The API, the values of the chart and the metrics. |

The sidebar lists the pages in `docs/astro.config.mjs`. A new page is added
there.

## Links and pictures

Pages link to each other by the relative paths of their files, with the
anchor of a heading where it helps:

```md
See [Roles](../use/projects.md#roles).
```

Such a link also leads to the page where the files are read as they are, as
in the repository, and the build turns it into the address of the page. A
link to a page or a heading that is not there fails the build.

Pictures are in `docs/src/assets` and are linked by their relative paths as
well.

## Writing

- A change to what Aviary does changes its documentation in the same commit.
- Each fact has one page, and the other pages link to it. What a value, a
  request or a metric is belongs in the reference, and how to reach a goal
  with them in the other sections.
- The pages are plain Markdown, without components, so that they also read
  well as files.

## Checks

`make test` builds the site, which checks the links. `make lint` checks
that the configuration and the styles of the site are formatted, which
`npm run format` in `docs/` does. The pages are not formatted by a tool, so
that a table keeps each row on a line of its own.

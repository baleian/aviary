// The workspaces of the example data: what the sandbox of a session has
// under /home/workspace. A workspace is made when it is first read, from the
// session that it is of. A file has no bytes until it is read.

import { bytesOf } from "./files.js";
import { seeded } from "./util.js";

const MINUTE = 60000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

// The most bytes that are made for a file of the example data, however
// large it says it is.
const MOST = 24 << 20;

const encoder = new TextEncoder();
const bytes = (text) => encoder.encode(text).length;

// ---- Nodes ----------------------------------------------------------------

// A directory has its entries by name, or what makes them when they are
// first read. A file has its text, or the bytes that were written to it, or
// only a size. A link has the path that it points at.
const directory = (entries, at) => ({ kind: "directory", entries, at });
const link = (target) => ({ kind: "symlink", target });
// ago gives what a spec makes another time than that of its workspace: so
// long before now.
const ago = (ms, spec) => ({ ago: ms, spec });

// grow makes the node of a spec, as of the time at: an object is a
// directory, a function a directory that is made when it is read, a string
// the text of a file, and a number the size of one, whose bytes are made
// when it is read.
function grow(spec, at, now) {
  if (spec && spec.ago != null) return grow(spec.spec, now - spec.ago, now);
  if (spec && spec.kind === "symlink") return { ...spec, at };
  if (typeof spec === "string")
    return { kind: "file", text: spec, size: bytes(spec), at };
  if (typeof spec === "number") return { kind: "file", size: spec, at };
  if (typeof spec === "function")
    return directory(() => growAll(spec(), at, now), at);
  return directory(growAll(spec, at, now), at);
}

function growAll(spec, at, now) {
  const entries = new Map();
  for (const [name, child] of Object.entries(spec))
    entries.set(name, grow(child, at, now));
  return entries;
}

const entriesOf = (node) => {
  if (typeof node.entries === "function") node.entries = node.entries();
  return node.entries;
};

// ---- What the files say ---------------------------------------------------

const README = `# storefront

The storefront backend: catalogue, cart, checkout and webhooks.

![How a checkout flows through the services](docs/architecture.svg)

## Develop

    npm install
    npm test

## Layout

| Path | What is there |
| --- | --- |
| \`src/cart\` | Prices, coupons and the bundle rule |
| \`src/checkout\` | Totals, taxes and the payment intent |
| \`src/webhooks\` | The sender, with its retries |
| \`migrations\` | The schema of the orders database |

## Before a release

- [x] \`npm test\` passes
- [x] The migrations run on a copy of production
- [ ] The webhook retry policy is documented for integrators

See [the design of the retries](docs/webhook-retries.md).
`;

const NOTES = `# Notes

What was asked of this repository, newest last.

## Requests
`;

const PACKAGE = `{
  "name": "storefront",
  "version": "2.14.0",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "tsx watch src/index.ts",
    "build": "tsc -p tsconfig.json",
    "test": "vitest run",
    "migrate": "node scripts/migrate.js"
  },
  "dependencies": {
    "fastify": "5.6.1",
    "pg": "8.16.3",
    "zod": "4.1.12"
  },
  "devDependencies": {
    "tsx": "4.20.6",
    "typescript": "5.9.3",
    "vitest": "3.2.4"
  }
}
`;

const TSCONFIG = `{
  "compilerOptions": {
    "target": "ES2023",
    "module": "NodeNext",
    "strict": true,
    "outDir": "dist",
    "rootDir": "src"
  },
  "include": ["src"]
}
`;

const TOTAL = `import type { Cart, Coupon, Line } from "../cart/types.js";
import { taxFor } from "./tax.js";

export interface Total {
  subtotal: number;
  discount: number;
  tax: number;
  total: number;
}

// Amounts are in cents. A discount is taken off each line and rounded
// there, so that the lines of a receipt add up to its total.
export function totalOf(cart: Cart, coupons: Coupon[]): Total {
  const lines = cart.lines.map((line) => discounted(line, coupons));
  const subtotal = sum(cart.lines.map((line) => line.price * line.quantity));
  const discount = subtotal - sum(lines);
  const tax = taxFor(cart.region, sum(lines));
  return { subtotal, discount, tax, total: subtotal - discount + tax };
}

function discounted(line: Line, coupons: Coupon[]): number {
  let amount = line.price * line.quantity;
  for (const coupon of coupons) {
    if (!coupon.applies(line)) continue;
    // Rounding half up here is what made two coupons differ by a cent.
    amount -= Math.floor((amount * coupon.percent) / 100);
  }
  return Math.max(0, amount);
}

const sum = (amounts: number[]) =>
  amounts.reduce((total, amount) => total + amount, 0);
`;

const SENDER = `import { setTimeout as sleep } from "node:timers/promises";
import type { Delivery, Endpoint } from "./types.js";

const TRIES = 5;
const FIRST_WAIT_MS = 200;

export class DeliveryFailed extends Error {
  constructor(
    readonly delivery: Delivery,
    readonly tries: number,
    readonly last: Response | Error,
  ) {
    super(\`delivery \${delivery.id} failed after \${tries} tries\`);
  }
}

// send delivers the event to the endpoint. What may pass by itself is tried
// again: an error of the network, a 429, and an answer of 500 or more.
export async function send(endpoint: Endpoint, delivery: Delivery) {
  let wait = FIRST_WAIT_MS;
  let last: Response | Error = new Error("not sent");
  for (let attempt = 1; attempt <= TRIES; attempt++) {
    try {
      last = await fetch(endpoint.url, {
        method: "POST",
        headers: { "content-type": "application/json", ...signed(endpoint, delivery) },
        body: JSON.stringify(delivery.event),
      });
      if (last.ok) return;
      if (last.status < 500 && last.status !== 429) break;
      wait = retryAfter(last) ?? wait;
    } catch (error) {
      last = error as Error;
    }
    // The deliveries of an outage do not come back all at once.
    await sleep(wait * (0.5 + Math.random()));
    wait *= 2;
  }
  throw new DeliveryFailed(delivery, TRIES, last);
}

function retryAfter(answer: Response): number | undefined {
  const seconds = Number(answer.headers.get("retry-after"));
  return seconds > 0 ? seconds * 1000 : undefined;
}

function signed(endpoint: Endpoint, delivery: Delivery) {
  return { "x-storefront-signature": endpoint.sign(delivery.event) };
}
`;

const RULES = `import type { Line } from "./types.js";

export interface Bundle {
  skus: string[];
  percent: number;
}

// A bundle applies once for every full set of its SKUs in the cart.
export function bundles(lines: Line[], bundle: Bundle): number {
  const counts = bundle.skus.map(
    (sku) => lines.find((line) => line.sku === sku)?.quantity ?? 0,
  );
  return Math.min(...counts);
}

// TODO(carol): a line that is part of two bundles is discounted twice.
export function bundleDiscount(lines: Line[], bundle: Bundle): number {
  const sets = bundles(lines, bundle);
  const set = bundle.skus
    .map((sku) => lines.find((line) => line.sku === sku)!.price)
    .reduce((total, price) => total + price, 0);
  return Math.floor((sets * set * bundle.percent) / 100);
}
`;

const SEED_PY = `"""Fills a development database with a few hundred orders."""

import argparse
import random
from datetime import datetime, timedelta

import psycopg

PLANS = ["basic", "pro", "team", "enterprise"]


def orders(count: int, seed: int):
    rng = random.Random(seed)
    now = datetime.utcnow()
    for number in range(count):
        yield {
            "id": 100_000 + number,
            "plan": rng.choice(PLANS),
            "total_cents": rng.choice([900, 2900, 9900, 49900]),
            "created_at": now - timedelta(minutes=rng.randrange(60 * 24 * 90)),
        }


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--count", type=int, default=400)
    parser.add_argument("--seed", type=int, default=7)
    args = parser.parse_args()
    with psycopg.connect() as connection, connection.cursor() as cursor:
        for order in orders(args.count, args.seed):
            cursor.execute(
                "insert into orders (id, plan, total_cents, created_at) "
                "values (%(id)s, %(plan)s, %(total_cents)s, %(created_at)s) "
                "on conflict (id) do nothing",
                order,
            )
    print(f"seeded {args.count} orders")


if __name__ == "__main__":
    main()
`;

const CI = `name: ci
on:
  pull_request:
  push:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-24.04
    services:
      postgres:
        image: postgres:17
        env:
          POSTGRES_PASSWORD: test
        ports: ["5432:5432"]
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-node@v5
        with:
          node-version: 22
          cache: npm
      - run: npm ci
      - run: npm run migrate
      - run: npm test
`;

const DOCKERFILE = `FROM node:22-slim AS build
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM node:22-slim
WORKDIR /app
ENV NODE_ENV=production
COPY --from=build /src/dist ./dist
COPY --from=build /src/node_modules ./node_modules
USER node
CMD ["node", "dist/index.js"]
`;

const MAKEFILE = `.PHONY: dev test migrate

dev:
\tnpm run dev

test:
\tnpm test

migrate:
\tnode scripts/migrate.js
`;

const GITIGNORE = `node_modules
dist
coverage
.env
*.log
`;

const ENV = `# Copy to .env and fill in.
STOREFRONT_DATABASE_URL=postgres://localhost:5432/storefront
STOREFRONT_PAYMENTS_URL=https://pay-sandbox.example.com
STOREFRONT_WEBHOOK_SECRET=
`;

const MIGRATION = `-- +goose Up
alter table orders
    add column idempotency_key text,
    add column paid_at timestamptz;

create unique index concurrently orders_idempotency_key
    on orders (idempotency_key)
    where idempotency_key is not null;

-- +goose Down
drop index orders_idempotency_key;
alter table orders
    drop column idempotency_key,
    drop column paid_at;
`;

const RETRIES_KO = `# 결제 웹훅 재시도 설계

결제 서비스가 보내는 웹훅을 받는 쪽이 잠깐 죽어 있어도 주문이 빠지지 않게 한다.

## 다시 보내는 경우

| 응답 | 다시 보내는가 | 이유 |
| --- | --- | --- |
| 네트워크 오류 | 예 | 저절로 풀릴 수 있다 |
| 500 이상 | 예 | 받는 쪽의 일시적인 장애 |
| 429 | 예, \`Retry-After\`만큼 기다린 뒤 | 받는 쪽이 나중에 오라고 했다 |
| 400~499 | 아니오 | 요청 자체가 틀렸다 |

## 기다리는 시간

처음 200ms에서 시작해 매번 두 배로 늘리고, 0.5~1.5 사이의 무작위 값을 곱한다.

\`\`\`ts
await sleep(wait * (0.5 + Math.random()));
wait *= 2;
\`\`\`

다섯 번 모두 실패하면 마지막 응답을 그대로 남기고, 사람이 다시 보낼 수 있게 목록에 올린다.

> 무작위 값을 곱하지 않으면 장애가 풀리는 순간에 밀린 요청이 한꺼번에 몰린다.

![시도 횟수별 성공률](../reports/errors-by-hour.png)
`;

const RETRIES = `# Webhook retries

A delivery that fails is tried again, up to five times.

1. The wait before each try doubles, from 200 ms.
2. It is multiplied by a random factor between 0.5 and 1.5.
3. A \`Retry-After\` header of the endpoint replaces the wait.

Only what may pass by itself is tried again:

- an error of the network
- an answer of \`500\` or more
- \`429\`, after the time that the endpoint asks for

The Korean version of this page is [결제 웹훅 재시도 설계](결제%20웹훅%20재시도%20설계.md).
`;

const SUMMARY = `# Checkout errors, night of 24 November

**412** requests failed between 23:40 and 00:20 UTC. All of them trace back to
one change.

## What happened

| Time (UTC) | Event |
| --- | --- |
| 23:38 | \`storefront\` 2.14.0 is deployed |
| 23:40 | The first \`TypeError: Cannot read properties of undefined (reading 'total')\` |
| 00:17 | The deploy is rolled back |
| 00:20 | The last failed request |

## Cause

\`totalOf\` reads \`cart.region\` before the cart has one. Carts that were
created before the deploy have no region.

\`\`\`diff
-  const tax = taxFor(cart.region, sum(lines));
+  const tax = taxFor(cart.region ?? DEFAULT_REGION, sum(lines));
\`\`\`

## Files

- [The report, with the chart](checkout-errors.html)
- [The same as a PDF](checkout-errors.pdf)
- ![Errors an hour](errors-by-hour.png)
`;

const ARCHITECTURE = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 720 300" width="720" height="300" font-family="system-ui, sans-serif" font-size="17" font-weight="600">
<rect width="720" height="300" rx="18" fill="#f3f6fb"/>
<g fill="#fff" stroke="#b9d6fb" stroke-width="2"><rect x="36" y="100" width="170" height="100" rx="16"/><rect x="275" y="24" width="170" height="92" rx="16"/><rect x="275" y="184" width="170" height="92" rx="16"/><rect x="514" y="100" width="170" height="100" rx="16"/></g>
<g fill="none" stroke="#0b6fe0" stroke-width="3" stroke-linecap="round"><path d="M206 135 275 84M206 165 275 216M445 70 514 132M445 230 514 168"/></g>
<g fill="#0d1b2e" text-anchor="middle"><text x="121" y="156">Checkout</text><text x="360" y="76">Payments</text><text x="360" y="236">Webhooks</text><text x="599" y="156">Ledger</text></g>
</svg>
`;

// A report as an agent writes one: a page that stands by itself, with its
// styles and its script in it.
const REPORT = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Checkout errors, 24 November</title>
<style>
  :root { color-scheme: light dark; --ink: #0d1b2e; --dim: #5d6b82; --line: #e3e8ef; --bar: #0b6fe0; --hot: #f0553a; --page: #fff; --card: #f5f8fc; }
  @media (prefers-color-scheme: dark) { :root { --ink: #e9eff8; --dim: #93a1b9; --line: #1e2c45; --page: #101c30; --card: #0c1628; } }
  body { margin: 0; padding: 32px; font: 15px/1.6 system-ui, sans-serif; color: var(--ink); background: var(--page); }
  h1 { margin: 0 0 4px; font-size: 24px; letter-spacing: -0.02em; }
  p.lead { margin: 0 0 24px; color: var(--dim); }
  .tiles { display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px; margin-bottom: 24px; }
  .tile { padding: 14px 16px; border-radius: 14px; background: var(--card); }
  .tile b { display: block; font-size: 26px; letter-spacing: -0.02em; }
  .tile span { color: var(--dim); font-size: 13px; }
  canvas { width: 100%; height: 240px; border-radius: 14px; background: var(--card); }
  table { width: 100%; border-collapse: collapse; margin-top: 24px; }
  th, td { text-align: left; padding: 9px 10px; border-bottom: 1px solid var(--line); }
  th { color: var(--dim); font-size: 12.5px; cursor: pointer; user-select: none; }
  th.on::after { content: " \\2193"; }
  td.n { text-align: right; font-variant-numeric: tabular-nums; }
  #said { margin-top: 12px; color: var(--dim); font-size: 13px; min-height: 20px; }
</style>
</head>
<body>
<h1>Checkout errors</h1>
<p class="lead">24 November, 23:40 to 00:20 UTC · storefront 2.14.0</p>
<div class="tiles">
  <div class="tile"><b>412</b><span>failed requests</span></div>
  <div class="tile"><b>37 min</b><span>until the rollback</span></div>
  <div class="tile"><b>1</b><span>cause</span></div>
</div>
<canvas id="chart" width="1200" height="480"></canvas>
<p id="said">Move over a bar to read it.</p>
<table>
  <thead><tr><th data-key="error">Error</th><th data-key="count" class="on">Requests</th><th data-key="first">First seen</th></tr></thead>
  <tbody id="rows"></tbody>
</table>
<script>
  const minutes = [2, 9, 21, 34, 41, 38, 44, 39, 36, 31, 29, 33, 27, 18, 7, 3];
  const errors = [
    { error: "TypeError: Cannot read properties of undefined (reading 'total')", count: 371, first: "23:40" },
    { error: "WebhookTimeout: delivery timed out after 5000 ms", count: 29, first: "23:44" },
    { error: "PaymentDeclined: the card was declined by its issuer", count: 9, first: "23:41" },
    { error: "RangeError: rounding of cents is out of range", count: 3, first: "00:02" },
  ];
  const canvas = document.getElementById("chart");
  const said = document.getElementById("said");
  const style = getComputedStyle(document.documentElement);
  let over = -1;
  function draw() {
    const c = canvas.getContext("2d");
    const { width, height } = canvas;
    c.clearRect(0, 0, width, height);
    const most = Math.max(...minutes);
    const step = (width - 80) / minutes.length;
    minutes.forEach((count, index) => {
      const tall = ((height - 90) * count) / most;
      c.fillStyle = style.getPropertyValue(index === over ? "--hot" : "--bar");
      c.beginPath();
      c.roundRect(40 + index * step + 8, height - 50 - tall, step - 16, tall, 8);
      c.fill();
    });
    c.fillStyle = style.getPropertyValue("--dim");
    c.font = "22px system-ui";
    c.fillText("23:40", 40, height - 16);
    c.fillText("00:20", width - 100, height - 16);
  }
  canvas.addEventListener("mousemove", (event) => {
    const box = canvas.getBoundingClientRect();
    const x = ((event.clientX - box.left) / box.width) * canvas.width;
    const index = Math.floor((x - 40) / ((canvas.width - 80) / minutes.length));
    over = index >= 0 && index < minutes.length ? index : -1;
    said.textContent = over < 0 ? "Move over a bar to read it." : minutes[over] + " failed requests in minutes " + over * 2.5 + " to " + (over + 1) * 2.5;
    draw();
  });
  function rows(key) {
    const sorted = [...errors].sort((a, b) => (typeof a[key] === "number" ? b[key] - a[key] : String(a[key]).localeCompare(b[key])));
    document.getElementById("rows").innerHTML = sorted
      .map((row) => "<tr><td>" + row.error + "</td><td class='n'>" + row.count + "</td><td>" + row.first + "</td></tr>")
      .join("");
    for (const head of document.querySelectorAll("th")) head.classList.toggle("on", head.dataset.key === key);
  }
  for (const head of document.querySelectorAll("th")) head.addEventListener("click", () => rows(head.dataset.key));
  rows("count");
  draw();
</script>
</body>
</html>
`;

const CHURN_SQL = `-- Cancellations a week, by plan, over the last quarter.
select plan,
       date_trunc('week', cancelled_at) as week,
       count(*) as cancellations
from subscriptions
where cancelled_at > now() - interval '3 months'
group by 1, 2
order by 2, 1;
`;

const NOTEBOOK = `{
 "cells": [
  {"cell_type": "markdown", "metadata": {}, "source": ["# Weekly churn by plan"]},
  {"cell_type": "code", "execution_count": 1, "metadata": {}, "outputs": [], "source": ["import pandas as pd\\n", "churn = pd.read_csv('data/cancellations.csv', parse_dates=['cancelled_at'])"]},
  {"cell_type": "code", "execution_count": 2, "metadata": {}, "outputs": [], "source": ["churn.groupby(['plan', pd.Grouper(key='cancelled_at', freq='W')]).size().unstack(0).plot()"]}
 ],
 "metadata": {"kernelspec": {"display_name": "Python 3", "language": "python", "name": "python3"}},
 "nbformat": 4,
 "nbformat_minor": 5
}
`;

// ---- Files that are made ----------------------------------------------------

const TYPES = {
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  svg: "image/svg+xml",
  pdf: "application/pdf",
  json: "application/json",
  html: "text/html",
  md: "text/markdown",
  csv: "text/csv",
};

const ext = (name) =>
  name.includes(".") ? name.slice(name.lastIndexOf(".") + 1).toLowerCase() : "";

// Files that are not text, of which the example data has only the size.
const BINARY = new Set(
  "bin sqlite parquet zip gz tgz woff2 wasm xlsx docx pptx mp4 idx pack".split(
    " ",
  ),
);

// The lines that a source file of the example data repeats, by extension.
const SOURCES = {
  ts: (name, n) =>
    [
      `import { z } from "zod";`,
      ``,
      `export const ${camel(name)}${n} = z.object({`,
      `  id: z.string().uuid(),`,
      `  amount: z.number().int().nonnegative(),`,
      `  currency: z.enum(["EUR", "USD", "KRW"]),`,
      `});`,
      ``,
      `export function ${camel(name)}Of${n}(input: unknown) {`,
      `  const parsed = ${camel(name)}${n}.safeParse(input);`,
      `  if (!parsed.success) throw new Error(parsed.error.message);`,
      `  return parsed.data;`,
      `}`,
      ``,
    ].join("\n"),
  js: (name, n) =>
    `export function ${camel(name)}${n}(value) {\n  return value == null ? null : String(value).trim();\n}\n\n`,
  py: (name, n) =>
    `def ${snake(name)}_${n}(rows):\n    """Returns the rows that are not cancelled."""\n    return [row for row in rows if not row.get("cancelled_at")]\n\n\n`,
  go: (name, n) =>
    `// ${pascal(name)}${n} returns the total of the amounts, in cents.\nfunc ${pascal(name)}${n}(amounts []int64) int64 {\n\tvar total int64\n\tfor _, amount := range amounts {\n\t\ttotal += amount\n\t}\n\treturn total\n}\n\n`,
  sql: (_name, n) =>
    `create table if not exists audit_${n} (\n    id bigint generated always as identity primary key,\n    happened_at timestamptz not null default now(),\n    what text not null\n);\n\n`,
  yaml: (_name, n) =>
    `- name: step-${n}\n  run: npm test -- --shard=${n}/8\n  timeout-minutes: 10\n`,
  css: (_name, n) =>
    `.row-${n} {\n  display: flex;\n  gap: 8px;\n  padding: 6px 10px;\n}\n\n`,
  sh: (_name, n) =>
    `echo "step ${n}"\nnpm run migrate -- --to ${n} || exit 1\n\n`,
};
SOURCES.tsx = SOURCES.ts;
SOURCES.mjs = SOURCES.js;
SOURCES.yml = SOURCES.yaml;

const words = (name) =>
  name
    .replace(/\.[^.]+$/, "")
    .split(/[^a-zA-Z0-9]+/)
    .filter(Boolean);
const pascal = (name) =>
  words(name)
    .map((word) => word[0].toUpperCase() + word.slice(1))
    .join("") || "Part";
const camel = (name) => {
  const made = pascal(name);
  return made[0].toLowerCase() + made.slice(1);
};
const snake = (name) => words(name).join("_").toLowerCase() || "part";

function seedOf(name) {
  let sum = 0;
  for (const letter of name) sum = (sum * 31 + letter.codePointAt(0)) >>> 0;
  return sum;
}

// made returns the bytes of a file that has only a size.
async function made(name, size) {
  const kind = ext(name);
  const most = Math.min(size, MOST);
  if (BINARY.has(kind)) {
    const random = seeded(seedOf(name));
    const block = new Uint8Array(Math.min(most, 1 << 16));
    for (let at = 0; at < block.length; at++) block[at] = random() * 256;
    const parts = [];
    for (let left = most; left > 0; left -= block.length)
      parts.push(left >= block.length ? block : block.slice(0, left));
    return new Blob(parts, { type: "application/octet-stream" });
  }
  const source = SOURCES[kind];
  if (source) {
    const head = kind === "sh" ? "#!/bin/sh\nset -eu\n\n" : "";
    let text = head;
    for (let n = 1; text.length < most; n++) text += source(name, n);
    return new Blob([text.slice(0, most)], { type: "text/plain" });
  }
  return bytesOf({
    name,
    media_type: TYPES[kind] ?? "text/plain",
    size: most,
  });
}

// ---- The workspaces ---------------------------------------------------------

// many returns a spec of so many files, named by a function of their number.
function many(count, name, size) {
  const spec = {};
  for (let n = 1; n <= count; n++)
    spec[name(n)] = typeof size === "function" ? size(n) : size;
  return spec;
}

const two = (n) => String(n).padStart(2, "0");
const three = (n) => String(n).padStart(3, "0");

const PACKAGES =
  "accepts ajv ansi-styles anymatch argparse balanced-match braces buffer-from bytes cac chai chalk chokidar color-convert commander concat-map content-type cookie cross-spawn debug deep-eql depd dotenv esbuild escape-html estree-walker etag expect-type fast-deep-equal fast-glob fastify fastq fdir fill-range find-up forwarded glob-parent has-flag http-errors iconv-lite inherits ipaddr.js is-extglob is-glob is-number isexe js-tokens json-schema-traverse light-my-request loupe magic-string merge2 micromatch mime-db mime-types minimatch ms nanoid negotiator on-finished parseurl path-key pathe pathval pg pg-pool pg-protocol picocolors picomatch pino postcss proxy-addr qs range-parser raw-body resolve-from rfdc rollup safe-buffer safer-buffer semver setprototypeof shebang-command siginfo sonic-boom source-map-js split2 stackback statuses std-env strip-ansi supports-color tinybench tinyexec tinyglobby tinypool tinyrainbow tinyspy to-regex-range toidentifier tsx type-is typescript undici unpipe vary vite vite-node vitest which why-is-node-running xtend zod".split(
    " ",
  );

// The packages of a project, which are many: each name a few times over,
// as the versions of a large tree are.
function packages() {
  const spec = { ".bin": many(18, (n) => PACKAGES[n * 3], 412) };
  const pkg = () => ({
    LICENSE: 1071,
    "README.md": 3400,
    "index.js": 5200,
    "package.json": 890,
    dist: { "index.cjs": 18400, "index.d.ts": 2300, "index.mjs": 17900 },
  });
  for (const name of PACKAGES) spec[name] = pkg;
  for (let n = 0; n < 1100; n++) {
    const name = `${PACKAGES[n % PACKAGES.length]}-${PACKAGES[(n * 7 + 3) % PACKAGES.length].split("-")[0]}${n > 400 ? "-" + (n % 97) : ""}`;
    spec[name] = pkg;
  }
  spec["@types"] = () =>
    Object.fromEntries(
      ["node", "pg", "semver", "estree", "chai", "deep-eql"].map((name) => [
        name,
        { "index.d.ts": 48200, "package.json": 1200, "README.md": 900 },
      ]),
    );
  return spec;
}

// The repository of the storefront, which the sessions of its engineers
// have checked out.
function storefront() {
  return {
    ".git": () => ({
      HEAD: "ref: refs/heads/main\n",
      config:
        '[core]\n\trepositoryformatversion = 0\n[remote "origin"]\n\turl = https://git.example.com/shop/storefront.git\n',
      index: 48211,
      objects: () =>
        many(
          256,
          (n) => (n - 1).toString(16).padStart(2, "0"),
          () => ({}),
        ),
      refs: { heads: { main: "8f3c2a71d5b0c9e4a6f1d2b3c4e5f60718293a4b\n" } },
    }),
    ".github": { workflows: { "ci.yml": CI } },
    ".env.example": ENV,
    ".gitignore": GITIGNORE,
    Dockerfile: DOCKERFILE,
    LICENSE: 11358,
    Makefile: MAKEFILE,
    "NOTES.md": NOTES,
    "README.md": README,
    "package-lock.json": 412800,
    "package.json": PACKAGE,
    "tsconfig.json": TSCONFIG,
    coverage: {},
    docs: {
      "architecture.svg": ARCHITECTURE,
      "webhook-retries.md": RETRIES,
      "결제 웹훅 재시도 설계.md": RETRIES_KO,
      "api.md": 18400,
      "runbook.md": 9200,
      "webhook-retries-design.pdf": 2411724,
    },
    migrations: {
      ...many(
        34,
        (n) =>
          `${String(n).padStart(5, "0")}_${["orders", "carts", "coupons", "webhooks", "payments", "audit"][n % 6]}_${n}.sql`,
        1400,
      ),
      "00035_orders_idempotency_key.sql": MIGRATION,
    },
    node_modules: packages,
    scripts: {
      "migrate.js": 2100,
      "release.sh": 940,
      "seed.py": SEED_PY,
    },
    src: {
      "index.ts": 1800,
      "config.ts": 2600,
      cart: {
        "coupons.ts": 4100,
        "price.ts": 3600,
        "rules.ts": RULES,
        "types.ts": 1300,
      },
      catalogue: many(
        14,
        (n) =>
          [
            "brands",
            "categories",
            "images",
            "inventory",
            "products",
            "search",
            "variants",
          ][n % 7] +
          (n > 7 ? ".test-data" : "") +
          ".ts",
        3200,
      ),
      checkout: {
        "intent.ts": 5200,
        "session.ts": 4400,
        "tax.ts": 2900,
        "total.ts": TOTAL,
        "types.ts": 1700,
      },
      vendor: {
        legacy: {
          payments: {
            adapters: {
              v1: {
                internal: { compat: { "shim.ts": 2200, "README.md": 600 } },
              },
            },
          },
        },
      },
      webhooks: {
        "endpoints.ts": 3900,
        "sender.ts": SENDER,
        "signature.ts": 2100,
        "types.ts": 1200,
      },
    },
    test: {
      cart: { "price.test.ts": 5100, "rules.test.ts": 3300 },
      checkout: { "total.test.ts": 4700 },
      webhooks: { "sender.test.ts": 6200 },
    },
  };
}

// What a session adds to the repository, by the title of the session.
const ADDED = {
  "Triage last night's checkout errors": () => ({
    reports: ago(70 * MINUTE, {
      "checkout-errors.html": REPORT,
      "checkout-errors.pdf": 184320,
      "errors-by-hour.png": 96256,
      "summary.md": SUMMARY,
    }),
    data: ago(75 * MINUTE, {
      "checkout-errors.log": 1843200,
      "requests-2026-11-24.csv": 14680064,
      "events.parquet": 231211008,
      "cancellations by plan and region, export for the board (final) v3.xlsx": 912384,
    }),
    logs: ago(
      76 * MINUTE,
      many(
        320,
        (n) => `worker-2026-11-${two(1 + (n % 24))}-${three(n)}.log`,
        (n) => 20000 + n * 1700,
      ),
    ),
    tmp: { latest: link("../reports") },
  }),
  "Add retry with backoff to the webhook sender": () => ({
    "CHANGES.md": ago(
      44 * MINUTE,
      "# Changes\n\n- The webhook sender tries a delivery up to five times.\n- A `Retry-After` header replaces the wait.\n",
    ),
  }),
  "결제 웹훅 재시도 로직 검토": () => ({
    "검토 결과.md": ago(
      DAY + 4 * HOUR,
      "# 검토 결과\n\n- 재시도는 다섯 번, 기다림은 200ms부터 두 배씩.\n- `429`는 `Retry-After`를 따른다.\n- 무작위 값을 곱하는 부분에 테스트가 없다.\n",
    ),
  }),
};

// The workspace of an analyst, which is no repository: queries, what they
// returned, and what was drawn of it.
function analysis() {
  return {
    "README.md":
      "# churn\n\nWeekly cancellations by plan, from the warehouse.\n\n- `queries/` the SQL\n- `data/` what the queries returned\n- `report.html` the chart\n",
    "report.html": ago(
      7 * HOUR,
      REPORT.replaceAll("Checkout errors", "Weekly churn"),
    ),
    "report.pdf": ago(7 * HOUR, 310272),
    "churn.ipynb": ago(7 * HOUR, NOTEBOOK),
    charts: ago(
      7 * HOUR,
      many(
        12,
        (n) =>
          `churn-${["basic", "pro", "team", "enterprise"][n % 4]}-${2026}-${two(n)}.png`,
        88000,
      ),
    ),
    data: ago(7 * HOUR + 20 * MINUTE, {
      "cancellations.csv": 5242880,
      "subscriptions.csv": 48234496,
      "plans.json": 2048,
      "warehouse.sqlite": 734003200,
    }),
    queries: {
      "churn-by-plan.sql": CHURN_SQL,
      ...many(9, (n) => `cohort-${two(n)}.sql`, 900),
    },
  };
}

const workspaces = new Map();

// forget lets go of the workspaces, as when the example data begins anew.
export function forget() {
  workspaces.clear();
}

// workspaceOf returns the workspace of a session of an agent: the root of
// its files. Once the agent has worked in the session, a session of an
// analyst has an analysis, and any other the repository of the storefront
// with what the session added to it. Until then the workspace has only what
// was written to it.
export function workspaceOf(session, agent, now, worked) {
  let root = workspaces.get(session.id);
  if (!root) {
    root = directory(new Map(), Date.parse(session.created_at));
    workspaces.set(session.id, root);
  }
  if (worked && !root.worked) {
    root.worked = true;
    const spec =
      agent.slug === "sql-analyst"
        ? analysis()
        : { ...storefront(), ...(ADDED[session.title]?.() ?? {}) };
    for (const [name, node] of growAll(spec, root.at, now))
      if (!root.entries.has(name)) root.entries.set(name, node);
  }
  return root;
}

// ---- Reading and writing ----------------------------------------------------

export class NoSuchFile extends Error {}
export class NotADirectory extends Error {}
export class IsADirectory extends Error {}

// parts returns the names of a path below the workspace, or nothing if it
// leaves the workspace or names nothing.
export function parts(path) {
  const names = String(path ?? "")
    .split("/")
    .filter((name) => name !== "");
  return names.some((name) => name === "." || name === "..") ? null : names;
}

// find returns the node at the path. A link is followed where it stays in
// the workspace.
function find(root, path, hops = 0) {
  const names = parts(path);
  if (!names || hops > 8) throw new NoSuchFile();
  let node = root;
  const walked = [];
  for (const name of names) {
    if (node.kind !== "directory") throw new NoSuchFile();
    node = entriesOf(node).get(name);
    if (!node) throw new NoSuchFile();
    if (node.kind === "symlink") {
      const target = resolve(walked, node.target);
      if (target == null) throw new NoSuchFile();
      node = find(root, target, hops + 1);
    }
    walked.push(name);
  }
  return node;
}

// resolve returns the path that a link in the directory points at, or
// nothing if it leaves the workspace.
function resolve(directory, target) {
  if (target.startsWith("/")) return null;
  const names = [...directory];
  for (const name of target.split("/")) {
    if (name === "" || name === ".") continue;
    if (name !== "..") names.push(name);
    else if (!names.length) return null;
    else names.pop();
  }
  return names.join("/");
}

const order = (a, b) =>
  a.toLowerCase() < b.toLowerCase()
    ? -1
    : a.toLowerCase() > b.toLowerCase()
      ? 1
      : a < b
        ? -1
        : 1;

function record(name, node) {
  const entry = {
    name,
    type: node.kind,
    modified_at: new Date(node.at).toISOString(),
  };
  if (node.kind === "file") entry.size = node.size;
  if (node.kind === "symlink") entry.target = node.target;
  return entry;
}

// list returns the entries of a directory, the directories first and each
// kind by name: up to limit of them, after the one named after.
export function list(root, path, after, limit) {
  const node = find(root, path);
  if (node.kind !== "directory") throw new NotADirectory();
  const all = [...entriesOf(node)].sort(
    ([a, x], [b, y]) =>
      (x.kind === "directory" ? 0 : 1) - (y.kind === "directory" ? 0 : 1) ||
      order(a, b),
  );
  const from = after ? all.findIndex(([name]) => name === after) + 1 : 0;
  const page = all.slice(from, from + limit);
  const listed = { entries: page.map(([name, child]) => record(name, child)) };
  if (from + limit < all.length) listed.next = page.at(-1)[0];
  return listed;
}

// stat returns the entry of the file or directory at the path.
export function stat(root, path) {
  const names = parts(path) ?? [];
  return record(names.at(-1) ?? "", find(root, path));
}

// read returns the bytes of the file at the path.
export async function read(root, path) {
  const node = find(root, path);
  if (node.kind !== "file") throw new IsADirectory();
  const name = parts(path).at(-1);
  if (node.blob) return node.blob;
  if (node.text != null)
    return new Blob([node.text], { type: TYPES[ext(name)] ?? "text/plain" });
  return made(name, node.size);
}

// write puts the file at the path, with the directories above it, and
// returns its entry and whether it replaced one.
export function write(root, path, blob, now) {
  const names = parts(path);
  if (!names?.length) throw new NoSuchFile();
  let node = root;
  for (const name of names.slice(0, -1)) {
    let child = entriesOf(node).get(name);
    if (!child) {
      child = directory(new Map(), now);
      entriesOf(node).set(name, child);
      node.at = now;
    }
    if (child.kind !== "directory") throw new NotADirectory();
    node = child;
  }
  const name = names.at(-1);
  const had = entriesOf(node).get(name);
  if (had?.kind === "directory") throw new IsADirectory();
  const file = { kind: "file", blob, size: blob.size, at: now };
  entriesOf(node).set(name, file);
  if (!had) node.at = now;
  return { entry: record(name, file), replaced: !!had };
}

// edit changes the text of a file as an agent does: what follows `before`
// is replaced by what follows it in `after`, or `after` is added at the end.
export async function edit(root, path, before, after, now) {
  let text = "";
  try {
    text = await (await read(root, path)).text();
  } catch {
    // The agent makes the file.
  }
  const changed =
    before && text.includes(before)
      ? text.replace(before, after)
      : text + after;
  return write(root, path, new Blob([changed], { type: "text/plain" }), now);
}

// The most entries that a search looks at.
const LOOKED = 60000;

// search returns the files and directories whose names have the words, the
// nearest to the root first: up to limit of them.
export function search(root, query, limit) {
  const wanted = query.toLowerCase();
  const found = [];
  let looked = 0;
  let cut = false;
  const walk = (node, path, depth) => {
    for (const [name, child] of entriesOf(node)) {
      if (++looked > LOOKED) {
        cut = true;
        return;
      }
      const at = path ? path + "/" + name : name;
      if (name.toLowerCase().includes(wanted))
        found.push({ depth, entry: { path: at, ...record(name, child) } });
      if (child.kind === "directory") walk(child, at, depth + 1);
    }
  };
  walk(root, "", 0);
  found.sort(
    (a, b) =>
      (a.entry.name.toLowerCase().startsWith(wanted) ? 0 : 1) -
        (b.entry.name.toLowerCase().startsWith(wanted) ? 0 : 1) ||
      a.depth - b.depth ||
      order(a.entry.path, b.entry.path),
  );
  const entries = found.slice(0, limit).map(({ entry }) => entry);
  return { entries, truncated: cut || found.length > limit };
}

// ---- Archives ---------------------------------------------------------------

const CRC = new Uint32Array(256).map((_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});

function crc32(data) {
  let crc = 0xffffffff;
  for (const byte of data) crc = CRC[(crc ^ byte) & 0xff] ^ (crc >>> 8);
  return (crc ^ 0xffffffff) >>> 0;
}

// The most bytes of one file that an archive of the example data holds.
const ARCHIVED = 4 << 20;

// The most files that an archive of the example data holds.
const FILES = 4000;

// archive returns the directory at the path as a ZIP file, whose files are
// stored as they are. The paths in it begin with the name of the directory.
export async function archive(root, path, top) {
  const node = find(root, path);
  if (node.kind !== "directory") throw new NotADirectory();
  const files = [];
  const walk = (directory, at) => {
    for (const [name, child] of entriesOf(directory)) {
      if (files.length >= FILES) return;
      const below = at + "/" + name;
      if (child.kind === "directory") walk(child, below);
      else if (child.kind === "file") files.push(below);
    }
  };
  walk(node, top);
  const chunks = [];
  const central = [];
  let offset = 0;
  for (const file of files) {
    const within = file.slice(top.length + 1);
    const whole = await read(root, path ? path + "/" + within : within);
    const data = new Uint8Array(await whole.slice(0, ARCHIVED).arrayBuffer());
    const name = encoder.encode(file);
    const crc = crc32(data);
    const local = new DataView(new ArrayBuffer(30));
    local.setUint32(0, 0x04034b50, true);
    local.setUint16(4, 20, true);
    // The names are UTF-8.
    local.setUint16(6, 0x0800, true);
    local.setUint32(14, crc, true);
    local.setUint32(18, data.length, true);
    local.setUint32(22, data.length, true);
    local.setUint16(26, name.length, true);
    chunks.push(local.buffer, name, data);
    const entry = new DataView(new ArrayBuffer(46));
    entry.setUint32(0, 0x02014b50, true);
    entry.setUint16(4, 20, true);
    entry.setUint16(6, 20, true);
    entry.setUint16(8, 0x0800, true);
    entry.setUint32(16, crc, true);
    entry.setUint32(20, data.length, true);
    entry.setUint32(24, data.length, true);
    entry.setUint16(28, name.length, true);
    entry.setUint32(42, offset, true);
    central.push(entry.buffer, name);
    offset += 30 + name.length + data.length;
  }
  const size = central.reduce((sum, part) => sum + part.byteLength, 0);
  const end = new DataView(new ArrayBuffer(22));
  end.setUint32(0, 0x06054b50, true);
  end.setUint16(8, files.length, true);
  end.setUint16(10, files.length, true);
  end.setUint32(12, size, true);
  end.setUint32(16, offset, true);
  return new Blob([...chunks, ...central, end.buffer], {
    type: "application/zip",
  });
}

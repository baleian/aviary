// The files of the example data. A file has no bytes until it is read: they
// are made then from its name, and are as many as its size says.

import { seeded } from "./util.js";

// seedOf returns a number for a name, which what is made for it begins from.
function seedOf(name) {
  let sum = 0;
  for (const letter of name) sum = (sum * 31 + letter.codePointAt(0)) >>> 0;
  return sum;
}

const two = (n) => String(n).padStart(2, "0");

// ---- Text -----------------------------------------------------------------

const ERRORS = [
  "TypeError: Cannot read properties of undefined (reading 'total')",
  "WebhookTimeout: delivery timed out after 5000 ms",
  "PaymentDeclined: the card was declined by its issuer",
  "RangeError: rounding of cents is out of range",
];
const PLANS = ["basic", "pro", "team", "enterprise"];
const REGIONS = ["emea", "amer", "apac"];

// What a text of the example data begins with, and the line that it goes
// on with, by the extension of its name.
const TEXTS = {
  log: [
    "",
    (n, random) => {
      const level = ["ERROR", "WARN ", "INFO "][Math.floor(random() * 3)];
      const time = `2026-11-24T23:${two(40 + (n % 20))}:${two(n % 60)}.${String(Math.floor(random() * 1000)).padStart(3, "0")}Z`;
      const said =
        level === "INFO "
          ? `retrying the payment intent attempt=${1 + (n % 4)}`
          : ERRORS[Math.floor(random() * ERRORS.length)];
      return `${time} ${level} checkout request=${Math.floor(random() * 0xffffff).toString(16)} cart=${40000 + Math.floor(random() * 9000)} ${said}`;
    },
  ],
  csv: [
    "id,plan,region,started_at,cancelled_at,reason,monthly_cents\n",
    (n, random) =>
      [
        100000 + n,
        PLANS[Math.floor(random() * PLANS.length)],
        REGIONS[Math.floor(random() * REGIONS.length)],
        `2026-0${1 + (n % 6)}-${two(1 + (n % 28))}`,
        `2026-0${7 + (n % 3)}-${two(1 + ((n * 7) % 28))}`,
        [
          "price",
          "missing feature",
          "moved to a competitor",
          "no longer needed",
        ][Math.floor(random() * 4)],
        [900, 2900, 9900, 49900][Math.floor(random() * 4)],
      ].join(","),
  ],
  json: [
    "",
    (n, random) =>
      JSON.stringify(
        {
          id: PLANS[n % PLANS.length] + (n < PLANS.length ? "" : "-" + n),
          monthly_cents: [900, 2900, 9900, 49900][n % 4],
          seats: 1 + Math.floor(random() * 50),
          features: ["sso", "audit log", "priority support"].slice(0, n % 4),
        },
        null,
        2,
      ),
  ],
  md: [
    "# Notes\n\nWhat the numbers of the quarter say, and what is still open.\n\n",
    (n) =>
      [
        "## Cancellations\n",
        "- Basic fell from 41 to 27 cancellations a week.",
        "- Pro is flat, between 9 and 12 a week.",
        "- Most who left Basic named the price as their reason.\n",
        "## Open questions\n",
        "1. Does the survey count the accounts that were paused?",
        "2. `cancelled_at` is in UTC, and the dashboard is not.\n",
      ][n % 7],
  ],
  txt: [
    "",
    (n) =>
      [
        "These files are the exports of the quarter.",
        "",
        "subscriptions  one row for each subscription that was active",
        "cancellations  one row for each subscription that was cancelled",
        "plans          the plans, and what each costs a month",
        "",
      ][n % 6],
  ],
};

// text returns a text of the size: lines that repeat after a while.
function text(name, size) {
  const ext = name.slice(name.lastIndexOf(".") + 1).toLowerCase();
  const [head, line] = TEXTS[ext] ?? TEXTS[ext === "tsv" ? "csv" : "txt"];
  const random = seeded(seedOf(name));
  let block = "";
  for (let n = 0; block.length < Math.min(size, 1 << 18); n++) {
    block += line(n, random) + "\n";
  }
  const whole = head + block.repeat(Math.ceil(size / block.length));
  return whole.slice(0, size);
}

// ---- PDF ------------------------------------------------------------------

const PROSE = [
  "A delivery that fails is tried again, up to five times. The wait before each",
  "try doubles, from 200 ms, and is multiplied by a random factor between 0.5",
  "and 1.5, so that the deliveries of an outage do not come back all at once.",
  "",
  "Only what may pass by itself is tried again: an error of the network, and an",
  "answer of 500 or more. An answer of 400 to 499 says that the request itself",
  "is wrong, and is not sent again. 429 is the exception: the endpoint asks for",
  "the delivery to come later, and says when in its Retry-After header.",
  "",
  "The last failure is kept as the endpoint gave it, with the number of tries,",
  "and the delivery is listed for a person to send again.",
];

// pdf returns a document of a few pages, in the fonts that every reader has.
function pdf(name, size) {
  const escaped = (words) => words.replace(/[()\\]/g, "\\$&");
  const write = (x, y, points, words, bold) =>
    `BT /${bold ? "B" : "R"} ${points} Tf ${x} ${y} Td (${escaped(words)}) Tj ET\n`;
  const box = (x, y, width, height, grey) =>
    `${grey} ${grey} ${grey} rg ${x} ${y} ${width} ${height} re f\n`;
  const random = seeded(seedOf(name));
  const headings = ["Retries", "What is tried again", "Measurements"];
  const pages = headings.map((heading, page) => {
    let body = box(0, 0, 595, 842, 1) + "0.05 0.11 0.18 rg\n";
    let y = 770;
    if (page === 0) {
      body += write(64, y, 26, "Webhook retries", true);
      body +=
        "0.36 0.42 0.51 rg\n" + write(64, y - 24, 11, "Design, 2026-11-24");
      body += "0.05 0.11 0.18 rg\n";
      y -= 72;
    }
    body += write(64, y, 15, `${page + 1}. ${heading}`, true);
    y -= 28;
    for (const line of PROSE) {
      if (line) body += write(64, y, 11, line);
      y -= line ? 16 : 10;
    }
    y -= 14;
    // A chart of bars, and a table below it.
    body += box(64, y - 150, 467, 150, 0.95);
    for (let bar = 0; bar < 12; bar++) {
      const tall = 20 + Math.floor(random() * 110);
      body += `0.04 0.44 0.88 rg ${84 + bar * 37} ${y - 140} 22 ${tall} re f\n`;
    }
    y -= 180;
    body += box(64, y - 4, 467, 22, 0.92) + "0.05 0.11 0.18 rg\n";
    body +=
      write(72, y + 2, 10, "Try", true) + write(180, y + 2, 10, "Wait", true);
    body += write(300, y + 2, 10, "Deliveries that passed", true);
    for (let row = 1; row <= 5; row++) {
      y -= 22;
      body += write(72, y + 2, 10, String(row));
      body += write(180, y + 2, 10, `${100 * 2 ** row} ms`);
      body += write(
        300,
        y + 2,
        10,
        `${Math.floor(random() * 60 + 30 / row)} %`,
      );
    }
    body +=
      "0.36 0.42 0.51 rg\n" +
      write(280, 40, 9, `${page + 1} / ${headings.length}`);
    return body;
  });

  // The document with a filler of so many bytes, which makes up its size.
  const build = (filler) => {
    const objects = [
      "<< /Type /Catalog /Pages 2 0 R >>",
      `<< /Type /Pages /Count ${pages.length} /Kids [${pages.map((_, page) => `${5 + 2 * page} 0 R`).join(" ")}] >>`,
      "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
      "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold >>",
    ];
    pages.forEach((body, page) => {
      objects.push(
        `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /R 3 0 R /B 4 0 R >> >> /Contents ${6 + 2 * page} 0 R >>`,
        `<< /Length ${body.length} >>\nstream\n${body}endstream`,
      );
    });
    objects.push(
      `<< /Length ${filler} >>\nstream\n${" ".repeat(filler)}\nendstream`,
    );
    let whole = "%PDF-1.4\n";
    const offsets = objects.map((object, index) => {
      const offset = whole.length;
      whole += `${index + 1} 0 obj\n${object}\nendobj\n`;
      return offset;
    });
    const table = whole.length;
    whole += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
    for (const offset of offsets) {
      whole += `${String(offset).padStart(10, "0")} 00000 n \n`;
    }
    return `${whole}trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${table}\n%%EOF\n`;
  };
  let filler = 0;
  let whole = build(filler);
  // The filler says how long it is, in digits that count too.
  for (let tries = 0; tries < 4 && whole.length !== size; tries++) {
    filler = Math.max(0, filler + size - whole.length);
    whole = build(filler);
  }
  return whole;
}

// ---- Pictures -------------------------------------------------------------

// picture draws what a screenshot of a dashboard may look like: as wide as
// a screen, or as tall as that of a phone. It returns nothing where nothing
// can be drawn.
async function picture(name, type) {
  if (typeof OffscreenCanvas === "undefined") return undefined;
  const random = seeded(seedOf(name));
  const tall = /\(\d+\)/.test(name);
  const [width, height] = tall ? [780, 1640] : [1440, 900];
  const canvas = new OffscreenCanvas(width, height);
  const c = canvas.getContext("2d");
  const dark = random() < 0.35;
  const hue = [212, 168, 258, 338][Math.floor(random() * 4)];
  const [page, card, ink, dim, rule] = dark
    ? ["#0b1424", "#131f35", "#e9eff8", "#8a99b3", "#22314c"]
    : ["#f3f6fb", "#ffffff", "#0d1b2e", "#6a788f", "#e1e7ef"];
  const tint = (light, alpha = 1) => `hsla(${hue}, 82%, ${light}%, ${alpha})`;
  const font = (weight, px) =>
    `${weight} ${px}px "Plus Jakarta Sans Variable", system-ui, sans-serif`;
  const panel = (x, y, w, h) => {
    c.fillStyle = card;
    c.strokeStyle = rule;
    c.beginPath();
    c.roundRect(x, y, w, h, 18);
    c.fill();
    c.stroke();
  };
  const words = (says, x, y, weight, px, colour) => {
    c.font = font(weight, px);
    c.fillStyle = colour;
    c.fillText(says, x, y);
  };

  c.fillStyle = page;
  c.fillRect(0, 0, width, height);
  c.fillStyle = card;
  c.fillRect(0, 0, width, 72);
  c.fillStyle = rule;
  c.fillRect(0, 72, width, 1);
  c.fillStyle = tint(52);
  c.beginPath();
  c.roundRect(32, 20, 32, 32, 9);
  c.fill();
  words(name.replace(/\.\w+$/, ""), 80, 45, 700, 22, ink);

  // The numbers of the day, then a chart, then a table.
  const edge = 32;
  const across = tall ? 2 : 4;
  const tile = (width - edge * (across + 1)) / across;
  let y = 104;
  const titles = ["Events", "Users", "p95", "Failed"];
  for (let at = 0; at < 4; at++) {
    const x = edge + (at % across) * (tile + edge);
    const top = y + Math.floor(at / across) * 152;
    panel(x, top, tile, 120);
    words(titles[at], x + 24, top + 40, 600, 16, dim);
    words(
      String(Math.floor(random() * 9000 + 120)),
      x + 24,
      top + 90,
      700,
      40,
      ink,
    );
    words(
      `${random() < 0.5 ? "+" : "-"}${(random() * 20).toFixed(1)} %`,
      x + tile - 104,
      top + 88,
      700,
      16,
      at === 3 ? "#e0492e" : tint(46),
    );
  }
  y += (tall ? 2 : 1) * 152;

  const chart = tall ? 520 : 440;
  panel(edge, y, width - 2 * edge, chart);
  words("Events an hour", edge + 28, y + 46, 700, 20, ink);
  const [left, right, top, bottom] = [
    edge + 36,
    width - edge - 36,
    y + 84,
    y + chart - 40,
  ];
  c.strokeStyle = rule;
  c.lineWidth = 1;
  for (let grid = 0; grid <= 4; grid++) {
    const level = top + ((bottom - top) * grid) / 4;
    c.beginPath();
    c.moveTo(left, level);
    c.lineTo(right, level);
    c.stroke();
  }
  let level = 0.5;
  const points = Array.from({ length: 36 }, (_, at) => {
    level = Math.min(0.95, Math.max(0.08, level + (random() - 0.5) * 0.28));
    return [left + ((right - left) * at) / 35, bottom - (bottom - top) * level];
  });
  c.beginPath();
  c.moveTo(left, bottom);
  for (const [x, at] of points) c.lineTo(x, at);
  c.lineTo(right, bottom);
  const fill = c.createLinearGradient(0, top, 0, bottom);
  fill.addColorStop(0, tint(56, 0.34));
  fill.addColorStop(1, tint(56, 0));
  c.fillStyle = fill;
  c.fill();
  c.beginPath();
  points.forEach(([x, at], index) =>
    index ? c.lineTo(x, at) : c.moveTo(x, at),
  );
  c.strokeStyle = tint(50);
  c.lineWidth = 4;
  c.lineJoin = "round";
  c.stroke();
  y += chart + edge;

  for (let row = 0; y + 64 < height; row++, y += 76) {
    panel(edge, y, width - 2 * edge, 64);
    c.fillStyle = row % 3 === 0 ? "#e0492e" : tint(52);
    c.beginPath();
    c.arc(edge + 34, y + 32, 7, 0, 2 * Math.PI);
    c.fill();
    words(
      ERRORS[row % ERRORS.length].split(":")[0],
      edge + 58,
      y + 39,
      700,
      17,
      ink,
    );
    words(
      String(Math.floor(random() * 900 + 12)),
      width - edge - 90,
      y + 39,
      600,
      17,
      dim,
    );
  }
  return canvas.convertToBlob({ type });
}

// An example of a drawing: a few boxes and what joins them.
const DRAWING = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 720 360" width="720" height="360" font-family="system-ui, sans-serif" font-size="18" font-weight="600">
<rect width="720" height="360" fill="#f3f6fb"/>
<g fill="#fff" stroke="#b9d6fb" stroke-width="2"><rect x="40" y="130" width="170" height="100" rx="16"/><rect x="275" y="40" width="170" height="100" rx="16"/><rect x="275" y="220" width="170" height="100" rx="16"/><rect x="510" y="130" width="170" height="100" rx="16"/></g>
<g fill="none" stroke="#0b6fe0" stroke-width="3" stroke-linecap="round"><path d="M210 165 275 105M210 195 275 255M445 90 510 160M445 270 510 200"/></g>
<g fill="#0d1b2e" text-anchor="middle"><text x="125" y="186">Checkout</text><text x="360" y="96">Payments</text><text x="360" y="276">Webhooks</text><text x="595" y="186">Ledger</text></g>
</svg>
`;

// bytesOf returns the file of an attachment of the example data.
export async function bytesOf({ name, media_type: type, size }) {
  const ext = name.slice(name.lastIndexOf(".") + 1).toLowerCase();
  if (ext === "pdf") return new Blob([pdf(name, size)], { type });
  if (ext === "svg") return new Blob([DRAWING.padEnd(size)], { type });
  const drawn =
    (type === "image/png" || type === "image/jpeg") &&
    (await picture(name, type));
  if (!drawn) return new Blob([text(name, size)], { type });
  // What follows a picture is passed over by those who read it.
  const rest = Math.max(0, size - drawn.size);
  return new Blob([drawn, new Uint8Array(rest)], { type });
}

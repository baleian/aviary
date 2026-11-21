// Renders the Markdown that agents write. It builds DOM nodes and never sets
// HTML, so the text of an agent cannot inject markup. It covers what agents
// commonly write: paragraphs, lists, headings, quotes, tables, code blocks,
// and inline code, emphasis and links.

import {h} from './util.js';

const INLINE = /(`+)([\s\S]*?)\1|\*\*([\s\S]+?)\*\*|(?<![\w*])\*(?!\s)([^*\n]+?)\*(?![\w*])|(?<![\w_])_(?!\s)([^_\n]+?)_(?![\w_])|\[([^\]\n]+)\]\(([^)\s]+)\)/g;

function inline(text) {
  const out = [];
  let last = 0;
  for (const match of text.matchAll(INLINE)) {
    if (match.index > last) out.push(text.slice(last, match.index));
    const [, ticks, codeText, bold, star, under, label, target] = match;
    if (ticks) out.push(h('code', codeText.trim()));
    else if (bold != null) out.push(h('strong', inline(bold)));
    else if (star != null || under != null) out.push(h('em', inline(star ?? under)));
    else if (/^(https?:|mailto:)/i.test(target)) out.push(h('a', {href: target, target: '_blank', rel: 'noopener noreferrer'}, inline(label)));
    else out.push(match[0]);
    last = match.index + match[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

const cells = (line) => line.trim().replace(/^\||\|$/g, '').split('|').map((cell) => cell.trim());

// codeBlock returns a <pre> for a block of code. The lines of a diff are
// coloured.
export function codeBlock(text, language = '') {
  const pre = h('pre.code');
  const diff = language === 'diff' || /^(@@ |\+\+\+ |--- )/m.test(text);
  if (!diff) {
    pre.append(text);
    return pre;
  }
  for (const line of text.split('\n')) {
    const kind = /^(\+\+\+|---|@@)/.test(line) ? 'hk' : line.startsWith('+') ? 'ad' : line.startsWith('-') ? 'dl' : '';
    pre.append(kind ? h('span.' + kind, line) : line + '\n');
  }
  return pre;
}

export function markdown(text) {
  const root = h('div.md');
  const lines = String(text).replace(/\r\n?/g, '\n').split('\n');
  let i = 0;
  const paragraph = [];
  const flush = () => {
    if (paragraph.length) root.append(h('p', inline(paragraph.join('\n'))));
    paragraph.length = 0;
  };
  while (i < lines.length) {
    const line = lines[i];
    const fence = line.match(/^\s*(```+|~~~+)\s*([\w+-]*)\s*$/);
    if (fence) {
      flush();
      const body = [];
      i++;
      // Text that is still being written may not have closed its fence yet.
      while (i < lines.length && !lines[i].trim().startsWith(fence[1])) body.push(lines[i++]);
      i++;
      root.append(codeBlock(body.join('\n'), fence[2]));
      continue;
    }
    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    if (heading) {
      flush();
      root.append(h('h' + Math.min(heading[1].length, 3), inline(heading[2].replace(/\s+#+\s*$/, ''))));
      i++;
      continue;
    }
    if (/^\s*([-*+]|\d+[.)])\s+/.test(line)) {
      flush();
      const ordered = /^\s*\d/.test(line);
      const list = h(ordered ? 'ol' : 'ul');
      while (i < lines.length && /^\s*([-*+]|\d+[.)])\s+/.test(lines[i]) && /^\s*\d/.test(lines[i]) === ordered) {
        const item = [lines[i].replace(/^\s*([-*+]|\d+[.)])\s+/, '')];
        i++;
        while (i < lines.length && /^\s{2,}\S/.test(lines[i]) && !/^\s*([-*+]|\d+[.)])\s+/.test(lines[i])) item.push(lines[i++].trim());
        list.append(h('li', inline(item.join(' '))));
      }
      root.append(list);
      continue;
    }
    if (/^\s*>\s?/.test(line)) {
      flush();
      const quote = [];
      while (i < lines.length && /^\s*>\s?/.test(lines[i])) quote.push(lines[i++].replace(/^\s*>\s?/, ''));
      root.append(h('blockquote', inline(quote.join('\n'))));
      continue;
    }
    if (line.includes('|') && i + 1 < lines.length && /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/.test(lines[i + 1])) {
      flush();
      const head = cells(line);
      i += 2;
      const body = [];
      while (i < lines.length && lines[i].includes('|') && lines[i].trim()) body.push(cells(lines[i++]));
      root.append(h('table',
        h('thead', h('tr', head.map((cell) => h('th', inline(cell))))),
        h('tbody', body.map((row) => h('tr', row.map((cell) => h('td', inline(cell))))))));
      continue;
    }
    if (!line.trim()) {
      flush();
      i++;
      continue;
    }
    paragraph.push(line);
    i++;
  }
  flush();
  return root;
}

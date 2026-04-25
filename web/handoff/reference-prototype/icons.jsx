/* global React */
// Inline SVG icons — Lucide-style, 1.5 stroke, 16px
const { createElement: h } = React;

const svg = (children, size = 16) => ({ className = "", style = {}, size: sz } = {}) =>
  h("svg", {
    xmlns: "http://www.w3.org/2000/svg", width: sz || size, height: sz || size,
    viewBox: "0 0 24 24", fill: "none", stroke: "currentColor",
    strokeWidth: 1.6, strokeLinecap: "round", strokeLinejoin: "round",
    className, style
  }, children);

const p = (d) => h("path", { d, key: d });
const circ = (cx, cy, r, key) => h("circle", { cx, cy, r, key: key || `${cx},${cy}` });
const line = (x1, y1, x2, y2, key) => h("line", { x1, y1, x2, y2, key: key || `${x1}${y1}${x2}${y2}` });
const rect = (x, y, w, hh, r, key) => h("rect", { x, y, width: w, height: hh, rx: r, key: key || `r${x}${y}` });

const Icons = {
  Dashboard: svg([rect(3,3,7,7,1,"a"), rect(14,3,7,7,1,"b"), rect(14,14,7,7,1,"c"), rect(3,14,7,7,1,"d")]),
  Agents:    svg([p("M12 2a5 5 0 0 1 5 5v3a5 5 0 0 1-10 0V7a5 5 0 0 1 5-5Z"), p("M8 14h8l2 7H6l2-7Z")]),
  Workflows: svg([circ(5,6,2.5,"a"), circ(5,18,2.5,"b"), circ(19,12,2.5,"c"), p("M7.5 6h6a3 3 0 0 1 3 3v.5"), p("M7.5 18h6a3 3 0 0 0 3-3V14.5")]),
  Marketplace: svg([p("M3 9h18l-1.5-5H4.5L3 9Z"), p("M5 9v10a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V9"), p("M9 14h6")]),
  Search:    svg([circ(11,11,7,"a"), line(20,20,16.5,16.5,"b")]),
  Bell:      svg([p("M6 8a6 6 0 0 1 12 0c0 6 3 8 3 8H3s3-2 3-8"), p("M10.5 20a1.5 1.5 0 0 0 3 0")]),
  Plus:      svg([line(12,5,12,19,"v"), line(5,12,19,12,"h")]),
  ChevronRight: svg([p("m9 6 6 6-6 6")]),
  ChevronDown:  svg([p("m6 9 6 6 6-6")]),
  ChevronLeft:  svg([p("m15 6-6 6 6 6")]),
  ChevronsLeft: svg([p("m11 6-6 6 6 6"), p("m18 6-6 6 6 6")]),
  ChevronsRight: svg([p("m6 6 6 6-6 6"), p("m13 6 6 6-6 6")]),
  Send:      svg([p("M22 2 11 13"), p("M22 2l-7 20-4-9-9-4 20-7Z")]),
  More:      svg([circ(5,12,1.2,"a"), circ(12,12,1.2,"b"), circ(19,12,1.2,"c")]),
  MoreV:     svg([circ(12,5,1.2,"a"), circ(12,12,1.2,"b"), circ(12,19,1.2,"c")]),
  Tool:      svg([p("M14.7 6.3a4 4 0 0 0-5.6 5.6l-6.1 6.1 2 2 6.1-6.1a4 4 0 0 0 5.6-5.6l-2.6 2.6-2-2 2.6-2.6Z")]),
  Message:   svg([p("M21 12a8 8 0 1 1-3.2-6.4L21 4l-1.2 3.6A8 8 0 0 1 21 12Z")]),
  Play:      svg([p("m7 4 12 8-12 8V4Z")]),
  PlaySmall: svg([p("m8 5 10 7-10 7V5Z")]),
  Pause:     svg([rect(6,4,4,16,1,"a"), rect(14,4,4,16,1,"b")]),
  Download:  svg([p("M12 3v12"), p("m7 10 5 5 5-5"), p("M4 20h16")]),
  Upload:    svg([p("M12 20V8"), p("m7 13 5-5 5 5"), p("M4 4h16")]),
  Clock:     svg([circ(12,12,9,"a"), p("M12 7v5l3 2")]),
  Calendar:  svg([rect(3,5,18,16,2,"a"), line(3,10,21,10,"b"), line(8,3,8,7,"c"), line(16,3,16,7,"d")]),
  Cog:       svg([circ(12,12,3,"a"), p("M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3h0A1.7 1.7 0 0 0 10 3.1V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1Z")]),
  Moon:      svg([p("M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z")]),
  Sun:       svg([circ(12,12,4,"a"), line(12,2,12,4,"b"), line(12,20,12,22,"c"), line(2,12,4,12,"d"), line(20,12,22,12,"e"), line(4.9,4.9,6.3,6.3,"f"), line(17.7,17.7,19.1,19.1,"g"), line(4.9,19.1,6.3,17.7,"h"), line(17.7,6.3,19.1,4.9,"i")]),
  Logout:    svg([p("M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"), p("m16 17 5-5-5-5"), line(21,12,9,12,"a")]),
  User:      svg([circ(12,8,4,"a"), p("M20 21a8 8 0 0 0-16 0")]),
  Key:       svg([circ(7.5,15.5,4.5,"a"), p("m21 2-9.6 9.6"), p("m15.5 7.5 3 3L22 7l-3-3")]),
  Sliders:   svg([line(4,21,4,14,"a"), line(4,10,4,3,"b"), line(12,21,12,12,"c"), line(12,8,12,3,"d"), line(20,21,20,16,"e"), line(20,12,20,3,"f"), line(1,14,7,14,"g"), line(9,8,15,8,"h"), line(17,16,23,16,"i")]),
  Filter:    svg([p("M22 3H2l8 9.5V19l4 2v-8.5L22 3Z")]),
  Star:      svg([p("M12 2l2.9 6 6.6 1-4.8 4.7 1.2 6.5L12 17l-5.9 3.2L7.3 13.7 2.5 9l6.6-1L12 2Z")]),
  Check:     svg([p("M20 6 9 17l-5-5")]),
  X:         svg([line(18,6,6,18,"a"), line(6,6,18,18,"b")]),
  Edit:      svg([p("M17 3a2.8 2.8 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5L17 3Z")]),
  Trash:     svg([p("M3 6h18"), p("M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"), p("M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6")]),
  Copy:      svg([rect(9,9,13,13,2,"a"), p("M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1")]),
  Globe:     svg([circ(12,12,9,"a"), line(3,12,21,12,"b"), p("M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18Z")]),
  Lock:      svg([rect(4,11,16,10,2,"a"), p("M8 11V7a4 4 0 0 1 8 0v4")]),
  Paperclip: svg([p("m21 12-9 9a5 5 0 0 1-7-7l9-9a3 3 0 0 1 4 4l-9 9a1 1 0 0 1-1-1l8-8")]),
  Code:      svg([p("m16 18 6-6-6-6"), p("m8 6-6 6 6 6")]),
  Terminal:  svg([p("m4 7 4 4-4 4"), line(12,15,20,15,"a")]),
  Database:  svg([p("M4 6c0-1.7 3.6-3 8-3s8 1.3 8 3-3.6 3-8 3-8-1.3-8-3Z"), p("M4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6"), p("M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3")]),
  Sparkle:   svg([p("M12 3v4"), p("M12 17v4"), p("M3 12h4"), p("M17 12h4"), p("m5.6 5.6 2.8 2.8"), p("m15.6 15.6 2.8 2.8"), p("m5.6 18.4 2.8-2.8"), p("m15.6 8.4 2.8-2.8")]),
  ArrowUp:   svg([line(12,19,12,5,"a"), p("m5 12 7-7 7 7")]),
  ArrowRight:svg([line(5,12,19,12,"a"), p("m12 5 7 7-7 7")]),
  Home:      svg([p("m3 11 9-8 9 8"), p("M5 10v10h14V10")]),
  Tag:       svg([p("M21 12 12 21l-9-9V3h9l9 9Z"), circ(7.5,7.5,1.5,"b")]),
  Folder:    svg([p("M3 6a2 2 0 0 1 2-2h4l2 3h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6Z")]),
  Layers:    svg([p("m12 3 10 5-10 5L2 8l10-5Z"), p("m2 13 10 5 10-5"), p("m2 18 10 5 10-5")]),
  Command:   svg([p("M18 3a3 3 0 0 0-3 3v12a3 3 0 0 0 3 3 3 3 0 0 0 3-3 3 3 0 0 0-3-3H6a3 3 0 0 0-3 3 3 3 0 0 0 3 3 3 3 0 0 0 3-3V6a3 3 0 0 0-3-3 3 3 0 0 0-3 3 3 3 0 0 0 3 3h12a3 3 0 0 0 3-3 3 3 0 0 0-3-3Z")]),
  Zap:       svg([p("M13 2 3 14h7l-1 8 10-12h-7l1-8Z")]),
  GitBranch: svg([line(6,3,6,15,"a"), circ(18,6,3,"b"), circ(6,18,3,"c"), p("M18 9a9 9 0 0 1-9 9")]),
  FileText:  svg([p("M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-6-6Z"), p("M14 2v6h6"), line(8,13,16,13,"b"), line(8,17,14,17,"c")]),
  ExternalLink: svg([p("M15 3h6v6"), p("M10 14 21 3"), p("M21 14v5a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5")]),
  Eye:       svg([p("M2 12s4-8 10-8 10 8 10 8-4 8-10 8S2 12 2 12Z"), circ(12,12,3,"a")]),
  Bolt:      svg([p("M13 2 3 14h7l-1 8 10-12h-7l1-8Z")]),
  Pulse:     svg([p("M3 12h4l3-9 4 18 3-9h4")]),
  Stop:      svg([rect(6,6,12,12,2,"a")]),
  Refresh:   svg([p("M3 12a9 9 0 0 1 15-6.7L21 8"), p("M21 3v5h-5"), p("M21 12a9 9 0 0 1-15 6.7L3 16"), p("M3 21v-5h5")]),
};

window.Icons = Icons;

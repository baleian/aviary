import { defaultKeymap } from "@codemirror/commands";
import {
  HighlightStyle,
  LanguageDescription,
  syntaxHighlighting,
} from "@codemirror/language";
import { languages } from "@codemirror/language-data";
import {
  highlightSelectionMatches,
  search,
  searchKeymap,
} from "@codemirror/search";
import { Compartment, EditorState } from "@codemirror/state";
import {
  drawSelection,
  EditorView,
  highlightSpecialChars,
  keymap,
  lineNumbers,
} from "@codemirror/view";
import { tags } from "@lezer/highlight";
import { useEffect, useRef } from "react";

// The colours are those of the page, so that the code follows its theme.
const look = EditorView.theme({
  "&": {
    height: "100%",
    color: "var(--ink)",
    backgroundColor: "transparent",
    fontSize: "12.5px",
  },
  "&.cm-focused": { outline: "none" },
  ".cm-scroller": {
    fontFamily: "var(--mono)",
    // Each character shows as it is written.
    fontVariantLigatures: "none",
    lineHeight: "20px",
    overflow: "auto",
  },
  ".cm-content": { padding: "10px 0 24px", caretColor: "transparent" },
  ".cm-line": { padding: "0 16px 0 6px" },
  ".cm-gutters": {
    backgroundColor: "var(--surface)",
    color: "var(--mist)",
    border: "none",
  },
  ".cm-lineNumbers .cm-gutterElement": {
    padding: "0 10px 0 16px",
    minWidth: "40px",
  },
  ".cm-selectionBackground, &.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground":
    { backgroundColor: "var(--k100)" },
  ".cm-selectionMatch": { backgroundColor: "var(--c100)" },
  ".cm-searchMatch": {
    backgroundColor: "var(--c100)",
    outline: "1px solid var(--c-line)",
  },
  ".cm-searchMatch.cm-searchMatch-selected": {
    backgroundColor: "var(--canary)",
    color: "var(--on-canary)",
  },
  ".cm-panels": {
    backgroundColor: "var(--surface-2)",
    color: "var(--ink2)",
    borderBottom: "1px solid var(--line)",
    fontFamily: "var(--sans)",
  },
  ".cm-panels.cm-panels-top": { borderBottom: "1px solid var(--line)" },
  ".cm-search": { padding: "8px 12px", fontSize: "12.5px" },
  ".cm-search label": { fontSize: "12px", color: "var(--slate)" },
  ".cm-textfield": {
    backgroundColor: "var(--surface)",
    color: "var(--ink)",
    border: "1px solid var(--ctl)",
    borderRadius: "8px",
    padding: "3px 8px",
    fontFamily: "var(--sans)",
    fontSize: "12.5px",
  },
  ".cm-button": {
    backgroundImage: "none",
    backgroundColor: "var(--surface)",
    color: "var(--ink2)",
    border: "1px solid var(--line2)",
    borderRadius: "8px",
    padding: "3px 10px",
    fontFamily: "var(--sans)",
    fontSize: "12.5px",
  },
  ".cm-panel.cm-search [name=close]": {
    color: "var(--slate)",
    fontSize: "18px",
    cursor: "pointer",
  },
});

const colours = HighlightStyle.define([
  {
    tag: [tags.keyword, tags.modifier, tags.operatorKeyword, tags.tagName],
    color: "var(--hl-key)",
  },
  {
    tag: [tags.string, tags.special(tags.string), tags.regexp],
    color: "var(--hl-str)",
  },
  {
    tag: [tags.number, tags.bool, tags.null, tags.atom, tags.unit],
    color: "var(--hl-num)",
  },
  {
    tag: [tags.comment, tags.meta, tags.processingInstruction],
    color: "var(--hl-dim)",
  },
  {
    tag: [
      tags.function(tags.variableName),
      tags.function(tags.propertyName),
      tags.labelName,
    ],
    color: "var(--hl-call)",
  },
  {
    tag: [tags.typeName, tags.className, tags.namespace],
    color: "var(--hl-type)",
  },
  {
    tag: [tags.propertyName, tags.attributeName],
    color: "var(--hl-name)",
  },
  { tag: tags.heading, color: "var(--hl-key)", fontWeight: "700" },
  { tag: tags.link, color: "var(--link)" },
  { tag: tags.emphasis, fontStyle: "italic" },
  { tag: tags.strong, fontWeight: "700" },
  { tag: tags.inserted, color: "var(--g-ink)" },
  { tag: tags.deleted, color: "var(--r-ink)" },
  { tag: tags.invalid, color: "var(--r-ink)" },
]);

// Code shows a text as it is written, with the numbers of its lines and in
// the colours of what it is written in, which its name tells. It is read
// and searched, and not changed. Lines that do not fit are broken if wrap
// says so.
export default function Code({
  text,
  name,
  wrap,
}: {
  text: string;
  name: string;
  wrap: boolean;
}) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView>(null);
  const wrapping = useRef(new Compartment());
  const language = useRef(new Compartment());

  useEffect(() => {
    const made = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: "",
        extensions: [
          lineNumbers(),
          highlightSpecialChars(),
          drawSelection(),
          EditorState.readOnly.of(true),
          EditorView.editable.of(false),
          // It takes the focus all the same, for its keys.
          EditorView.contentAttributes.of({ tabindex: "0" }),
          syntaxHighlighting(colours),
          highlightSelectionMatches(),
          search({ top: true }),
          keymap.of([...searchKeymap, ...defaultKeymap]),
          wrapping.current.of([]),
          language.current.of([]),
          look,
        ],
      }),
    });
    view.current = made;
    return () => made.destroy();
  }, []);

  useEffect(() => {
    const shown = view.current!;
    shown.dispatch({
      changes: { from: 0, to: shown.state.doc.length, insert: text },
    });
  }, [text]);

  useEffect(() => {
    view.current!.dispatch({
      effects: wrapping.current.reconfigure(
        wrap ? EditorView.lineWrapping : [],
      ),
    });
  }, [wrap]);

  useEffect(() => {
    let gone = false;
    // A notebook is JSON.
    const known = LanguageDescription.matchFilename(
      languages,
      name.replace(/\.ipynb$/, ".json"),
    );
    view.current!.dispatch({ effects: language.current.reconfigure([]) });
    void known?.load().then((support) => {
      if (!gone)
        view.current?.dispatch({
          effects: language.current.reconfigure(support),
        });
    });
    return () => {
      gone = true;
    };
  }, [name]);

  return <div ref={host} className="fv-code" />;
}

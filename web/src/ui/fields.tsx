import {
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  useState,
} from "react";
import { slugify } from "../lib/slug.ts";
import { Icon } from "./Icon.tsx";

// Field is a control of a form with its label, and a hint below it.
export function Field({
  label,
  optional,
  hint,
  children,
}: {
  label: string;
  // Marks the field as one that may be left empty, in these words or as
  // "optional".
  optional?: boolean | string;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <label className="fld">
      <span className="lab">
        {label}
        {optional && <em>{optional === true ? "optional" : optional}</em>}
      </span>
      {children}
      {hint && <span className="hint">{hint}</span>}
    </label>
  );
}

// Select is a choice among options, each a value and its text.
export function Select({
  options,
  ...props
}: {
  options: [value: string, text: string][];
} & SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <span className="sel">
      <select className="inp" {...props}>
        {options.map(([value, text]) => (
          <option key={value} value={value}>
            {text}
          </option>
        ))}
      </select>
      <Icon name="down" />
    </span>
  );
}

// Toggle is a switch with its text.
export function Toggle({
  children,
  ...props
}: { children: ReactNode } & InputHTMLAttributes<HTMLInputElement>) {
  return (
    <label className="swt">
      <input type="checkbox" {...props} />
      <i />
      <span>{children}</span>
    </label>
  );
}

// NameAndSlug is the two fields that name a project or an agent: its name,
// for people, and its slug, for URLs. The slug follows the name until it is
// edited by hand. The fields are "name" and "slug".
export function NameAndSlug({ example }: { example: string }) {
  const [slug, setSlug] = useState("");
  const [own, setOwn] = useState(false);
  return (
    <div className="fld-row">
      <Field label="Name" optional>
        <input
          className="inp"
          name="name"
          placeholder={example}
          autoComplete="off"
          maxLength={120}
          data-autofocus
          onChange={(event) => {
            if (!own) setSlug(slugify(event.target.value));
          }}
        />
      </Field>
      <Field
        label="Slug"
        hint="Lowercase letters, digits and hyphens. It cannot be changed later."
      >
        <input
          className="inp mono"
          name="slug"
          placeholder={slugify(example)}
          autoComplete="off"
          spellCheck={false}
          maxLength={63}
          value={slug}
          onChange={(event) => {
            setSlug(event.target.value);
            setOwn(event.target.value !== "");
          }}
        />
      </Field>
    </div>
  );
}

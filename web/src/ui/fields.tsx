import {
  Fragment,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  useState,
} from "react";
import { slugify } from "../lib/slug.ts";
import { Icon } from "./Icon.tsx";

// Field is a control of a form with its label, and a hint or a warning below
// it.
export function Field({
  label,
  optional,
  hint,
  warning,
  children,
}: {
  label: string;
  // Marks the field as one that may be left empty, in these words or as
  // "optional".
  optional?: boolean | string;
  hint?: ReactNode;
  // What may be wrong with the field, which can be filled in all the same.
  warning?: ReactNode;
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
      {warning && (
        <span className="hint warn" role="status">
          <Icon name="warn" />
          <span>{warning}</span>
        </span>
      )}
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

// Check is a box to tick, with its text.
export function Check({
  children,
  ...props
}: { children?: ReactNode } & InputHTMLAttributes<HTMLInputElement>) {
  return (
    <label className="cb">
      <input type="checkbox" {...props} />
      <i />
      {children && <span>{children}</span>}
    </label>
  );
}

// Options is a choice among a few cards. The body of a card shows below it
// while it is chosen, and keeps what was entered in it while it is not.
export function Options<T extends string>({
  name,
  value,
  onChange,
  choices,
}: {
  name: string;
  value: T;
  onChange: (value: T) => void;
  choices: { value: T; title: string; body?: ReactNode }[];
}) {
  return (
    <div className="opts">
      {choices.map((choice) => (
        <Fragment key={choice.value}>
          <label className="opt">
            <input
              type="radio"
              name={name}
              value={choice.value}
              checked={choice.value === value}
              onChange={() => onChange(choice.value)}
            />
            <span className="dot" />
            <span>
              <b>{choice.title}</b>
            </span>
          </label>
          {choice.body && (
            <div className="opt-body" hidden={choice.value !== value}>
              {choice.body}
            </div>
          )}
        </Fragment>
      ))}
    </div>
  );
}

// Secret is a field for a key or a credential, which the browser neither
// shows nor fills in.
export function Secret(props: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className="inp mono"
      type="password"
      autoComplete="new-password"
      spellCheck={false}
      {...props}
    />
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

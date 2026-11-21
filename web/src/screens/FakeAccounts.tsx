import { useEffect, useRef, useState } from "react";
import { type Account, backend } from "../api/fake.ts";
import { initials } from "../lib/format.ts";
import { signIn } from "../session/auth.ts";

// FakeAccounts stands in for the sign-in page of an identity provider during
// development: it lists the accounts of the mock, and signs in the one that
// is chosen.
export default function FakeAccounts({ onClose }: { onClose: () => void }) {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const dialog = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    let wanted = true;
    void backend().then((mock) => {
      if (wanted) setAccounts(mock.accounts());
    });
    return () => {
      wanted = false;
    };
  }, []);

  // The dialog opens once it has its accounts, so that the focus goes to the
  // first of them.
  useEffect(() => {
    if (accounts.length && !dialog.current?.open) dialog.current?.showModal();
  }, [accounts]);

  return (
    <dialog
      ref={dialog}
      className="dlg idp"
      onClose={onClose}
      onPointerDown={(event) => {
        // A press on the backdrop is a press on the dialog itself.
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div className="idp-h">
        <small>Example identity provider</small>
        <h2>Choose an account</h2>
        <p>to continue to Aviary</p>
      </div>
      <div className="idp-l">
        {accounts.map((account) => (
          <button
            key={account.token}
            type="button"
            onClick={() => signIn(account.token)}
          >
            <span className="av grp">{initials(account.display_name)}</span>
            <span>
              <b>{account.display_name}</b>
              <small>
                {account.username} · {account.note}
              </small>
            </span>
          </button>
        ))}
      </div>
      <div className="idp-f">
        The mock’s stand-in for the sign-in page of your organization.
      </div>
    </dialog>
  );
}

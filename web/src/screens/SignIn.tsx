import { lazy, Suspense, useState } from "react";
import { Navigate, useLocation } from "react-router";
import { ApiError } from "../api/client.ts";
import { useMe } from "../api/queries.ts";
import { sentence } from "../lib/format.ts";
import { useTitle } from "../lib/title.ts";
import { begin, signInFailure, signsIn } from "../session/auth.ts";
import { Unreachable, Wait } from "../shell/Signed.tsx";
import { Icon, Use } from "../ui/Icon.tsx";

// While the backend of the mock answers, its accounts sign in.
const FakeAccounts = __FAKE_API__
  ? lazy(() => import("./FakeAccounts.tsx"))
  : null;

function Art() {
  const dome = "M20 400V300A280 280 0 0 1 580 300V400Z";
  return (
    <svg className="si-art" viewBox="0 0 600 400" aria-hidden="true">
      <clipPath id="dclip">
        <path d={dome} />
      </clipPath>
      <path d={dome} fill="url(#glass)" />
      <g clipPath="url(#dclip)" className="beam">
        <path d="M60 400L290 0h64L124 400Z" opacity=".42" />
        <path d="M196 400L426 0h22L218 400Z" opacity=".3" />
      </g>
      <use href="#dome" className="ribs" />
      <g className="trails">
        <path d="M84 322C94 274 116 242 150 224" />
        <path d="M70 200C82 170 100 146 123 131" />
        <path d="M416 356C436 350 450 342 463 331" />
        <path d="M430 268C474 240 504 190 521 126S548 76 571 51" />
      </g>
      <use
        href="#wing"
        x="150"
        y="205"
        width="34"
        height="17"
        className="near"
        transform="rotate(-8 167 213)"
      />
      <use
        href="#wing"
        x="122"
        y="116"
        width="24"
        height="12"
        className="far"
        transform="rotate(-14 134 122)"
      />
      <use
        href="#wing"
        x="462"
        y="318"
        width="22"
        height="11"
        className="far"
        transform="rotate(-20 473 323)"
      />
      <circle cx="520.5" cy="128" r="5.5" className="door" />
      <use
        href="#wing"
        x="566"
        y="20"
        width="44"
        height="22"
        fill="#FFC53D"
        transform="rotate(-24 588 31)"
      />
    </svg>
  );
}

// SignIn is the screen of a console without a caller. Aviary does not sign
// users in: the identity provider of the installation does, and the console
// gets a token from it. The browser goes there and comes back to this
// screen, which goes on to where the caller was going.
export function SignIn() {
  const me = useMe();
  const location = useLocation();
  const [choosing, setChoosing] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const [failure, setFailure] = useState(signInFailure);
  useTitle();
  const back = (location.state as { back?: string } | null)?.back || "/";
  if (me.isPending) return <Wait />;
  if (me.isSuccess) return <Navigate to={back} replace />;
  if (!(me.error instanceof ApiError && me.error.status === 401)) {
    return <Unreachable error={me.error} />;
  }

  function go() {
    if (!signsIn()) return setChoosing(true);
    setLeaving(true);
    begin(back).catch((error: Error) => {
      setLeaving(false);
      setFailure(error.message);
    });
  }

  return (
    <section className="signin">
      <Art />
      <div className="si-card">
        <div className="si-logo" role="img" aria-label="Aviary">
          <Use symbol="wordmark" />
        </div>
        <p className="si-tag">Room to fly. One guarded door.</p>
        {failure && (
          <div className="alert bad" role="alert">
            <Icon name="warn" />
            <span>{sentence(failure)}</span>
          </div>
        )}
        {(FakeAccounts || signsIn()) && (
          <button
            className="btn pri lg wide"
            type="button"
            autoFocus
            disabled={leaving}
            onClick={go}
          >
            Continue with your organization
            <Icon name="arrow" className="lg" />
          </button>
        )}
      </div>
      {FakeAccounts && choosing && (
        <Suspense>
          <FakeAccounts onClose={() => setChoosing(false)} />
        </Suspense>
      )}
    </section>
  );
}

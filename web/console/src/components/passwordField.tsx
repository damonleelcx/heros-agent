"use client";

import { useState } from "react";
import { Eye, EyeOff } from "lucide-react";
import { PASSWORD_COPY } from "@/content/passwordAccount";

/**
 * A password input with a reveal toggle.
 *
 * # Why one component and not a toggle per form
 *
 * There are six password inputs across four files — sign-in, the tenant credential, create-account,
 * reset-password, and the two on the account settings form. Adding the toggle six times is how they end
 * up with six behaviours: one that submits the form because somebody forgot `type="button"`, one that
 * survives a re-render as `text`, one with no accessible name. That drift is the most frequent silent
 * bug in this console's history, and the fix for it is to have one implementation to get right.
 *
 * # Why the caller keeps its own `className`
 *
 * These six live in TWO token vocabularies — the identity screens use the marketing surface
 * (`marketing-ink`, `marketing-canvas`) and the settings form uses the console theme (`border`,
 * `background`, `foreground`). A component that imposed one of them would silently restyle the other,
 * which the ui-consistency rule forbids: a change that adds a control must not repaint what was there.
 * So the input's appearance is passed through untouched and this file adds exactly one thing — the
 * button — positioned over it.
 *
 * The button inherits its colour via `text-current` rather than naming a token, for the same reason: it
 * has to be legible on both surfaces, and picking a colour here would be picking the wrong one on one of
 * them.
 *
 * # 🔴 What it deliberately does not do
 *
 * - **It never starts revealed.** Every mount is hidden, so a back-navigation, a failed submit or a
 *   re-render can never leave a password on screen that the reader last saw masked.
 * - **It never stores, logs or reports the value.** The only state is the boolean.
 * - **`type="button"`.** Inside a form, a button with no explicit type submits it — which here would
 *   mean "reveal my password" also means "sign in", on a half-typed password.
 *
 * # Why reveal is worth having at all
 *
 * The rule these fields enforce is 12 characters and a short sentence, and a passphrase is exactly what
 * you cannot verify by counting dots. Without a reveal, the reader's only way to check a typo is to
 * submit and be told the pair did not match — and on sign-in that message cannot say which half was
 * wrong, by design. The toggle is what makes that message survivable.
 */
export function PasswordField({
  className,
  ...input
}: React.InputHTMLAttributes<HTMLInputElement>) {
  const [revealed, setRevealed] = useState(false);
  const Icon = revealed ? EyeOff : Eye;

  return (
    <div className="relative">
      <input
        {...input}
        // 🔴 The caller's classes, then room for the button. Without the right padding a long
        // passphrase runs underneath it and the reader cannot read the end of what they typed —
        // which is the one thing this control exists to let them do.
        className={`${className ?? ""} pr-11`}
        type={revealed ? "text" : "password"}
      />
      <button
        aria-label={revealed ? PASSWORD_COPY.field.hide : PASSWORD_COPY.field.show}
        aria-pressed={revealed}
        className="absolute inset-y-0 right-0 flex cursor-pointer items-center px-3 text-current opacity-50 transition-opacity hover:opacity-100"
        onClick={() => setRevealed((v) => !v)}
        // Not `tabIndex={-1}`: somebody who navigates by keyboard is the reader most likely to have
        // mistyped, and skipping this in the tab order takes the control away from exactly them.
        type="button"
      >
        <Icon aria-hidden="true" className="size-4" />
      </button>
    </div>
  );
}

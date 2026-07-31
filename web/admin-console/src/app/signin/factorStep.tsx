"use client";

import { useActionState } from "react";
import { completeSignIn } from "./factorAction";

/**
 * FactorStep presents the PLATFORM-verified second factor, after the IdP has returned.
 *
 * # Why TOTP works without JavaScript and WebAuthn cannot
 *
 * The TOTP field is a plain input in a plain form: it posts and works whether or not this component
 * ever hydrated, which matters on the one screen an operator cannot get past. WebAuthn has no
 * no-JavaScript equivalent — `navigator.credentials.get()` is the only way to reach an authenticator —
 * so it is offered as an enhancement beside the field rather than instead of it. An operator whose
 * script failed to load can still sign in.
 *
 * # Why the challenge is fetched at the moment of the attempt
 *
 * It is single-use and two minutes long. Fetching it when the page renders would spend it on a page an
 * operator might read for five minutes, and the failure would look like a broken key rather than an
 * expired challenge.
 */
export function FactorStep() {
  const [state, action, pending] = useActionState(completeSignIn, null);

  async function useSecurityKey(form: HTMLFormElement) {
    const { mfaChallenge } = await import("./factorAction");
    const minted = await mfaChallenge();
    if (!minted) return;
    const b64 = (buffer: ArrayBuffer) =>
      btoa(String.fromCharCode(...new Uint8Array(buffer))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
    const fromB64 = (value: string) =>
      Uint8Array.from(atob(value.replace(/-/g, "+").replace(/_/g, "/")), (c) => c.charCodeAt(0));

    const credential = (await navigator.credentials.get({
      publicKey: {
        challenge: fromB64(minted.challenge),
        // `required`, not `preferred`: on the surface that can halt the fleet, "somebody tapped the
        // key" is not a second factor — "somebody unlocked the key" is. The platform re-checks the
        // user-verified flag regardless; asking for it here is what makes the prompt appear.
        userVerification: "required",
      },
    })) as PublicKeyCredential | null;
    if (!credential) return;
    const response = credential.response as AuthenticatorAssertionResponse;
    const set = (name: string, value: string) => {
      const field = form.elements.namedItem(name) as HTMLInputElement;
      field.value = value;
    };
    set("challenge_id", minted.challenge_id);
    set("credential_id", b64(credential.rawId));
    set("authenticator_data", b64(response.authenticatorData));
    set("client_data_json", b64(response.clientDataJSON));
    set("signature", b64(response.signature));
    form.requestSubmit();
  }

  return (
    <form className="action" action={action}>
      <fieldset disabled={pending}>
        <legend>Second factor</legend>
        <p className="hint">
          Your identity provider has confirmed who you are. This platform verifies your second factor
          itself &mdash; a factor your provider considers satisfied is not sufficient on the surface
          that can halt the fleet.
        </p>

        <label htmlFor="totp">Authenticator code</label>
        <input
          id="totp"
          name="totp"
          type="text"
          inputMode="numeric"
          autoComplete="one-time-code"
          pattern="[0-9]{6}"
          placeholder="123456"
        />

        <input type="hidden" name="challenge_id" />
        <input type="hidden" name="credential_id" />
        <input type="hidden" name="authenticator_data" />
        <input type="hidden" name="client_data_json" />
        <input type="hidden" name="signature" />

        <button type="submit" className="primary" disabled={pending}>
          {pending ? "Verifying…" : "Continue"}
        </button>
        <button
          type="button"
          onClick={(event) => useSecurityKey(event.currentTarget.form as HTMLFormElement)}
          disabled={pending}
        >
          Use a security key instead
        </button>
      </fieldset>
      {state && !state.ok ? (
        <div className="state state--denied" role="alert">
          <p className="state__title">Not accepted</p>
          <p className="state__body">{state.message}</p>
        </div>
      ) : null}
    </form>
  );
}

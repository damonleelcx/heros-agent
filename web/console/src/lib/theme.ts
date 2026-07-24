import { cookies } from "next/headers";

/**
 * theme.ts resolves the reader's theme SERVER-SIDE (R17 / FR37, task 5b.5).
 *
 * # Why the server and not the browser
 *
 * FR37 requires the **first paint** to already be in the chosen theme. A preference read in the
 * browser can only be applied after the browser has painted something, so the reader watches the
 * page change colour — the "theme flash" the requirement exists to forbid. Worse, the usual fix is a
 * blocking inline `<script>` in `<head>`, which this console cannot have: its CSP is
 * `default-src 'self'` with no `unsafe-inline`, and relaxing it to buy a theme toggle would trade a
 * security property for a cosmetic one.
 *
 * Reading a cookie in the layout costs nothing, needs no script, and is correct on the first byte.
 *
 * # Why "system" is a stored value rather than the absence of one
 *
 * `system` and "never chose" render identically today, but they are different facts and a reader who
 * deliberately chose *follow my OS* should not be silently re-decided for if the default ever
 * changes. It is also the same reason the console models an unknown status rather than letting it
 * fall through: an absent value and a chosen value are not the same thing.
 */

export const THEME_COOKIE = "heros_console_theme";

export const THEMES = ["system", "dark", "light"] as const;
export type Theme = (typeof THEMES)[number];

/** THEME_LABEL is the word each setting is offered under. `Intl` is not involved — these are UI copy. */
export const THEME_LABEL: Record<Theme, string> = {
  system: "System",
  dark: "Dark",
  light: "Light",
};

export function isTheme(value: string | undefined | null): value is Theme {
  return typeof value === "string" && (THEMES as readonly string[]).includes(value);
}

/**
 * readTheme returns the persisted choice, defaulting to `system`.
 *
 * An unrecognised cookie value falls back to `system` rather than throwing: a stale or hand-edited
 * cookie should not be able to make a page fail to render, and `system` is the setting that defers to
 * the reader anyway.
 */
export async function readTheme(): Promise<Theme> {
  const store = await cookies();
  const raw = store.get(THEME_COOKIE)?.value;
  return isTheme(raw) ? raw : "system";
}

/**
 * THEME_COOKIE_OPTIONS — deliberately NOT `httpOnly`.
 *
 * The session cookie is httpOnly because it is a credential. This one is a display preference: it
 * carries no identity, authorises nothing, and is scoped to presentation. Marking it httpOnly would
 * imply it is sensitive, and a flag that means nothing is a flag that stops meaning anything.
 *
 * `sameSite: lax` rather than `strict` so a theme survives arriving from an external link — a reader
 * who followed a shared console URL should not land in the wrong theme.
 */
export const THEME_COOKIE_OPTIONS = {
  httpOnly: false,
  sameSite: "lax",
  secure: process.env.NODE_ENV === "production",
  path: "/",
  maxAge: 60 * 60 * 24 * 365,
} as const;

import { headers } from "next/headers";
import { Monitor, Moon, Sun } from "lucide-react";
import { readTheme, THEME_LABEL, THEMES, type Theme } from "@/lib/theme";

/**
 * ThemeControl is the reader's theme choice (R17 / FR37, task 5b.5).
 *
 * # A form, deliberately
 *
 * Three submit buttons in one form, posting to `/api/theme`, which sets a cookie and redirects back.
 * No client component, no state, no script — see `lib/theme.ts` for why the first paint has to be
 * correct and why an inline script is not an option under this console's CSP.
 *
 * # `aria-pressed` and a WORD, not an icon alone
 *
 * The active option carries a surface, a hue AND `aria-pressed="true"`. That is the same rule the
 * console applies to every status: never colour alone. The icons are decoration on top of a
 * screen-reader-visible label, so the control is operable without seeing them.
 *
 * # Why "System" is offered rather than assumed
 *
 * A reader who wants their OS honoured should be able to say so and see it reflected, not merely
 * benefit from a default they cannot see. It is also the setting that makes the control honest: with
 * only Dark and Light, choosing neither is unrepresentable.
 */
const ICON: Record<Theme, React.ReactNode> = {
  system: <Monitor className="size-3.5" aria-hidden="true" />,
  dark: <Moon className="size-3.5" aria-hidden="true" />,
  light: <Sun className="size-3.5" aria-hidden="true" />,
};

export async function ThemeControl() {
  const current = await readTheme();

  // Where to return to. Carried in the form rather than read from `Referer` on the server, because
  // this console sends `Referrer-Policy: no-referrer` — a Referer-based redirect silently returns the
  // reader to `/`, which is what it did until a browser showed it doing so.
  const back = (await headers()).get("x-pathname") ?? "/";

  return (
    <form method="post" action="/api/theme">
      <input type="hidden" name="back" value={back} />
      <span className="visually-hidden" id="theme-control-label">
        Colour theme
      </span>
      <div
        role="group"
        aria-labelledby="theme-control-label"
        className="flex items-center gap-0.5 rounded-lg border border-border bg-muted/20 p-0.5"
      >
        {THEMES.map((theme) => (
          <button
            key={theme}
            type="submit"
            name="theme"
            value={theme}
            className="cursor-pointer rounded-md p-1.5 text-muted-foreground transition-colors hover:text-foreground aria-pressed:bg-primary/12 aria-pressed:text-primary"
            aria-pressed={theme === current}
          >
            {ICON[theme]}
            <span className="visually-hidden">{THEME_LABEL[theme]}</span>
          </button>
        ))}
      </div>
    </form>
  );
}

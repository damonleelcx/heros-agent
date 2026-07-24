/**
 * postcss.config.mjs — Tailwind v4's only build wiring.
 *
 * The design system this console is built from (`Component library design`) expresses its whole
 * language as Tailwind v4 theme variables, so the console consumes it the same way rather than
 * transcribing it into hand-written CSS a second time. Transcription is exactly how the design
 * language in this repository forked three ways before.
 *
 * There is no `tailwind.config.js`: v4 takes its theme from CSS (`@theme` in
 * `src/app/tokens.customer.css`), which keeps the token layer the single place a literal may appear
 * and keeps `scripts/scan-tokens.mjs` able to enforce that.
 */
const config = {
  plugins: {
    "@tailwindcss/postcss": {},
  },
};

export default config;

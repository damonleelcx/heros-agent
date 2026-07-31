# Craft acceptance matrix — P8 operator console (task 14.7, FR37)

> **The rule.** Acceptance for a user-visible console behaviour is **rendered-browser evidence against a
> real API response**, and it must cover the matrix below. **A cell without evidence blocks acceptance**
> — it is not assumed to pass.
>
> **Stack under test.** `cmd/proof/operatorconsole` (real admin API, real RBAC/audit/kill-switch machinery, test-mode
> IdP) on `127.0.0.1:4311`, console BFF on `localhost:4310`. Browsers: Chrome (real profile) and the
> in-app Chrome pane. Date: 2026-07-24.

---

## 1. The matrix

| # | Cell | Evidence | Result |
|---|---|---|---|
| 1 | **Dark · wide (1728×938) · comfortable · populated** | Overview, Tenants, Kill switch, Audit, Jobs & fleet, tenant detail | ✅ |
| 2 | **Dark · wide · compact · populated** | Tenants — all 5 rows and all 5 columns present at both densities | ✅ |
| 3 | **Light · wide (1440×900) · comfortable · populated** | Sign-in, Tenants | ✅ |
| 4 | **Light · wide · compact · populated** | Jobs & fleet (as Support) | ✅ |
| 5 | **Light · narrow (390×844)** | Tenants — chrome and nav wrap, no horizontal overflow, prose re-measures | ✅ |
| 6 | **Dark · 200 % zoom equivalent (720×450 CSS px)** | Overview — nav wraps, no horizontal scroll, `HALTED` pill still legible | ✅ |
| 7 | **`prefers-reduced-motion`** | Runtime probe: **22** animated/transitioning elements → **0** with the reduced-motion rule applied; `document.body.innerText` **identical**; **0** elements invisible without motion | ✅ |
| 8 | **State: populated** | every view above | ✅ |
| 9 | **State: empty** | Overview (no impersonations, no anomalies), Kill switch (no armed scopes) — each says *"a real, current answer — not a failure to load"* | ✅ |
| 10 | **State: denied** | Support → `/killswitch`: *"held by Platform-SRE or Superadmin"* + capability name; nav shows only the 3 granted surfaces; palette search for "kill" returns *"The palette only offers what your role grants"* | ✅ |
| 11 | **State: degraded** | Admin API stopped → `/killswitch` renders the degraded boundary naming the transport failure and stating the P6 kill switch is still armable independently | ✅ |
| 12 | **State: outcome unknown** | API stopped mid-command → *"Outcome unknown — do not retry yet"* with a link to the audit log, visually distinct from both receipt and failure | ✅ |

## 2. Behaviours verified live (not inferred from source)

| Behaviour | Evidence |
|---|---|
| Palette opens on ⌘K from any view, focus in input | Overview and Kill switch, both roles |
| Palette lists **only** granted capabilities | Support: "kill" → no entries, explicit message |
| Palette **navigates**, never performs | "Arm the global kill switch" → `/killswitch#global-kill-switch` with reason **empty**, confirmation **unchecked**, nothing armed |
| Receipt names a **reachable** audit entry | Arm per-tenant → receipt "entry 5 (write-ahead 4)" → link resolves to seq 5 with matching actor/target/reason |
| URL-addressable views | `/audit?action=p6.autonomous.merge` reproduces the filtered view with fields populated and a `FILTERED` marker; unfiltered total stays visible |
| Density persists and hides nothing | Toggle → all rows/columns present at both densities, across navigations |
| Operating picture reflects real state | Arming `tenant-castle` → Overview shows `HALTED`, Tenants shows `1 HALTED` and the halt reason |
| Live figures carry an as-of time | `As of Jul 24, 2026, 06:25:20 AM UTC` on the Overview |

## 3. Defects found by walking the matrix (all fixed)

1. **The console never hydrated in development.** The strict CSP had no `'unsafe-eval'`, which React
   Refresh needs; the page served correct HTML and every form was inert. Invisible to `next build`,
   `tsc` and the unit tests — it took a real browser. *(This also explains the `Origin: null` symptom:
   without hydration the sign-in form fell back to a native POST.)* Fixed dev-only; the production CSP
   is unchanged and the asymmetry is asserted.
2. **An unreachable platform crashed every page.** `requireIdentity` throws on a transport failure and
   no route boundary existed, so the operator got a framework stack trace — no chrome, no statement of
   what failed — at exactly the moment they are trying to find out what is wrong. Fixed with
   `app/error.tsx`, which renders the degraded state in the operator's own chrome.
3. **The sign-in page kept the old chrome classes** after the refactor; caught by the test asserting
   every class a view uses is defined in the stylesheet.
4. **A "Clear" link stretched across its filter row**, reading as an input. Caught in the light-compact
   cell.
5. **The visual baseline recorded a stale render once** (a page mid-recompile), which is precisely the
   *"a baseline updated without looking records the regression"* failure its own message warns about.
   Re-baselined against a verified render.

## 4. What this matrix does **not** cover

- **Pixel-level regressions.** The baseline (`npm run baseline`) is structural: primitives, order,
  vocabulary, labels. Sub-pixel alignment and contrast drift *within* a token are not covered by any
  automated gate here — they are covered by this matrix being re-walked, by a human, per release.
- **Assistive-technology walkthrough.** The floor tests assert scoped headers, labels, focus
  visibility, tabular fallbacks and `lang`; an actual screen-reader pass is the keyboard-only pass
  recorded under task 12.14 and is not re-performed here.
- **Print and forced-colours modes.** Out of scope for this change; no requirement asks for them.

## 5. Re-running it

```bash
GOWORK=off go run ./cmd/proof/operatorconsole                     # admin API on :4311
cd web/admin-console && ADMIN_API_BASE=http://127.0.0.1:4311 \
  ADMIN_PLATFORM_CREDENTIAL=<printed by p8hermes> npm run dev
ADMIN_CONSOLE_URL=http://localhost:4310 npm test     # 35 assertions, incl. the live half
ADMIN_CONSOLE_URL=http://localhost:4310 ADMIN_SESSION=<token> npm run baseline
```

Then walk §1 in a browser. Fixture principals: `sso|support`, `sso|billing_ops`, `sso|platform_sre`,
`sso|superadmin` — cell 10 needs at least two of them.

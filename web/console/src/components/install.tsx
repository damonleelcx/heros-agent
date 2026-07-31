import { Check, CircleSlash, Clock, HardDriveDownload, ShieldCheck, ShieldAlert, Terminal } from "lucide-react";
import { Card, Chip, Row, Section, Banner } from "@/components/primitives";
import type { ChannelView, InstallView, TargetView } from "@/lib/types.generated";

/**
 * install.tsx renders the distribution contract: how to install, on what, and what the release actually proved
 * about itself (P20 tasks 6.1, 8.1–8.3).
 *
 * # The distinction this surface exists to make visible
 *
 * There are THREE reasons a reader cannot install something, and they are not variations of one another:
 *
 *   available            → they run a command.
 *   generated, unpublished → nobody can install it; the manifest exists but no index points at it. Waiting is
 *                            the action, and what is being waited for is NAMED.
 *   platform not built    → there is no artifact at all, and the answer is a different channel entirely.
 *
 * Rendering those as one greyed-out row is the failure this surface is built to end — the same lesson the
 * coverage surface learned. A greyed control says "not for you" and stops the reader; each of these says
 * something different about whose move it is.
 *
 * # Why nothing here is computed
 *
 * Every string comes from `distribution` through the BFF and is rendered as received: which channels are
 * delivered, which platforms are built, what each blocker is, and which trust claims were earned. A console
 * that recomputed any of it would be the second source of truth the whole contract exists to prevent — and it
 * would drift in the optimistic direction, because the optimistic copy is the one nobody double-checks.
 */

/** CommandBlock renders a pasteable command. Selectable text, no clipboard scripting: a copy button that
 * silently fails on a page served over a context the Clipboard API refuses is worse than a command a reader
 * can select, because it looks like it worked. */
export function CommandBlock({ children, label }: { children: string; label?: string }) {
  return (
    <div className="flex flex-col gap-1">
      {label ? (
        <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground">{label}</p>
      ) : null}
      <pre className="overflow-x-auto rounded-lg border border-border bg-muted/40 p-3 font-mono text-xs leading-relaxed text-foreground">
        <code>{children}</code>
      </pre>
    </div>
  );
}

/**
 * ChannelCard is one install channel a reader can use today.
 *
 * It shows the install command first and the verification sentence directly under it, because those two
 * together are the whole decision: what to run, and what running it checks on your behalf. The upgrade,
 * uninstall and pin idioms follow, since a reader evaluating a tool for a team asks how to get rid of it and
 * how to pin a version before they ask anything else (task 3.8).
 */
export function ChannelCard({ channel }: { channel: ChannelView }) {
  return (
    <Card>
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h3 className="flex items-center gap-2 font-display text-sm font-normal text-foreground">
            <Terminal className="size-3.5 text-primary" aria-hidden="true" />
            {channel.label}
          </h3>
          <Row>
            {(channel.oses ?? []).map((os) => (
              <Chip key={os}>{os}</Chip>
            ))}
            <Chip tone="ok">
              <Check className="size-2.5" aria-hidden="true" />
              available
            </Chip>
          </Row>
        </div>

        <CommandBlock>{channel.install}</CommandBlock>

        <p className="flex gap-2 text-xs leading-relaxed text-muted-foreground">
          <ShieldCheck className="mt-0.5 size-3.5 shrink-0 text-primary" aria-hidden="true" />
          <span>{channel.verification}</span>
        </p>

        <dl className="grid gap-2 border-t border-border/60 pt-3 text-xs sm:grid-cols-3">
          {[
            { term: "upgrade", value: channel.upgrade },
            { term: "uninstall", value: channel.uninstall },
            { term: "pin a version", value: channel.pin },
          ].map((row) => (
            <div className="flex min-w-0 flex-col gap-1" key={row.term}>
              <dt className="font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground">
                {row.term}
              </dt>
              <dd className="break-words font-mono text-[11px] leading-snug text-foreground">{row.value}</dd>
            </div>
          ))}
        </dl>

        {channel.manager_owned ? (
          <p className="text-xs leading-relaxed text-muted-foreground">
            This channel&rsquo;s package manager owns the installed file, so <code className="mono">heros upgrade</code>{" "}
            defers to it rather than replacing the binary — overwriting a manager-owned file corrupts that
            manager&rsquo;s state, and its next upgrade would silently revert you.
          </p>
        ) : null}
      </div>
    </Card>
  );
}

/**
 * PendingChannelRow is a channel whose manifest is generated but which nobody can install from yet.
 *
 * 🔴 It is deliberately NOT a greyed-out ChannelCard. A dimmed copy of an available thing reads as "disabled
 * for your plan", and this is not that: the artifact is correct and attached to every release, and what is
 * missing is an index to publish it to. So the row states the blocker in full and shows no install command at
 * all — a command a reader cannot run is worse than no command, because they will run it.
 */
export function PendingChannelRow({ channel }: { channel: ChannelView }) {
  const pendingUpstream = channel.publication === "pending-upstream-pr";
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-dashed border-border bg-transparent p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="flex items-center gap-2 font-display text-sm font-normal text-foreground">
          <Clock className="size-3.5 text-muted-foreground" aria-hidden="true" />
          {channel.label}
        </h3>
        <Row>
          {(channel.oses ?? []).map((os) => (
            <Chip key={os}>{os}</Chip>
          ))}
          <Chip>{pendingUpstream ? "waiting on an upstream review" : "waiting on a publishing repository"}</Chip>
        </Row>
      </div>
      <p className="text-xs leading-relaxed text-muted-foreground">{channel.blocker}</p>
      <p className="text-xs leading-relaxed text-muted-foreground">
        No command is shown because none of them would work yet. When it lands, this row becomes an install
        card, and the release notes name the version it first worked in.
      </p>
    </div>
  );
}

/**
 * TargetTable is the TOTAL supported-platform matrix — every row, including the ones nothing builds.
 *
 * The unbuilt rows are present for the reason the coverage surface exists: a reader who finds no row for their
 * platform does not conclude "not built", they conclude the download is broken and open a ticket. A row that
 * says what is true and what to do instead costs them a minute instead of a day.
 */
export function TargetTable({ targets }: { targets: readonly TargetView[] }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[42rem] border-collapse text-xs">
        <thead>
          <tr className="border-b border-border text-left">
            <th className="pb-2 pr-3 font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground">
              platform
            </th>
            <th className="pb-2 pr-3 font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground">
              arch
            </th>
            <th className="pb-2 pr-3 font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground">
              native binary
            </th>
            <th className="pb-2 pr-3 font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground">
              built on
            </th>
            <th className="pb-2 font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground">
              how to install it
            </th>
          </tr>
        </thead>
        <tbody>
          {targets.map((t) => {
            const shipped = t.support === "shipped";
            return (
              <tr className="border-b border-border/50 align-top" key={t.key}>
                <td className="py-2.5 pr-3 text-foreground">{t.platform}</td>
                <td className="py-2.5 pr-3 font-mono text-[11px] text-muted-foreground">{t.arch}</td>
                <td className="py-2.5 pr-3">
                  {shipped ? (
                    <Chip tone="ok">
                      <Check className="size-2.5" aria-hidden="true" />
                      shipped
                    </Chip>
                  ) : (
                    <Chip tone="bad">
                      <CircleSlash className="size-2.5" aria-hidden="true" />
                      not built
                    </Chip>
                  )}
                </td>
                <td className="py-2.5 pr-3 font-mono text-[11px] text-muted-foreground">
                  {t.runner ? t.runner : "—"}
                </td>
                <td className="py-2.5 text-muted-foreground">
                  {shipped ? (
                    <Row>
                      {(t.channels ?? []).map((c) => (
                        <Chip key={c}>{c}</Chip>
                      ))}
                    </Row>
                  ) : (
                    <div className="flex max-w-md flex-col gap-1">
                      <span className="leading-relaxed">{t.limit}</span>
                      <span className="leading-relaxed text-foreground">Instead: {t.answer}</span>
                    </div>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

/**
 * TrustPosture renders the two things that must never be confused: what was DECIDED, and what a release
 * actually DELIVERED.
 *
 * When no release's attestation is known, this renders the absence — not the decision dressed up as a
 * property. The day the budget for code signing was approved is not the day any download became notarized,
 * and a surface that blurred the two would announce the second while only the first had happened.
 */
export function TrustPosture({ view }: { view: InstallView }) {
  const delivered = view.delivered;
  const postureLabel =
    view.ratified_posture === "sign-notarize"
      ? "Developer-ID sign + notarize on macOS, Authenticode on Windows"
      : "ship unsigned, with the one-command clear documented";

  return (
    <div className="flex flex-col gap-4">
      <Banner tone="info" title="The decision and the delivery are two different facts">
        <p>
          <strong className="font-medium text-foreground">Ratified posture:</strong> {postureLabel}. That is a
          budget and an organizational identity, decided once.
        </p>
        <p>
          What any single download actually carries is recorded per release, from the outcome of the signing
          steps that ran — never from the decision. Everything below comes from that record.
        </p>
      </Banner>

      {!delivered ? (
        <Banner tone="warn" title="No published release is known to this deployment">
          <p>
            The trust posture of a release is read from that release&rsquo;s own attestation, and this
            deployment has not been given one. Rather than showing the ratified posture here — which would
            describe an intention as though it were a property of a file you downloaded — the surface says so.
          </p>
          <p>
            Verify any release yourself with the two documented commands: the checksums, then the signature
            over the manifest. Both run offline, with no account.
          </p>
        </Banner>
      ) : (
        <Card>
          <div className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h3 className="font-display text-sm font-normal text-foreground">
                What release {delivered.version} delivered
              </h3>
              {delivered.signing_key_id ? (
                <Chip variant="hash" title="the release key that signed the checksum manifest">
                  {delivered.signing_key_id}
                </Chip>
              ) : null}
            </div>
            <ul className="flex flex-col gap-2.5">
              {(delivered.claims ?? []).map((claim) => (
                <li className="flex gap-2 text-xs leading-relaxed" key={claim.id}>
                  {claim.earned ? (
                    <ShieldCheck className="mt-0.5 size-3.5 shrink-0 text-primary" aria-hidden="true" />
                  ) : (
                    <ShieldAlert className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                  )}
                  <span className={claim.earned ? "text-foreground" : "text-muted-foreground"}>{claim.text}</span>
                </li>
              ))}
            </ul>
          </div>
        </Card>
      )}
    </div>
  );
}

/** VerifyItYourself is the offline verification runbook, stated on the surface rather than linked away from it:
 * a reader who has to leave the page to find out whether they can check the download usually does not. */
export function VerifyItYourself() {
  return (
    <Section title="Verify a download yourself — offline, no account">
      <p className="text-xs leading-relaxed text-muted-foreground">
        Every channel above performs both steps for you and <strong className="text-foreground">refuses to
        place the binary on your PATH</strong> if either fails. To run them by hand:
      </p>
      <CommandBlock label="1 · the download is intact">
        {"sha256sum -c SHA256SUMS        # or: shasum -a 256 -c SHA256SUMS"}
      </CommandBlock>
      <CommandBlock label="2 · the manifest came from the holder of the heros release key">
        {"ssh-keygen -Y verify -f allowed_signers -I heros-release \\\n  -n file -s SHA256SUMS.sshsig < SHA256SUMS"}
      </CommandBlock>
      <p className="text-xs leading-relaxed text-muted-foreground">
        <code className="mono">ssh-keygen</code> is used rather than <code className="mono">openssl</code>{" "}
        because stock macOS ships LibreSSL, which cannot verify ed25519 at all — the same signature is published
        in both encodings so the check works on a machine with nothing installed. Neither step needs a network.
      </p>
    </Section>
  );
}

/** FreeAndPaid states the boundary where an evaluating engineer will actually read it (task 8.3). */
export function FreeAndPaid() {
  return (
    <Section title="What is free, and what is not">
      <div className="grid gap-3 md:grid-cols-2">
        <Card>
          <div className="flex flex-col gap-2">
            <h3 className="flex items-center gap-2 font-display text-sm font-normal text-foreground">
              <HardDriveDownload className="size-3.5 text-primary" aria-hidden="true" />
              The CLI — free, no account, forever
            </h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              <code className="mono">discover</code>, <code className="mono">apply</code>,{" "}
              <code className="mono">eval</code>, <code className="mono">coverage</code>,{" "}
              <code className="mono">doctor</code>, <code className="mono">init</code>,{" "}
              <code className="mono">version</code> and <code className="mono">upgrade</code> all run locally.
              No telemetry is sent, and no local command starts requiring an account later.
            </p>
          </div>
        </Card>
        <Card>
          <div className="flex flex-col gap-2">
            <h3 className="font-display text-sm font-normal text-foreground">The hosted platform — the paid upgrade</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              <code className="mono">heros login</code> and <code className="mono">heros link</code> push a
              run&rsquo;s allowlisted metrics to a tenant, which is what buys this console: leaderboards across
              runs, attribution scorecards, autonomous proposals and pull requests, and team-wide history.
              Nothing in the free path is degraded to sell it.
            </p>
          </div>
        </Card>
      </div>
    </Section>
  );
}

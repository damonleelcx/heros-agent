import { requireSession } from "@/lib/session";
import { PageFrame, Section, Chip, Row, Banner } from "@/components/primitives";
import { Tabs, type TabItem } from "@/components/tabs";
import {
  ChannelCard,
  PendingChannelRow,
  TargetTable,
  TrustPosture,
  VerifyItYourself,
  FreeAndPaid,
} from "@/components/install";
import { fetchInstall } from "./data";

/**
 * The install surface (P20) — "how do I get this, on what, and can I trust it".
 *
 * # Why a console page for something a README could hold
 *
 * Because a README cannot be checked against the code, and this particular README drifts in a specific
 * direction: toward listing channels that exist as build artifacts but that nobody can actually install from.
 * The page renders the same frozen contract the release pipeline enforces, so a reader here and a reader running
 * `heros doctor` cannot be told different things — and when a channel stops being installable, this page stops
 * showing its command, without anybody editing prose.
 *
 * # Why three tabs and not one long page
 *
 * The three questions have different readers. Someone installing wants one command. Someone deciding whether
 * their fleet is covered wants the platform matrix. Someone in a security review wants the trust posture and the
 * offline verification steps. Stacking all three makes each of them scroll past the other two.
 */
export const dynamic = "force-dynamic";

export default async function InstallPage() {
  const session = await requireSession();
  const view = await fetchInstall(session.tenantId);

  if (!view) {
    return (
      <PageFrame eyebrow="Install" title="Get the CLI" wide>
        <Banner tone="warn" title="The distribution contract is unavailable">
          This page states which install channels work and what each one verifies on your behalf. Showing a
          partial answer would be worse than showing none — a stale install command sends a reader to a URL that
          404s, or worse, to one that no longer verifies anything.
        </Banner>
      </PageFrame>
    );
  }

  const channels = view.channels ?? [];
  const targets = view.targets ?? [];
  const available = channels.filter((c) => c.delivered);
  const pending = channels.filter((c) => !c.delivered);
  const shipped = targets.filter((t) => t.support === "shipped");

  const tabs: TabItem[] = [
    {
      id: "install",
      label: "Install",
      content: (
        <>
          <Section title={`${available.length} channels you can use now`}>
            <div className="flex flex-col gap-3">
              {available.map((c) => (
                <ChannelCard channel={c} key={c.id} />
              ))}
            </div>
          </Section>
          {pending.length > 0 ? (
            <Section title={`${pending.length} generated, not yet publishable`}>
              <p className="text-xs leading-relaxed text-muted-foreground">
                These manifests are generated from each release&rsquo;s signed checksum manifest and attached to
                it, so they are correct — but no index a package manager reads points at them yet. They are
                listed rather than hidden because an absent channel reads as <em>not supported</em>, and that is
                not what is true here.
              </p>
              <div className="flex flex-col gap-2">
                {pending.map((c) => (
                  <PendingChannelRow channel={c} key={c.id} />
                ))}
              </div>
            </Section>
          ) : null}
          <FreeAndPaid />
        </>
      ),
    },
    {
      id: "platforms",
      label: "Platforms",
      content: (
        <Section title="Every platform, including the ones nothing builds" aside={<span className="mono">{view.matrix_version}</span>}>
          <TargetTable targets={targets} />
          <p className="text-xs leading-relaxed text-muted-foreground">
            The unbuilt rows are rows, not blanks. A reader who finds no row for their platform concludes the
            download is broken and opens a ticket; a row that says what is true and what to do instead costs them
            a minute. Each shipped row names the native runner that builds it — there is no cross-compiled
            target, because a cross-built binary linking the tree-sitter C frontends is a different, less-tested
            artifact than the native one.
          </p>
        </Section>
      ),
    },
    {
      id: "trust",
      label: "Trust",
      content: (
        <>
          <Section title="OS trust posture">
            <TrustPosture view={view} />
          </Section>
          <VerifyItYourself />
        </>
      ),
    },
  ];

  return (
    <PageFrame
      eyebrow="Install"
      title="Get the CLI"
      wide
      lede={
        <>
          One self-contained binary, free with no account. Every channel verifies the checksum and the release
          signature before placing anything on your PATH, and refuses outright if either fails.
        </>
      }
      actions={
        <Row>
          <Chip tone="ok">{available.length} channels available</Chip>
          <Chip>{shipped.length} platforms built</Chip>
          <Chip variant="hash">{view.documented_release}</Chip>
        </Row>
      }
    >
      <Tabs tabs={tabs} />
    </PageFrame>
  );
}

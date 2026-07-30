import { PageFrame, Section, Chip, Row } from "@/components/primitives";
import { Tabs } from "@/components/tabs";
import {
  ChannelCard,
  PendingChannelRow,
  TargetTable,
  TrustPosture,
  VerifyItYourself,
  FreeAndPaid,
} from "@/components/install";
import { PREVIEW_INSTALL, PREVIEW_INSTALL_SIGNED } from "./fixture";

/**
 * A self-contained preview of the install surface, seeded with the engine's own distribution contract so the
 * presentation can be checked in a browser without a live platform backend. It uses only the root layout (no
 * session, no BFF), which is why it lives outside /app.
 *
 * # What has to be SEEN here rather than asserted
 *
 * Three states must be distinguishable at a glance, and no test can establish that:
 *
 *   an available channel      → a card with a command
 *   a generated-but-unpublished channel → a dashed row with a blocker and NO command
 *   an unbuilt platform       → a matrix row that says what to use instead
 *
 * The whole surface exists because those three were being rendered as one greyed-out control. The Trust tab
 * shows both postures side by side for the same reason: whether a reader can tell "notarized" from "not
 * notarized" in one look is the property the D3-(A) spend is buying.
 */
export const dynamic = "force-dynamic";

export default function InstallPreview() {
  const view = PREVIEW_INSTALL;
  const channels = view.channels ?? [];
  const available = channels.filter((c) => c.delivered);
  const pending = channels.filter((c) => !c.delivered);
  const targets = view.targets ?? [];

  return (
    <PageFrame
      eyebrow="Preview · install"
      title="Get the CLI"
      wide
      lede="One self-contained binary, free with no account. Every channel verifies the checksum and the release signature before placing anything on your PATH, and refuses outright if either fails."
      actions={
        <Row>
          <Chip tone="ok">{available.length} channels available</Chip>
          <Chip>{targets.filter((t) => t.support === "shipped").length} platforms built</Chip>
          <Chip variant="hash">{view.documented_release}</Chip>
        </Row>
      }
    >
      <Tabs
        tabs={[
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
                <Section title={`${pending.length} generated, not yet publishable`}>
                  <div className="flex flex-col gap-2">
                    {pending.map((c) => (
                      <PendingChannelRow channel={c} key={c.id} />
                    ))}
                  </div>
                </Section>
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
              </Section>
            ),
          },
          {
            id: "trust",
            label: "Trust",
            content: (
              <>
                <Section title="No release published yet — the honest rendering">
                  <TrustPosture view={view} />
                </Section>
                <Section title="A release that delivered its posture — for comparison">
                  <TrustPosture view={PREVIEW_INSTALL_SIGNED} />
                </Section>
                <VerifyItYourself />
              </>
            ),
          },
        ]}
      />
    </PageFrame>
  );
}

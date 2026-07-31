import { sessionOnly } from "@/lib/view";
import { PageFrame } from "@/components/primitives";
import { Tabs } from "@/components/tabs";
import { Configurator } from "./configurator";
import { SkillToolSelection } from "./selection";

export const dynamic = "force-dynamic";

export default async function ConfigurePage() {
  await sessionOnly();
  return (
    <PageFrame
      eyebrow="Configure"
      title="Change one call site"
      lede="Override the model, prompt, skills or context policy for a single node. Everything you leave out is not a missing value — it means that call site is unchanged. The change runs in a sandbox; nothing touches production."
      wide
    >
      <Tabs
        tabs={[
          { id: "override", label: "Override a node", content: <Configurator /> },
          { id: "selection", label: "Skills & tools", content: <SkillToolSelection /> },
        ]}
      />
    </PageFrame>
  );
}

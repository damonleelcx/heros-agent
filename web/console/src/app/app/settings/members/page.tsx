import { load } from "@/lib/view";
import { platformFetch } from "@/lib/platformApi";
import { PageFrame, Section, Empty, Failure, DataTable, Chip, Stat, Stats, Banner } from "@/components/primitives";
import {
  MEMBER_COPY,
  SEATS_COPY,
  CREDENTIAL_COPY,
  ROLE_COPY,
  controlsFor,
  type MemberState,
  type ViewerRole,
} from "@/lib/organizationCopy";
import { MemberActions, InviteForm, CredentialActions } from "@/components/members";
import { instant } from "@/lib/format";

/**
 * The members surface: who is in this organization, who has been invited, and which keys exist.
 *
 * # Why three things on one page rather than three pages
 *
 * They are one question asked three ways — *who can reach this organization's data?* A person, a pending
 * person, and a key are the three answers, and separating them lets somebody audit two of the three and
 * believe they are done. The removal flow makes the point concrete: removing a member ends their
 * sessions and their personal keys and leaves the organization's machine keys running, so the list of
 * machine keys has to be on the screen where somebody removes people.
 *
 * # 🔴 Every seat number on this page is labelled
 *
 * "Seats" is two different numbers — the count that gates the next invitation and the peak that prices
 * the invoice — and they move in opposite directions on the same day. A reader given one unlabelled
 * cannot tell which they got. `SEATS_COPY` has no string that says only "seats", and this page renders
 * nothing it did not get from the platform: no count is derived in the browser.
 *
 * # Six member states, six sets of words
 *
 * `MEMBER_COPY` is the table. `invited` and `expired` are separate rows because "waiting for them" and
 * "send it again" are different next actions, and showing the first for the second is how an invitation
 * sits dead in an inbox for a fortnight.
 */
export const dynamic = "force-dynamic";

type OrganizationView = {
  id: string;
  name: string;
  status: string;
  created_at: string;
  seats_current: number;
  seats_allowed?: number;
  seats_unlimited?: boolean;
  /** The VIEWER's role. Absent for a machine credential, which has no role in an organization. */
  your_role?: string;
};

type MemberRow = { user_id: string; email?: string; role: string; status: string; joined_at: string };
type InvitationRow = {
  invitation_id: string;
  email: string;
  role: string;
  state: "pending" | "accepted" | "expired" | "revoked";
  created_at: string;
  expires_at: string;
};
export type CredentialRow = {
  credential_id: string;
  label: string;
  kind: "personal" | "machine";
  role: string;
  created_at: string;
  revoked: boolean;
  user_id?: string;
};

export default async function MembersPage() {
  const { outcome, session } = await load<OrganizationView>((paths) => paths.organization());

  // The three collections are fetched SEPARATELY and each failure is its own. A tenant whose credential
  // surface refuses still gets their member list — and the section that could not be read says so,
  // rather than the whole page rendering a failure for a part they did not come for.
  const [members, invitations, credentials] = await Promise.all([
    // 🔴 `userId` travels with every one of these. Without it the call reaches the platform as the
    // BFF's machine credential, which `actingMember` refuses — and all three sections render as a plan
    // boundary for a capability the customer has. Found in a browser; no test saw it.
    platformFetch<{ members: MemberRow[] }>("/api/v1/organization/members", {
      tenantId: session.tenantId,
      userId: session.userId,
    }),
    platformFetch<{ invitations: InvitationRow[] }>("/api/v1/organization/invitations", {
      tenantId: session.tenantId,
      userId: session.userId,
    }),
    platformFetch<{ credentials: CredentialRow[] }>("/api/v1/organization/credentials", {
      tenantId: session.tenantId,
      userId: session.userId,
    }),
  ]);

  const org = outcome.ok ? outcome.data : null;
  const memberRows = members.ok ? (members.data.members ?? []) : [];
  const activeMembers = memberRows.filter((m) => m.status === "active");
  const owners = activeMembers.filter((m) => m.role === "owner");
  const overLimit =
    org && org.seats_allowed !== undefined && org.seats_current > org.seats_allowed;

  /*
   * 🔴 What this viewer may SEE, decided once from a table.
   *
   * Deciding at each control is how a surface renders a button that is always refused — a silent dead
   * write, where pressing it teaches somebody the product is broken rather than that the action is not
   * theirs. The platform still refuses everything below; this decides what to ASK.
   */
  const role = (org?.your_role ?? "none") as ViewerRole;
  const can = controlsFor(org?.your_role);

  return (
    <PageFrame
      eyebrow="Settings"
      title={org?.name ?? session.tenantId}
      lede="Who is in this organization, who has been invited, and which keys can reach its data."
      wide
    >
      {!outcome.ok || !org ? (
        <Failure
          kind={outcome.ok ? "upstream" : outcome.kind}
          error={outcome.ok ? "the organization view was empty" : outcome.error}
          denial={outcome.ok ? undefined : outcome.denial}
          subject="organization"
        />
      ) : (
        <Section title="Seats">
          {overLimit ? (
            <Banner tone="warn" title={MEMBER_COPY["over-seat-limit"].label}>
              {SEATS_COPY.overLimit(org.seats_current, org.seats_allowed ?? 0)}
            </Banner>
          ) : null}
          <Stats>
            {/*
              🔴 Both numbers, both labelled, neither derived here. `seats_current` comes from the
              platform, which reads it from membership — the console counting rows itself would be a
              second definition of a seat, and the definition is not even settled yet.
            */}
            <Stat label={SEATS_COPY.currentLabel} value={String(org.seats_current)} note={SEATS_COPY.currentHelp} />
            {org.seats_unlimited ? (
              <Stat label={SEATS_COPY.allowedLabel} value={SEATS_COPY.unlimited} note={SEATS_COPY.allowedHelp} />
            ) : (
              <Stat
                label={SEATS_COPY.allowedLabel}
                value={String(org.seats_allowed ?? "—")}
                note={SEATS_COPY.allowedHelp}
              />
            )}
          </Stats>
          <p className="text-xs text-muted-foreground">{SEATS_COPY.definitionPending}</p>
          {/*
            A hidden control with no explanation reads as a missing feature. One sentence turns it into
            a boundary somebody can act on — by asking an owner.
          */}
          {ROLE_COPY[role] ? <p className="text-xs text-muted-foreground">{ROLE_COPY[role]}</p> : null}
        </Section>
      )}

      <Section title="Members" aside={members.ok ? `${activeMembers.length} active` : undefined}>
        {!members.ok ? (
          <Failure kind={members.kind} error={members.error} denial={members.denial} subject="members" />
        ) : activeMembers.length === 0 ? (
          <Empty title="No members yet">
            <p>Invite somebody below. You are signed in with a credential that names no person.</p>
          </Empty>
        ) : (
          <DataTable
            caption="People in this organization"
            columns={[
              { key: "who", label: "Person" },
              { key: "role", label: "Role" },
              { key: "state", label: "State" },
              { key: "joined", label: "Joined" },
              { key: "act", label: "" },
            ]}
          >
            <tbody>
              {memberRows.map((m) => {
                // 🔴 `last-owner` is a STATE the row is in before anybody clicks, not an error that
                // arrives after they do. A screen that only discovers it from a failed request has
                // already let somebody try the one action nobody can undo.
                const state: MemberState =
                  m.status === "removed"
                    ? "removed"
                    : m.role === "owner" && owners.length === 1
                      ? "last-owner"
                      : "active";
                return (
                  <tr key={m.user_id}>
                    <td>{m.email || m.user_id}</td>
                    <td>
                      <Chip>{m.role}</Chip>
                    </td>
                    <td>
                      <span title={MEMBER_COPY[state].hint}>{MEMBER_COPY[state].label}</span>
                    </td>
                    <td>{instant(m.joined_at)}</td>
                    <td>
                      {m.status === "active" && can.changeRole && (m.role !== "owner" || can.changeOwnerRole) ? (
                        <MemberActions
                          userId={m.user_id}
                          email={m.email ?? m.user_id}
                          role={m.role}
                          canPromoteToOwner={can.promoteToOwner}
                          canRemove={can.removeMember && (m.role !== "owner" || can.removeOwner)}
                          lastOwner={state === "last-owner"}
                        />
                      ) : null}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </DataTable>
        )}
      </Section>

      <Section title="Invitations">
        {can.invite ? <InviteForm /> : null}
        {!invitations.ok ? (
          <Failure kind={invitations.kind} error={invitations.error} subject="invitations" />
        ) : (invitations.data.invitations ?? []).length === 0 ? (
          <Empty title="No invitations outstanding" />
        ) : (
          <DataTable
            caption="Invitations and what became of them"
            columns={[
              { key: "email", label: "Address" },
              { key: "role", label: "Role" },
              { key: "state", label: "State" },
              { key: "expires", label: "Expires" },
            ]}
          >
            <tbody>
              {invitations.data.invitations.map((i) => {
                // The platform decides `expired` — it holds the clock. The console renders the word it
                // was given rather than comparing dates itself, which would be a second clock.
                const state: MemberState = i.state === "expired" ? "expired" : i.state === "pending" ? "invited" : "removed";
                return (
                  <tr key={i.invitation_id}>
                    <td>{i.email}</td>
                    <td>
                      <Chip>{i.role}</Chip>
                    </td>
                    <td>
                      <span title={MEMBER_COPY[state].hint}>{MEMBER_COPY[state].label}</span>
                    </td>
                    <td>{instant(i.expires_at)}</td>
                  </tr>
                );
              })}
            </tbody>
          </DataTable>
        )}
      </Section>

      <Section title="API keys">
        {can.manageKeys ? <CredentialActions /> : null}
        {!credentials.ok ? (
          <Failure kind={credentials.kind} error={credentials.error} subject="API keys" />
        ) : (credentials.data.credentials ?? []).length === 0 ? (
          <Empty title="No keys yet">
            <p>{CREDENTIAL_COPY.personalHelp}</p>
          </Empty>
        ) : (
          <DataTable
            caption="Keys that can reach this organization's data"
            columns={[
              { key: "label", label: "Label" },
              { key: "kind", label: "Kind" },
              { key: "created", label: "Created" },
              { key: "state", label: "State" },
            ]}
          >
            <tbody>
              {credentials.data.credentials.map((c) => (
                <tr key={c.credential_id}>
                  <td>{c.label}</td>
                  <td>
                    {/*
                      🔴 The WORD, not a blank column. The difference decides what member removal covers,
                      and a reader must not have to infer it from an empty cell.
                    */}
                    <span
                      title={c.kind === "personal" ? CREDENTIAL_COPY.personalHelp : CREDENTIAL_COPY.machineHelp}
                    >
                      {c.kind === "personal" ? CREDENTIAL_COPY.personalLabel : CREDENTIAL_COPY.machineLabel}
                    </span>
                  </td>
                  <td>{instant(c.created_at)}</td>
                  <td>{c.revoked ? "Revoked" : "Active"}</td>
                </tr>
              ))}
            </tbody>
          </DataTable>
        )}
      </Section>
    </PageFrame>
  );
}

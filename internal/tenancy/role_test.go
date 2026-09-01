package tenancy

import "testing"

// TestTheCapabilityTableIsExhaustive.
//
// # 🔴 Why this test exists at all
//
// `Can` fails closed: a pair missing from the table denies. That is the right RUNTIME behaviour and the
// wrong development experience — the feature simply does not work, for everybody, including the owner,
// and the reason is a map literal thirty lines away with no obvious hole in it.
//
// This makes the omission a build failure instead. Adding a capability without saying what each role may
// do with it is not a decision anybody makes deliberately, so it should not be possible to make it
// quietly.
func TestTheCapabilityTableIsExhaustive(t *testing.T) {
	if len(grants) != len(Roles) {
		t.Errorf("the table has %d roles and Roles lists %d; one of them has been added without the other",
			len(grants), len(Roles))
	}
	for _, r := range Roles {
		byCapability, ok := grants[r]
		if !ok {
			t.Errorf("role %q is listed in Roles but has no row in the table, so it holds nothing", r)
			continue
		}
		for _, c := range Capabilities {
			if _, decided := byCapability[c]; !decided {
				t.Errorf("nobody has decided whether %q may %q. Add it to the table — `false` is a "+
					"perfectly good answer, and an absent one is the same answer with no record of "+
					"having been made", r, c)
			}
		}
		for c := range byCapability {
			if !contains(Capabilities, c) {
				t.Errorf("role %q has an entry for %q, which is not in Capabilities — either the "+
					"capability was renamed and this row was left behind, or Capabilities is short one",
					r, c)
			}
		}
	}
}

// TestAnUnknownRoleHoldsNothing.
//
// 🔴 The direction of failure matters more than the failure. A mangled role, a role from a newer build,
// an empty string from a row that should not exist — each must hold NOTHING, never whatever `member`
// happens to hold. This is the property that makes a database inconsistency a broken feature rather than
// a silent grant.
func TestAnUnknownRoleHoldsNothing(t *testing.T) {
	for _, r := range []Role{"", "root", "administrator", "OWNER", " owner", "member "} {
		for _, c := range Capabilities {
			if Can(r, c) {
				t.Errorf("the unrecognised role %q holds %q", r, c)
			}
		}
		if ValidRole(string(r)) && r != "" {
			t.Errorf("%q is accepted by ValidRole but holds nothing, which is a contradiction", r)
		}
	}
}

// TestAnAdminCannotBecomeAnOwner is the escalation fence.
//
// # 🔴 What it is defending
//
// An admin manages people. If that included owners, an admin could demote the owner and promote
// themselves, and the difference between the two roles would last exactly as long as it took somebody to
// try. Two separate rules close it, and both are checked here: an admin cannot GRANT owner, and an admin
// cannot ACT ON one.
func TestAnAdminCannotBecomeAnOwner(t *testing.T) {
	if Can(Admin, TransferOwnership) {
		t.Error("an admin can transfer ownership, so an admin can make themselves an owner")
	}
	if CanGrant(Admin, Owner) {
		t.Error("an admin can grant the owner role")
	}
	if CanManage(Admin, Owner) {
		t.Error("an admin can act on an owner, so an admin can demote or remove the owner above them")
	}
	// And the rules that must keep working, or an organization has no administration at all.
	if !CanManage(Owner, Owner) {
		t.Error("an owner cannot act on another owner; ownership could never be handed over")
	}
	for _, r := range []Role{Admin, Member, Viewer} {
		if !CanManage(Admin, r) {
			t.Errorf("an admin cannot manage a %s, which is the whole of what the role is for", r)
		}
		if !CanManage(Owner, r) {
			t.Errorf("an owner cannot manage a %s", r)
		}
	}
}

// TestNobodyBelowAdminCanTouchMembership.
func TestNobodyBelowAdminCanTouchMembership(t *testing.T) {
	for _, r := range []Role{Member, Viewer} {
		for _, c := range []Capability{InviteMember, ManageMembers, TransferOwnership} {
			if Can(r, c) {
				t.Errorf("a %s holds %q", r, c)
			}
		}
		for _, target := range Roles {
			if CanManage(r, target) {
				t.Errorf("a %s can manage a %s", r, target)
			}
		}
	}
}

// TestAViewerCannotSpendMoneyOrWriteCode.
//
// The two capabilities that have consequences outside this database: RunGoals calls a model the
// organization pays for, and ApproveChange writes to their repository. A read-only role that holds
// either is not read-only.
func TestAViewerCannotSpendMoneyOrWriteCode(t *testing.T) {
	for _, c := range []Capability{RunGoals, ApproveChange, LoadSubject} {
		if Can(Viewer, c) {
			t.Errorf("a viewer holds %q, so 'read-only' is not what the role means", c)
		}
	}
	if !Can(Viewer, ReadGoals) {
		t.Error("a viewer cannot read, which leaves the role with no purpose")
	}
}

// TestTheSystemPrincipalHoldsNoCapability.
//
// 🔴 Background work is not a person. It must never satisfy ApproveChange in particular: an approval is
// a human consenting to a write, and a worker that could consent on their behalf would make the gate
// decorative while leaving every test about the gate green.
func TestTheSystemPrincipalHoldsNoCapability(t *testing.T) {
	p := System("acme")
	if !p.Valid() {
		t.Fatal("the system principal is not valid, so background work cannot act at all")
	}
	for _, c := range Capabilities {
		if p.May(c) {
			t.Errorf("the system principal holds %q", c)
		}
	}
}

// TestOrderingIsNotTheMechanism.
//
// Roles is ordered for rendering, and it would be very easy for somebody to start using its INDEX as a
// rank. This asserts the table is not merely a staircase: `member` holds ApproveChange while `admin`
// holds ManageMembers and `member` does not, so no single ordering reproduces the table and any attempt
// to replace it with a comparison would visibly change behaviour.
func TestOrderingIsNotTheMechanism(t *testing.T) {
	if !Can(Member, ApproveChange) || Can(Viewer, ApproveChange) {
		t.Fatal("the premise of this test has changed; re-derive it")
	}
	if Can(Member, ManageMembers) || !Can(Admin, ManageMembers) {
		t.Fatal("the premise of this test has changed; re-derive it")
	}
	// If authority were a rank, everything a lower role holds a higher role would hold too. Assert the
	// table is at least checked for that property, so a future `>=` rewrite has to confront it.
	for _, c := range Capabilities {
		if Can(Member, c) && !Can(Admin, c) {
			t.Errorf("a member holds %q and an admin does not; if that is deliberate, say so here, "+
				"because it means no ordering of roles can ever reproduce this table", c)
		}
		if Can(Admin, c) && !Can(Owner, c) {
			t.Errorf("an admin holds %q and an owner does not", c)
		}
	}
}

func contains(cs []Capability, c Capability) bool {
	for _, x := range cs {
		if x == c {
			return true
		}
	}
	return false
}

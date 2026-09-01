package tenancy

// role.go answers what a principal may do, and is the only place that answers it.
//
// # 🔴 Why a table and not a comparison
//
// The obvious implementation is an ordered rank — owner 3, admin 2, member 1, viewer 0 — and a check
// that reads `role >= admin`. It is shorter, and it is wrong in a way that does not show up until
// somebody adds a role. Insert `auditor` between member and admin and every `>= member` check silently
// grants it; insert it below and every `<= member` check silently grants something else. The ordering
// becomes load-bearing for decisions nobody wrote down.
//
// So authority is an exhaustive table: every role against every capability, including the falses. It is
// longer to read and impossible to be surprised by, and `TestTheCapabilityTableIsExhaustive` fails the
// build if a new capability or role is added without a decision being made for every pair.
//
// # 🔴 Why the table fails closed
//
// `Can` returns false for a role it does not recognise and for a capability it has no entry for. A row
// in the database with a mangled role is not an owner; a capability someone forgot to add is granted to
// nobody. The failure is a feature that does not work, which somebody reports — not an authorisation
// that was never enforced, which nobody does.

// Role is what somebody is within one organization.
//
// Roles are per-tenant. The same person may be an owner of one organization and a viewer of another,
// which is why the role travels on the principal beside the tenant rather than on the account.
type Role string

const (
	// Owner runs the organization. Only an owner can make another owner.
	Owner Role = "owner"
	// Admin manages people and does the work, but cannot mint an owner — so an admin cannot promote
	// themselves out of the reach of the person who appointed them.
	Admin Role = "admin"
	// Member does the work: loads repositories, runs goals, approves the changes those runs propose.
	Member Role = "member"
	// Viewer reads. They can see goals and their history and nothing else — no runs, which cost money,
	// and no approvals, which write to the repository.
	Viewer Role = "viewer"
)

// Roles is every role, in descending authority. Used for rendering a menu and for the fence that walks
// the table; it is NOT used to decide anything, because ordering is not the mechanism here.
var Roles = []Role{Owner, Admin, Member, Viewer}

// Capability is one thing a principal may be permitted to do.
//
// Named after the ACTION, not after the role that happens to hold it today. `ApproveChange` still reads
// correctly after the table is edited; `AdminOnly` would become a lie the first time it was granted to
// somebody else, and lies in identifiers survive far longer than the code that made them true.
type Capability string

const (
	// ReadGoals is seeing goals, their tasks and their history.
	ReadGoals Capability = "goal.read"
	// LoadSubject is pointing the console at a repository.
	LoadSubject Capability = "subject.load"
	// RunGoals is starting work that calls a model, which costs the organization money.
	RunGoals Capability = "goal.run"
	// ApproveChange is consenting to a Tier-C write to the customer's own repository. The sharpest
	// capability in the product: everything else is reversible by closing the tab.
	ApproveChange Capability = "change.approve"
	// InviteMember is sending an invitation, which sends mail from this organization to a stranger.
	InviteMember Capability = "member.invite"
	// ManageMembers is changing somebody's role or removing them. See CanManage: holding this does NOT
	// let you touch an owner.
	ManageMembers Capability = "member.manage"
	// TransferOwnership is making somebody an owner, and by extension acting on one.
	TransferOwnership Capability = "org.transfer"
	// SetAutonomy is choosing how much of a run proceeds without a person.
	//
	// 🔴 Owner only, and deliberately not shared with an admin. Every other capability an admin holds is
	// about who may act; this one is about whether a person is asked before the product writes to the
	// organization's repository. That is the kind of decision the person accountable for the account
	// should make, and it is one click to undo either way.
	SetAutonomy Capability = "org.autonomy"
)

// Capabilities is every capability. The fence walks it; nothing else should.
var Capabilities = []Capability{
	ReadGoals, LoadSubject, RunGoals, ApproveChange, InviteMember, ManageMembers, TransferOwnership,
	SetAutonomy,
}

// grants is the whole authorization model.
//
// Every pair is written out. The `false` entries are not noise — they are the record that somebody
// considered the pair and decided against it, which is the difference between a denial and an omission.
var grants = map[Role]map[Capability]bool{
	Owner: {
		ReadGoals: true, LoadSubject: true, RunGoals: true, ApproveChange: true,
		InviteMember: true, ManageMembers: true, TransferOwnership: true, SetAutonomy: true,
	},
	Admin: {
		ReadGoals: true, LoadSubject: true, RunGoals: true, ApproveChange: true,
		InviteMember: true, ManageMembers: true,
		// 🔴 An admin cannot create an owner. Without this an admin promotes themselves to owner and the
		// distinction between the two roles lasts exactly as long as it takes somebody to notice.
		TransferOwnership: false,
		// Nor set the autonomy level: an admin manages who may act, not whether anybody is asked.
		SetAutonomy: false,
	},
	Member: {
		ReadGoals: true, LoadSubject: true, RunGoals: true,
		// A member approves changes. The alternative — approval reserved for admins — was considered and
		// rejected: the person who understands the diff is usually not the person who administers the
		// organization, and a gate the wrong person holds gets worked around rather than obeyed.
		ApproveChange: true,
		InviteMember:  false, ManageMembers: false, TransferOwnership: false, SetAutonomy: false,
	},
	Viewer: {
		ReadGoals: true,
		// A viewer cannot load a repository: doing so pulls somebody else's source onto the server and
		// replaces what the organization is currently looking at.
		LoadSubject: false,
		// Nor run anything, which spends money, nor approve anything, which writes code.
		RunGoals: false, ApproveChange: false,
		InviteMember: false, ManageMembers: false, TransferOwnership: false, SetAutonomy: false,
	},
}

// ValidRole reports whether a string is a role this build knows.
//
// Used when accepting a role from a request or reading one from the database. A role that does not
// appear here is refused at the edge rather than being carried inward to fail closed later — failing
// closed is the backstop, not the plan.
//
// 🔴 EXACT match, with no trimming. It trimmed once, and `Can` did not, so " owner" passed validation
// and then held nothing: the account was created, the database CHECK constraint rejected it with a
// Postgres error, and the person reading that error had been told by our own validator that the value
// was fine. Wherever a validator is more permissive than the thing it guards, the gap is a value the
// system accepted and cannot use. A role is an exact token; leniency here buys nothing worth that.
func ValidRole(s string) bool {
	_, ok := grants[Role(s)]
	return ok
}

// Can reports whether a role holds a capability.
func Can(r Role, c Capability) bool {
	byCapability, known := grants[r]
	if !known {
		return false
	}
	return byCapability[c]
}

// CanManage reports whether an actor may change or remove somebody in a given role.
//
// # 🔴 Why this is not just ManageMembers
//
// ManageMembers alone would let an admin demote the owner and then hold the organization. The rule that
// closes it: you may act on somebody only if you could have granted the role they hold. An admin cannot
// grant `owner`, so an admin cannot demote, remove, or otherwise reach one.
//
// It also means an owner is never left unable to act on their own organization, and that the check is
// one sentence rather than a list of special cases that grows every time a role is added.
func CanManage(actor, target Role) bool {
	if !Can(actor, ManageMembers) {
		return false
	}
	if target == Owner {
		return Can(actor, TransferOwnership)
	}
	return true
}

// CanGrant reports whether an actor may hand out a role.
func CanGrant(actor, target Role) bool { return CanManage(actor, target) }

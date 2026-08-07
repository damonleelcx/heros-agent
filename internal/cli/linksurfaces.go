package cli

// linksurfaces.go answers the question a successful `heros link` never used to answer: **which console
// pages does what I just sent actually fill, and what would fill the rest?**
//
// # Why this exists
//
// A developer ran a real workflow, linked the run, opened the console, and found fifteen surfaces with
// nothing to say about the thing they had just sent us — every one of them empty for a *different*
// reason, none of which the screen stated. The console's half of that is P29's projection work. This is
// the CLI's half, and it is the cheaper and more useful half: the moment of maximum context is the moment
// the command succeeds, and a message printed then costs nothing and prevents the whole investigation.
//
// `interaction-simplicity-first`: when a prerequisite is missing, the message contains the next step.
// Not a list of possible steps — ONE, per surface, named as the flag or command the reader would type.

// FillMechanism is what a surface needs in order to have this tenant's data on it.
type FillMechanism string

const (
	// FillByLink — the default run link fills it. Nothing extra is needed.
	FillByLink FillMechanism = "link"
	// FillByStructure — the `--with-ir` opt-in fills it.
	FillByStructure FillMechanism = "with-ir"
	// FillByReceipt — `heros apply --link-receipt` fills it.
	FillByReceipt FillMechanism = "link-receipt"
	// FillNotByLinking — linking cannot fill it, and saying so is the honest answer.
	//
	// 🔴 This is the state that must never be silently omitted. A surface that linking cannot fill and
	// that says nothing reads as a broken page; the same surface reporting "authoring surfaces fill when
	// you submit a Variant Spec — linking travels the other way" reads as a boundary, which is what it is.
	FillNotByLinking FillMechanism = "not-by-linking"
)

// LinkSurface is one console page and what would put this organization's data on it.
type LinkSurface struct {
	// Name is the console route segment under /app, which is also the directory under
	// `web/console/src/app/app/`. Keyed that way so the fence can compare the two.
	Name string `json:"name"`
	// Route is the path a reader opens.
	Route string `json:"route"`
	// Mechanism is what fills it.
	Mechanism FillMechanism `json:"mechanism"`
	// FillWith is the ONE thing to type, or the reason nothing would help. Never empty.
	FillWith string `json:"fill_with"`
}

// LinkSurfaces is the console's data surfaces and what fills each.
//
// 🔴 It is checked against the console's own route directory by
// `TestEveryConsoleSurfaceIsAccountedForInTheLinkReport`. A page added under `web/console/src/app/app/`
// and not listed here FAILS that test — which is what "derived, not hand-written" buys: the list cannot
// silently fall behind the product, and the failure names the missing page.
//
// Surfaces that are not about a tenant's workflow data are listed with `FillNotByLinking` and a sentence
// saying what they are, rather than omitted. Omitting them would make the fence unable to tell "we
// decided this page is not a data surface" from "somebody forgot it".
func LinkSurfaces() []LinkSurface {
	return []LinkSurface{
		{"runs", "/app/runs", FillByLink,
			"nothing — the run you just linked is on it"},
		{"billing", "/app/billing", FillByLink,
			"nothing — link coverage is computed from the runs you have linked"},

		{"workflows", "/app/workflows", FillByStructure,
			"re-run with --with-ir (it needs the workflow's shape, which the default link does not carry)"},
		{"coverage", "/app/coverage", FillByStructure,
			"re-run with --with-ir (the table itself is a build fact and always renders; --with-ir is what " +
				"crosses it with YOUR nodes)"},
		{"wiring", "/app/wiring", FillByStructure, "re-run with --with-ir"},
		{"context", "/app/context", FillByStructure, "re-run with --with-ir"},
		{"memory", "/app/memory", FillByStructure, "re-run with --with-ir"},
		{"harness", "/app/harness", FillByStructure, "re-run with --with-ir"},
		{"delivery", "/app/delivery", FillByStructure, "re-run with --with-ir"},
		{"studio", "/app/studio", FillByStructure,
			"re-run with --with-ir (the matrix's columns are your nodes)"},

		{"transforms", "/app/transforms", FillByReceipt,
			"run `heros apply --link-receipt` (a transform is generated on your machine; the receipt is " +
				"what tells the platform it happened)"},

		{"authoring", "/app/authoring", FillNotByLinking,
			"nothing linking can do — authoring fills when you submit a Variant Spec, and linking travels " +
				"the other way"},
		{"variants", "/app/variants", FillNotByLinking,
			"nothing linking can do — a variant is created by authoring one, not by reporting a run"},
		{"configure", "/app/configure", FillNotByLinking,
			"not a data surface — it is where a workflow's configuration is edited"},
		{"account", "/app/account", FillNotByLinking, "not a data surface — it is your organization's profile"},
		{"settings", "/app/settings", FillNotByLinking, "not a data surface — it is your organization's settings"},
		{"device", "/app/device", FillNotByLinking, "not a data surface — it approves a terminal's sign-in"},
		{"join", "/app/join", FillNotByLinking, "not a data surface — it accepts an invitation"},
	}
}

// LinkSurfaceReport is one surface as a `link` envelope reports it.
type LinkSurfaceReport struct {
	Name   string `json:"name"`
	Route  string `json:"route"`
	Filled bool   `json:"filled"`
	// FillWith is present whether or not the surface was filled — for a filled one it says why nothing
	// more is needed, which is what stops a reader from hunting for an option that does not exist.
	FillWith string `json:"fill_with"`
}

// ReportLinkSurfaces answers, for this invocation, which surfaces it filled.
//
// `structure` and `receipt` are what the caller actually transmitted, not what it was asked to: a
// `--with-ir` that failed to transmit must report the structure surfaces as UNFILLED, or the message
// tells a developer to go look at a page that has nothing on it.
func ReportLinkSurfaces(structure, receipt bool) []LinkSurfaceReport {
	out := make([]LinkSurfaceReport, 0, len(LinkSurfaces()))
	for _, s := range LinkSurfaces() {
		filled := false
		switch s.Mechanism {
		case FillByLink:
			filled = true
		case FillByStructure:
			filled = structure
		case FillByReceipt:
			filled = receipt
		case FillNotByLinking:
			filled = false
		}
		out = append(out, LinkSurfaceReport{
			Name: s.Name, Route: s.Route, Filled: filled, FillWith: s.FillWith,
		})
	}
	return out
}

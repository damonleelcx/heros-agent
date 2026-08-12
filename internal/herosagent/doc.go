// Package herosagent defines the platform's own analysis agent — HEROS — as an ordinary Variant Spec
// over the six axes, and publishes immutable versions of it.
//
// # Why the platform's agent is described with the platform's own vocabulary (D1)
//
// The alternative, rejected at design, was a `heros_config` table with typed columns for prompt, model,
// temperature and tool list. It is a week's less work and it forks the meaning of "harness strategy"
// permanently: two vocabularies for one concept, drifting from the first release. The arbitration
// ladder puts INEXTENSIBLE DESIGN (5) far above IMPLEMENTATION COST (8), and the prohibition on
// splitting a source of truth is not conditional.
//
// The consequence worth stating: because HEROS's definition is an ordinary spec, the platform's own
// eval harness can measure it. That is what makes the rehearsal gate (D7) buildable rather than
// aspirational.
//
// # What this package refuses, and why each refusal is here rather than at run time
//
//   - A WIRING override (task 3.2). HEROS is one node; there is no ordering to author, and a spec that
//     carried one would hash a configuration nobody can execute.
//   - An UNREGISTERED MODEL (task 3.4). A model ref that resolves to nothing is a definition that
//     cannot run, and discovering that when an analysis reaches it moves the error from the person who
//     made it to the person who did not.
//   - A HOST SERVICE THAT IS NOT SUPPLIED (tasks 6b.10, 6b.11). `internal/harnessruntime` and
//     `internal/memoryruntime` both REFUSE rather than degrade — "a critic-loop without a critic IS
//     reflexion, and running it under critic-loop's config_hash would report one strategy as another" —
//     so a definition selecting one is a definition that will refuse. Refusing at PUBLISH is the same
//     argument in miniature: the save succeeded, the agent is broken, and nothing in between said so.
//
// # 🔴 The credential is BOUND, never entered (D5)
//
// `CredentialRef` is a PROVIDER NAME resolved through `providergateway`'s configured `Secrets` source.
// No type in this package has a field that can hold a key value, no method accepts one, and
// `p30_nokey_test.go` discovers that by reflection rather than reading a list — so a field added later
// is caught by the fence and not by review.
//
// This answers "set the API key" as BIND the key, not TYPE the key. It is narrower than the words used
// and is flagged as such rather than quietly delivered as if it were the same thing.
package herosagent

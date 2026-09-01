package tools

import (
	"strings"
	"testing"
)

// TestTheScannerFlagsWhatAChangeIntroduces.
func TestTheScannerFlagsWhatAChangeIntroduces(t *testing.T) {
	src := `import { OpenAI } from "openai";

const client = new OpenAI();

export async function answer(history) {
  return client.chat.completions.create({
    model: path.join("gpt", "4o"),
    messages: history,
  });
}
`
	got := scriptUnresolved(src)
	if len(got) != 1 || got[0] != "path" {
		t.Fatalf("unresolved = %v, want [path]", got)
	}
}

// TestTheScannerDoesNotFlagWhatIsInScope is the important half.
//
// 🔴 A false rejection costs a correct change and teaches people to route around the check; a false
// acceptance costs one more round of a loop that already exists. Every case here is a way a name
// legitimately enters scope, and each one that the scanner got wrong would reject real work.
func TestTheScannerDoesNotFlagWhatIsInScope(t *testing.T) {
	cases := map[string]string{
		"default import":     `import fs from "fs";\nfs.readFileSync("a");`,
		"named import":       `import { readFile } from "fs";\nreadFile.call(null);`,
		"renamed import":     `import { readFile as rf } from "fs";\nrf.call(null);`,
		"namespace import":   `import * as path from "path";\npath.join("a");`,
		"require":            `const os = require("os");\nos.homedir();`,
		"destructured req":   `const { join } = require("path");\njoin.call(null);`,
		"const binding":      `const helper = makeHelper();\nhelper.run();`,
		"let binding":        `let cache = {};\ncache.get("k");`,
		"function decl":      `function helper() {}\nhelper.call(null);`,
		"class decl":         `class Runner {}\nconst r = new Runner();\nr.go();`,
		"arrow parameter":    `const f = (client) => client.send("x");`,
		"function parameter": `function f(client) { return client.send("x"); }`,
		"object destructure": `const { client, logger } = deps;\nclient.send(logger.name);`,
		"globals":            `console.log(JSON.stringify(process.env));`,
		"typescript enum":    `enum Mode { A }\nconst m = Mode.A;`,
		"for-of binding":     `for (const item of items) { item.run(); }`,
		"catch binding":      `try { x(); } catch (err) { err.message; }`,
	}
	for name, src := range cases {
		src = strings.ReplaceAll(src, `\n`, "\n")
		if got := scriptUnresolved(src); len(got) != 0 {
			t.Errorf("%s: flagged %v, but every name is in scope\nsource:\n%s", name, got, src)
		}
	}
}

// TestStringsAndCommentsAreNotCode.
//
// 🔴 Without stripping them, a URL in a string and a name in a comment both become "unresolved names",
// and the check would reject changes for MENTIONING things.
func TestStringsAndCommentsAreNotCode(t *testing.T) {
	src := `import { OpenAI } from "openai";

// axios.get is the old way; see docs.example.com/migrating
/* legacy: superagent.post(url) */
const url = "https://api.example.com/v1/things";
const template = ` + "`" + `use lodash.merge here` + "`" + `;
const client = new OpenAI();
client.send(url + template);
`
	if got := scriptUnresolved(src); len(got) != 0 {
		t.Fatalf("flagged %v — names inside strings and comments are not code", got)
	}
}

// TestOnlyLowerCamelNamesAreReported. A capitalised bare name is usually a type, enum or namespace
// declared elsewhere in the project, and flagging those would reject correct changes constantly.
func TestOnlyLowerCamelNamesAreReported(t *testing.T) {
	if got := scriptUnresolved(`const x = SomeNamespace.Thing;`); len(got) != 0 {
		t.Errorf("flagged a capitalised name: %v", got)
	}
	if got := scriptUnresolved(`const x = lodash.merge(a, b);`); len(got) != 1 || got[0] != "lodash" {
		t.Errorf("got %v, want [lodash]", got)
	}
}

// TestAPreExistingProblemIsNotTheChangesFault.
//
// 🔴 The differential rule. A file may already reference something conditionally imported, generated at
// build time, or injected by a framework — none of which the scanner understands. Judging the candidate
// alone would reject every correct change to such a file.
func TestAPreExistingProblemIsNotTheChangesFault(t *testing.T) {
	before := scriptUnresolved(`legacyGlobal.setup();\nconst a = 1;`)
	after := scriptUnresolved(`legacyGlobal.setup();\nconst a = 2;`)
	if added := introducedBy(before, after); len(added) != 0 {
		t.Fatalf("a change that touched nothing was blamed for %v", added)
	}

	// But a NEW problem in the same file is still reported.
	worse := scriptUnresolved(`legacyGlobal.setup();\nconst a = lodash.merge({}, {});`)
	if added := introducedBy(before, worse); len(added) != 1 || added[0] != "lodash" {
		t.Fatalf("introduced = %v, want [lodash]", added)
	}
}

// TestUncheckedIsNotClean. Collapsing "not checked" into "nothing found" would let a missing interpreter
// read as a clean bill of health.
func TestUncheckedIsNotClean(t *testing.T) {
	if _, checked := unresolvedNames("package main", "go"); checked {
		t.Error("Go reported a name check that does not exist for it")
	}
	if _, checked := unresolvedNames("x", "rust"); checked {
		t.Error("an unknown language reported a name check")
	}
	if _, checked := unresolvedNames(`const a = 1;`, "typescript"); !checked {
		t.Error("TypeScript did not report that it was checked")
	}
}

// dev-p37.mjs starts the P37 stub platform a browser session runs against (task 6.11).
//
// # Why a stub and not the real platform
//
// The acceptance A1 asks for is a PERSON, in a BROWSER, on a connected repository, changing their own
// node's memory strategy and reading the result. What the console has to be doing correctly for that is
// binding to the reader's node, rendering its current value, offering the platform's own vocabulary and
// showing what the platform answers — and every one of those is exercised over a real socket here.
//
// What a stub cannot prove is that the PLATFORM's answers are right; that is `internal/api`'s own tests
// and the four-layer save proof in `p37_save_proof_test.go`. Saying so is the point: neither half is the
// acceptance on its own, and `acceptance.md` records which is which.
//
//   node scripts/dev-p37.mjs        # serves on 4399, the port `npm run dev:browser` points at

import { createServer } from "node:http";
import { connected } from "../tests/support/connected.mjs";

const PORT = Number(process.env.PORT ?? 4399);

const server = createServer((req, res) => {
  // The console signs in against its own identity provider, not against this; every other route is the
  // connected fixture, which is the same one the automated acceptance runs against so the browser
  // session and the test suite cannot disagree about what a connected platform looks like.
  connected(req, res);
});

server.listen(PORT, "127.0.0.1", () => {
  console.log(`p37 stub platform on http://127.0.0.1:${PORT} — workflow acme/agent, nodes answer + classify`);
});

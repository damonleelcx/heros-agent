// dev-sso.mjs runs the console against a REAL identity provider on this machine, for rendered-browser
// acceptance of the P22 sign-in surface (R11).
//
// # Why this exists as a script rather than as a paragraph in a README
//
// Federated sign-in cannot be checked by reading. The defects that matter here are the ones a passing
// build cannot see: a cookie whose `SameSite` withholds it on the exact navigation the IdP performs, a
// redirect our own `form-action` policy refuses, a callback that sets a session on an immutable
// response. This console's history has one of each already recorded, every one found by clicking a
// button. So the acceptance rule is a browser, and a browser needs an IdP to talk to.
//
// It also cannot be a fixed-port README snippet: the IdP's port has to be inside the console's issuer,
// its discovery document, and its redirect allowlist, all at once. Wiring those by hand is how the
// person doing acceptance ends up debugging their own environment instead of the product.
//
//   npm run dev:sso            # OIDC (the primary mechanism)
//   npm run dev:sso -- saml    # SAML (the enterprise alternative)
//
// The keys and certificates are minted per run and live in a temporary directory. Nothing is
// committed, and nothing outlives the process.

import { spawn } from "node:child_process";
import process from "node:process";
import { startStubOidc, startStubSaml } from "../tests/support/idp.mjs";

const MODE = process.argv[2] === "saml" ? "saml" : "oidc";
const PORT = Number(process.env.PORT ?? 4321);
const ORIGIN = `http://127.0.0.1:${PORT}`;
// The tenant the fixture IdP's users resolve to. Overridable so the same script can stand up the
// console for a NAMED tenant — `TENANT=cus_nousresearch npm run dev:sso` is what the P22 hermes run
// uses, so the browser check and `cmd/proof/identity` describe one tenant rather than two.
const TENANT = process.env.TENANT ?? "tenant-hermes";

const env = {
  ...process.env,
  CONSOLE_PLATFORM_CREDENTIAL: "local-dev-credential-not-a-secret",
  PLATFORM_API_BASE: process.env.PLATFORM_API_BASE ?? "http://127.0.0.1:4399",
};

let idp;
if (MODE === "oidc") {
  idp = await startStubOidc({ clientId: "console-client" });
  Object.assign(env, {
    CONSOLE_TENANT_IDENTITY: "oidc",
    CONSOLE_IDP_ISSUER: idp.base,
    CONSOLE_IDP_CLIENT_ID: "console-client",
    CONSOLE_IDP_REDIRECT_ALLOWLIST: JSON.stringify([`${ORIGIN}/auth/callback`]),
    CONSOLE_IDP_TENANT_MAP: JSON.stringify({
      strategy: "domain",
      issuers: { [idp.base]: { tenant: TENANT, verified_domains: ["acme.test"] } },
    }),
    // The client secret comes through the `Secrets` seam like every other identity credential — from
    // the injected environment here, which is the `env` source's whole contract.
    HEROS_SECRETS_SOURCE: "env",
    CONSOLE_IDP_CLIENT_SECRET: "local-dev-client-secret-not-a-secret",
  });
} else {
  const entityId = "urn:heros:test:idp";
  idp = await startStubSaml({ entityId, spEntityId: `${ORIGIN}/saml` });
  Object.assign(env, {
    CONSOLE_TENANT_IDENTITY: "saml",
    CONSOLE_SAML_IDP_ENTITY_ID: entityId,
    CONSOLE_SAML_SP_ENTITY_ID: `${ORIGIN}/saml`,
    CONSOLE_SAML_ACS_ALLOWLIST: JSON.stringify([`${ORIGIN}/auth/saml/acs`]),
    CONSOLE_SAML_IDP_METADATA_URL: idp.metadataUrl,
    CONSOLE_IDP_TENANT_MAP: JSON.stringify({
      strategy: "domain",
      issuers: { [entityId]: { tenant: TENANT, verified_domains: ["acme.test"] } },
    }),
    HEROS_SECRETS_SOURCE: "file",
    HEROS_SECRETS_DIR: idp.secretsDir,
  });
}

console.log(`identity provider (${MODE}) up; console will federate against it`);
console.log(`open ${ORIGIN}/signin`);

const child = spawn("npx", ["next", "dev", "--port", String(PORT)], { env, stdio: "inherit" });
const stop = async () => {
  child.kill("SIGTERM");
  await idp.close();
  process.exit(0);
};
process.on("SIGINT", stop);
process.on("SIGTERM", stop);
child.on("exit", stop);

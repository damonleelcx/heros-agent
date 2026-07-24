/**
 * next.config.mjs — the customer console's build.
 *
 * The console is a SEPARATE application from the operator console (web/admin-console), on its own
 * origin, with its own cookie jar. Nothing here couples them: no shared basePath, no rewrites, no
 * shared asset prefix. That separation is the P9/P8 surface boundary made structural — an admin
 * principal is a cross-tenant principal, and it must not be able to reach a single-tenant surface by
 * accident of deployment.
 *
 * # Why the cache header is split rather than global
 *
 * The operator console sets `no-store` on everything, correctly: every one of its pages renders
 * operator data. This console has a PUBLIC surface — the home page — which renders no session and no
 * tenant data, and which must keep serving when the platform API is down (FR32, NFR13). Marking it
 * `no-store` would be a small lie about what it contains and would cost the one property that makes it
 * resilient. So: tenant routes are `no-store`, the public surface is cacheable, and the split is by
 * route prefix rather than by per-page judgement — a fail-closed rule enforced page by page fails the
 * first time somebody adds a page.
 */

/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  async headers() {
    return [
      {
        // Everything under /app renders tenant data. Nothing an intermediary may keep.
        source: "/app/:path*",
        headers: [
          { key: "Cache-Control", value: "no-store" },
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "X-Frame-Options", value: "DENY" },
          { key: "Referrer-Policy", value: "no-referrer" },
        ],
      },
      {
        // The BFF's own data routes are tenant data by definition.
        source: "/api/:path*",
        headers: [
          { key: "Cache-Control", value: "no-store" },
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "Referrer-Policy", value: "no-referrer" },
        ],
      },
      {
        source: "/:path*",
        headers: [
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "X-Frame-Options", value: "DENY" },
          { key: "Referrer-Policy", value: "no-referrer" },
          // The Content-Security-Policy is set per-request in middleware.ts with a fresh nonce. It is
          // deliberately NOT static here: a static nonce is no nonce.
        ],
      },
    ];
  },
};

export default nextConfig;

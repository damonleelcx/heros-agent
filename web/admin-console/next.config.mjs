/**
 * next.config.mjs — the operator console's build.
 *
 * The console is served from its OWN origin, deployed as its own unit (P8 design Decision 11). Nothing
 * here couples it to the customer console: no shared basePath, no rewrites into another application,
 * no shared asset prefix. The two are separate deployments and the browser keeps their cookie jars
 * apart, which is the isolation the whole decision rests on.
 */

/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  // The console renders operator data. Nothing it serves may be cached by an intermediary, and every
  // response carries the headers that keep an embedded or sniffed page from becoming an attack path.
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          { key: "Cache-Control", value: "no-store" },
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "X-Frame-Options", value: "DENY" },
          { key: "Referrer-Policy", value: "no-referrer" },
          // The Content-Security-Policy is set per-request in middleware.ts, where it carries a fresh
          // nonce so the console's own bootstrap runs while any injected inline script is refused. It is
          // deliberately NOT a static header here: a static nonce is no nonce.
        ],
      },
    ];
  },
};

export default nextConfig;

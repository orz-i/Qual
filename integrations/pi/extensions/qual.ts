import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

/**
 * Human-invoked Pi convenience commands for the local Qual dogfood pilot.
 * This extension NEVER starts agents/tests, mutates Qual state, reads PATs
 * or supplies alternate business tools (the MCP adapter owns those tools).
 */
const DEFAULT_API = "http://127.0.0.1:18080";

function localAPI(): string | null {
  const value = (process.env.QUAL_API_URL || DEFAULT_API).replace(/\/$/, "");
  try {
    const url = new URL(value);
    const host = url.hostname.replace(/^\[|\]$/g, "");
    if (url.protocol !== "http:" || !["127.0.0.1", "localhost", "::1"].includes(host)) {
      return null;
    }
    if (url.username || url.password || url.search || url.hash || url.pathname !== "/") {
      return null;
    }
    return value;
  } catch {
    return null;
  }
}

export default function qualPiExtension(pi: ExtensionAPI) {
  pi.registerCommand("qual-status", {
    description: "Check local Qual HTTP liveness and PostgreSQL/S3 readiness without using a PAT",
    handler: async (_args, ctx) => {
      const api = localAPI();
      if (!api) {
        ctx.ui.notify("Qual: refusing a non-loopback API URL in the PI dogfood extension.", "error");
        return;
      }
      try {
        const [live,ready] = await Promise.all([
          fetch(api + "/healthz", { signal: AbortSignal.timeout(4000), redirect: "error" }),
          fetch(api + "/readyz", { signal: AbortSignal.timeout(4000), redirect: "error" }),
        ]);
        const summary = [
          "Qual local API:",
          "HTTP process: " + (live.ok ? "alive" : "unavailable"),
          "PostgreSQL / S3: " + (ready.ok ? "ready" : "not ready"),
          "Project: QUAL",
          "MCP: use /mcp to inspect the user-registered qual-local server.",
        ].join("\n");
        ctx.ui.notify(summary, live.ok && ready.ok ? "info" : "warning");
      } catch {
        ctx.ui.notify("Qual local API is unreachable. Start Go server; use /mcp for PI integration state.", "warning");
      }
    },
  });
  pi.registerCommand("qual-pilot", {
    description: "Show self-dogfood policy: PI is external, Qual stores facts only",
    handler: async (_args, ctx) => {
      ctx.ui.notify(
        "Qual pilot / PI:\n" +
          "1. Inspect /mcp and /skill:qual-pi-dogfood.\n" +
          "2. Select a QUAL work item and read the latest version.\n" +
          "3. Claim via MCP; implement independently in PI.\n" +
          "4. Record actual commit, tests and evidence in Qual.\n" +
          "5. Submit for in_review; an explicitly authorized Human or Agent Reviewer approves done.\n" +
          "Never upload secrets or imply Qual executed work.",
        "info",
      );
    },
  });
}

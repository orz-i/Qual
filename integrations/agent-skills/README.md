# External Agent Skill / MCP / CLI integration

The generic Skill in [qual/SKILL.md](qual/SKILL.md) can be installed into a compatible external Agent host according to the host's own Skill folder conventions. For the **local PI self-dogfood pilot**, use the [PI package registration](../pi/README.md): it bundles a lightweight PI Extension and dedicated `qual-pi-dogfood` Skill, with 23 business tools from Qual's separate MCP stdio server. Real PI registration is a user action. It is a documentation package, not a process supervisor and not a ChatGPT store-published plugin.

Build the clients:

```bash
cd apps/server
go build -o ../../bin/qual ./cmd/qual
go build -o ../../bin/qual-mcp ./cmd/qual-mcp
```

The `bin/` directory is Git-ignored. Configure `QUAL_API_URL` and `QUAL_TOKEN` using your external host's secret manager. Never commit PATs to `mcp.json`, Skill files or CLI flags. For stdio, configure an MCP server command pointing at `bin/qual-mcp`; it connects to the backend's REST API (no database credentials). See [Phase 3 validation](../../docs/engineering/phase3-verification.md).

**Boundary:** MCP stdio only, no publicly hosted remote MCP OAuth endpoint yet, no Agent scheduling, no test execution, no model invocation.

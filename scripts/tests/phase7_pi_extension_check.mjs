// PI's extension only provides diagnostics, never receives credentials or
// duplicates Qual MCP tools. Node 24 supports type-stripping the local .ts file.
import qual from "../../integrations/pi/extensions/qual.ts";

const commands = new Map();
qual({
  registerCommand(name, spec) {
    if (commands.has(name)) throw new Error("duplicate PI command");
    commands.set(name, spec);
  },
});
if (commands.size !== 2 || !commands.has("qual-status") || !commands.has("qual-pilot")) {
  throw new Error("PI extension command declaration invalid");
}

const originalFetch = globalThis.fetch;
const notes = [];
const urls = [];
const context = {
  ui: {
    notify(message, level) {
      notes.push({ message, level });
    },
  },
};

try {
  globalThis.fetch = async (url) => {
    urls.push(url);
    return { ok: true };
  };
  await commands.get("qual-status").handler("", context);
  if (urls.length !== 2 || urls.some((url) => !url.startsWith("http://127.0.0.1:18080/"))) {
    throw new Error("PI extension contacted an external destination");
  }
  if (!notes[0].message.includes("PostgreSQL / S3: ready")) {
    throw new Error("PI readiness result missing");
  }
  await commands.get("qual-pilot").handler("", context);
  if (!notes.at(-1).message.includes("authorized Human or Agent Reviewer")) {
    throw new Error("delegated review discipline missing");
  }
  urls.length = 0;
  process.env.QUAL_API_URL = "http://external.example.com";
  await commands.get("qual-status").handler("", context);
  if (urls.length !== 0 || notes.at(-1).level !== "error") {
    throw new Error("PI extension failed to refuse non-loopback API");
  }
  console.log("PI extension command and no-external-request security check passed.");
} finally {
  globalThis.fetch = originalFetch;
  delete process.env.QUAL_API_URL;
}

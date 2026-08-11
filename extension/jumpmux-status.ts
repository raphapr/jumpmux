import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

export default function (pi: ExtensionAPI) {
  let pending = Promise.resolve();

  function report(status: "working" | "done" | "update" | "closed", ctx: ExtensionContext) {
    pending = pending.then(() =>
      pi.exec("jumpmux", [
        "agent-status",
        status,
        ctx.sessionManager.getSessionId(),
        ctx.sessionManager.getSessionFile() ?? "",
        ctx.cwd,
        pi.getSessionName() ?? "",
      ], { timeout: 30000 }).then(() => undefined),
    ).catch(() => {});
    return pending;
  }

  pi.on("session_start", async (_event, ctx) => report("done", ctx));
  pi.on("agent_start", async (_event, ctx) => report("working", ctx));
  pi.on("agent_settled", async (_event, ctx) => report("done", ctx));
  pi.on("session_info_changed", async (_event, ctx) => report("update", ctx));
  pi.on("session_shutdown", async (_event, ctx) => report("closed", ctx));
}

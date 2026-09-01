import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

export default function (pi: ExtensionAPI) {
  let pending = Promise.resolve();

  function report(status: "started" | "working" | "question" | "done" | "update" | "closed", ctx: ExtensionContext, title = pi.getSessionName() ?? "") {
    pending = pending.then(() =>
      pi.exec("jumpmux", [
        "agent-status", status, ctx.sessionManager.getSessionId(), ctx.sessionManager.getSessionFile() ?? "", ctx.cwd, title,
      ], { timeout: 30000 }).then(() => undefined),
    ).catch(() => {});
    return pending;
  }

  pi.on("session_start", async (_event, ctx) => report("started", ctx));
  pi.on("agent_start", async (_event, ctx) => report("working", ctx));
  pi.on("ui_prompt_start", async (_event, ctx) => report("question", ctx));
  pi.on("ui_prompt_end", async (_event, ctx) => report(ctx.isIdle() ? "done" : "working", ctx));
  pi.on("agent_settled", async (_event, ctx) => report("done", ctx));
  pi.on("session_info_changed", async (event, ctx) => report("update", ctx, event.name ?? ""));
  pi.on("session_shutdown", async (_event, ctx) => report("closed", ctx));
}

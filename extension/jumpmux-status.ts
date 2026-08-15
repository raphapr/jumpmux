import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

const interactiveToolSuffixes = new Set(["ask_user_question"]);

function isInteractiveTool(toolName: string) {
  return [...interactiveToolSuffixes].some((suffix) =>
    toolName === suffix || [".", ":", "/"].some((separator) => toolName.endsWith(separator + suffix)),
  );
}

export default function (pi: ExtensionAPI) {
  let pending = Promise.resolve();
  const interactiveToolCallIDs = new Set<string>();

  function report(status: "started" | "working" | "question" | "done" | "update" | "closed", ctx: ExtensionContext, title = pi.getSessionName() ?? "") {
    pending = pending.then(() =>
      pi.exec("jumpmux", [
        "agent-status", status, ctx.sessionManager.getSessionId(), ctx.sessionManager.getSessionFile() ?? "", ctx.cwd, title,
      ], { timeout: 30000 }).then(() => undefined),
    ).catch(() => {});
    return pending;
  }

  pi.on("session_start", async (_event, ctx) => {
    interactiveToolCallIDs.clear();
    return report("started", ctx);
  });
  pi.on("agent_start", async (_event, ctx) => {
    interactiveToolCallIDs.clear();
    return report("working", ctx);
  });
  pi.on("tool_call", async (event, ctx) => {
    if (!isInteractiveTool(event.toolName)) return;
    interactiveToolCallIDs.add(event.toolCallId);
    return report("question", ctx);
  });
  pi.on("tool_result", async (event, ctx) => {
    if (!interactiveToolCallIDs.delete(event.toolCallId)) return;
    return report(interactiveToolCallIDs.size > 0 ? "question" : "working", ctx);
  });
  pi.on("agent_settled", async (_event, ctx) => report("done", ctx));
  pi.on("session_info_changed", async (event, ctx) => report("update", ctx, event.name ?? ""));
  pi.on("session_shutdown", async (_event, ctx) => {
    interactiveToolCallIDs.clear();
    return report("closed", ctx);
  });
}

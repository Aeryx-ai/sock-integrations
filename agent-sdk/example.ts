// Headless agent that can message its owner through sock.
// Run: SOCK_TOKEN=sock_pt_... npx tsx agent-sdk/example.ts "tell me when you are done"
//
// sock_send is auto-approved because nobody is there to click. The blast
// radius is the puppet: its owner and the conversations it belongs to, at
// 60 sends per minute. Use one puppet per binding.
// Pass text (and optionally conversation_id) to sock_send; omit conversation_id
// to message the owner.
import { query } from "@anthropic-ai/claude-agent-sdk";

const token = process.env.SOCK_TOKEN;
if (!token) throw new Error("set SOCK_TOKEN to a sock_pt_ token");

for await (const msg of query({
  prompt: process.argv[2] ?? "Send my owner a one line hello with sock_send.",
  options: {
    mcpServers: {
      sock: {
        type: "stdio",
        command: "sock-mcp",
        env: { SOCK_TOKEN: token, ...(process.env.SOCK_URL ? { SOCK_URL: process.env.SOCK_URL } : {}) },
      },
    },
    allowedTools: [
      "mcp__sock__sock_whoami",
      "mcp__sock__sock_list_conversations",
      "mcp__sock__sock_send",
      "mcp__sock__sock_fetch",
      "mcp__sock__sock_ack",
    ],
  },
})) {
  if (msg.type === "result") console.log(msg);
}

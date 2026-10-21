import type { Explanation } from "../api/explain";
import type { LearnProposal } from "../api/learn";
import { learnKindLabel, proposalDetail } from "../components/chat/learnState";
import type { ChatLine } from "./types";

/** The pane steps reach the side view open beside the chat by. */
export const SIDE_PANE = "side";

/** The last n lines of text. */
function tail(text: string, n: number): string {
  return text.split("\n").slice(-n).join("\n");
}

/** The chat's messages, each as "author: text", a blank line between them. */
export function messagesText(messages: ChatLine[]): string {
  return messages.map((m) => `${m.author}: ${m.content}`).join("\n\n");
}

/** The Explain view's last lines: each explained turn, oldest first (the
 *  view lists them newest first), with its prompt and its write-up, or why
 *  it failed. */
export function explainText(explanations: Explanation[], lines: number): string {
  const text = [...explanations]
    .reverse()
    .map((e) => {
      const head = `## turn ${e.message_row_id ? `#${e.message_row_id}` : e.message_id} (${e.status})`;
      const prompt = e.prompt ? `prompt: ${e.prompt.split("\n")[0]}` : "";
      const body = e.status === "failed" ? `error: ${e.error ?? "failed"}` : e.content;
      return [head, prompt, body].filter(Boolean).join("\n");
    })
    .join("\n\n");
  return tail(text || "No explanations yet.", lines);
}

/** The Learn view's last lines: its proposals, then its learn thread's
 *  messages. */
export function learnText(proposals: LearnProposal[], messages: ChatLine[], lines: number): string {
  const parts: string[] = [];
  if (proposals.length > 0) {
    const list = proposals.map((p) => {
      const why = p.status === "failed" && p.error ? ` (${p.error})` : "";
      return `- #${p.id} [${p.status}${why}] ${learnKindLabel(p.kind)}: ${p.title}\n  ${proposalDetail(p)}${p.rationale ? `\n  why: ${p.rationale}` : ""}`;
    });
    parts.push(`Proposals:\n${list.join("\n")}`);
  }
  if (messages.length > 0) parts.push(`Learn thread:\n${messagesText(messages)}`);
  return tail(parts.join("\n\n") || "No learn pass yet.", lines);
}

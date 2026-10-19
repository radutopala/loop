import type { ReviewComment, ReviewOutcome, ReviewOutcomeState, ReviewSession } from "../../api/review";
import type { ColorPalette } from "../../theme";

// deriveOutcome is the daemon's review.DeriveOutcome over the session the
// panel holds, so the outcome follows the comment events as they land
// instead of waiting for the next GET:
//   - request_changes when an agent finding is real and blocking (unless its
//     thread is resolved), or an open GitHub thread a person started was
//     assessed blocking;
//   - unknown otherwise when the head isn't the commit last reviewed, or a
//     finding or thread isn't judged yet;
//   - approve_with_nits when only nice-to-haves remain;
//   - approve when nothing does.
export function deriveOutcome(session: ReviewSession): ReviewOutcome {
  const o: ReviewOutcome = { state: "approve", blocking: [], nits: [], pending: [], reviewed_sha: session.reviewed_sha || undefined };
  for (const c of session.comments) {
    if (!c || c.resolved) continue;
    let impact: string | undefined;
    if (c.source !== "github") {
      if (c.verdict === "false_positive" || c.verdict === "already_fixed") continue;
      if (c.verdict === "real") impact = c.impact;
    } else if (!c.reply_to && (c.github_id ?? 0) > 0) {
      const a = session.threads?.[String(c.github_id)];
      if (a?.status === "addressed") continue;
      if (a?.status === "open") impact = a.impact;
    } else {
      continue;
    }
    if (impact === "blocking") o.blocking.push(c.id);
    else if (impact === "nice_to_have") o.nits.push(c.id);
    else o.pending.push(c.id);
  }
  if (o.blocking.length > 0) o.state = "request_changes";
  else if (!session.reviewed_sha || session.reviewed_sha !== session.head_sha) {
    o.state = "unknown";
    o.reason = "the head commit isn't reviewed yet";
  } else if (o.pending.length > 0) {
    o.state = "unknown";
    o.reason = `${o.pending.length} comment(s) not judged yet`;
  } else if (o.nits.length > 0) o.state = "approve_with_nits";
  return o;
}

// outcomeLabel is the header chip's text.
export function outcomeLabel(state: ReviewOutcomeState): string {
  switch (state) {
    case "approve":
      return "Approve";
    case "approve_with_nits":
      return "Approve with nits";
    case "request_changes":
      return "Request changes";
    case "unknown":
      return "Outcome unknown";
  }
}

// outcomeColor is the chip's color: the accent when the PR can be
// approved, red when it can't, dim when it's not known yet.
export function outcomeColor(colors: ColorPalette, state: ReviewOutcomeState): string {
  if (state === "request_changes") return colors.dangerText;
  if (state === "unknown") return colors.textDim;
  return colors.active;
}

// outcomeTitle is the chip's tooltip: what decides the outcome.
export function outcomeTitle(o: ReviewOutcome): string {
  switch (o.state) {
    case "request_changes":
      return `${o.blocking.length} blocking: ${o.blocking.join(", ")}`;
    case "approve_with_nits":
      return `${o.nits.length} nice to have: ${o.nits.join(", ")}`;
    case "approve":
      return "Nothing left open";
    case "unknown":
      return o.reason ? `Can't tell yet: ${o.reason}` : "Can't tell yet";
  }
}

// canApprove reports whether Approve on GitHub goes without forcing.
export function canApprove(o: ReviewOutcome): boolean {
  return o.state === "approve" || o.state === "approve_with_nits";
}

// approveDisabledReason is the disabled Approve button's tooltip.
export function approveDisabledReason(o: ReviewOutcome): string {
  if (o.state === "request_changes") return `${o.blocking.length} blocking comment${o.blocking.length === 1 ? "" : "s"} left; Approve anyway is in the ⋯ menu`;
  return `${o.reason ? `${o.reason[0]!.toUpperCase()}${o.reason.slice(1)}` : "The outcome is unknown"}; Approve anyway is in the ⋯ menu`;
}

// isApprovedAtHead reports whether the PR was approved from here at the head
// the session is on.
export function isApprovedAtHead(session: Pick<ReviewSession, "approved_sha" | "head_sha">): boolean {
  return !!session.approved_sha && session.approved_sha === session.head_sha;
}

// approveBody is the approval's text the confirm dialog starts from: the
// nice-to-haves left, one line each, so the author sees what's still worth
// doing. Empty when there are none.
export function approveBody(o: ReviewOutcome, comments: ReviewComment[]): string {
  if (o.nits.length === 0) return "";
  const byId = new Map(comments.map((c) => [c.id, c]));
  const lines = ["Approved. A few nice-to-haves, none blocking:", ""];
  for (const id of o.nits) {
    const c = byId.get(id);
    if (!c) continue;
    const first = c.body.split("\n").find((ln) => ln.trim() !== "") ?? "";
    const where = c.path ? `\`${c.path}:${c.line}\`` : "";
    const link = c.url ? ` (${c.url})` : "";
    lines.push(`- ${[where, first.trim()].filter(Boolean).join(" ")}${link}`);
  }
  return lines.join("\n");
}

// impactLabel is the comment badge's text.
export function impactLabel(impact: string): string {
  return impact === "blocking" ? "blocking" : "nice to have";
}

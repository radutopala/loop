// The Learn switch shows what applies: the channel's own setting when it has
// one, the config default otherwise. A click stores the opposite explicitly,
// so the channel keeps it whatever the config says later.
export function learnEffective(learn: "" | "on" | "off", defaultLearn: boolean): boolean {
  if (learn === "") return defaultLearn;
  return learn === "on";
}

export function learnToggleTitle(learn: "" | "on" | "off", defaultLearn: boolean): string {
  const on = learnEffective(learn, defaultLearn);
  const source = learn === "" ? `config default (${defaultLearn ? "on" : "off"})` : "set for this channel";
  const what = "After each run, a hidden forked session reviews it and proposes shortcuts, tasks, gate rules, mounts and a thread name for you to apply.";
  return `Learn is ${on ? "on" : "off"} — ${source}.\n${what}\nClick to turn it ${on ? "off" : "on"}.`;
}

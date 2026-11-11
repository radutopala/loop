/** The composer's text and selection. */
export interface ComposerSnapshot {
  text: string;
  start: number;
  end: number;
}

/** Composer text with dictated words in it, and where they sit. */
export interface DictatedText {
  text: string;
  /** Where the dictated words start and end in text. */
  from: number;
  to: number;
}

/**
 * Puts dictated words in place of the selection that was there when
 * dictation began, with a space between them and any text right against
 * them on either side. Empty words leave the original text.
 */
export function spliceDictation(base: ComposerSnapshot, words: string): DictatedText {
  const start = Math.max(0, Math.min(base.start, base.text.length));
  const end = Math.max(start, Math.min(base.end, base.text.length));
  const before = base.text.slice(0, start);
  const after = base.text.slice(end);
  const w = words.trim();
  if (!w) return { text: base.text, from: start, to: start };
  const lead = before && !/\s$/.test(before) ? " " : "";
  const trail = after && !/^\s/.test(after) ? " " : "";
  const from = before.length + lead.length;
  return { text: before + lead + w + trail + after, from, to: from + w.length };
}

/** A live preview: committed words, then the tentative tail. tentativeFrom
 *  is where the tail starts in text (equal to to when there's none). */
export interface DictationPreview extends DictatedText {
  tentativeFrom: number;
}

export function previewDictation(base: ComposerSnapshot, committed: string, tentative: string): DictationPreview {
  const c = committed.trim();
  const t = tentative.trim();
  const joined = c + (c && t ? " " : "") + t;
  const out = spliceDictation(base, joined);
  return { ...out, tentativeFrom: t ? out.to - t.length : out.to };
}

/** A word without case, accents or surrounding punctuation, for matching. */
function plainWord(w: string): string {
  return w
    .normalize("NFD")
    .replace(/\p{M}/gu, "")
    .replace(/^[\p{P}\p{S}]+|[\p{P}\p{S}]+$/gu, "")
    .toLowerCase();
}

/**
 * When dictated text ends with one of phrases (any case, accents or
 * punctuation), returns the text before it, without the comma or similar
 * that led into it; otherwise null.
 */
export function stripSendPhrase(text: string, phrases: readonly string[]): string | null {
  const words = text.trim().split(/\s+/).filter(Boolean);
  const plain = words.map(plainWord);
  for (const phrase of phrases) {
    const want = phrase.split(/\s+/).map(plainWord).filter(Boolean);
    if (!want.length || want.length > words.length) continue;
    const tail = plain.slice(words.length - want.length);
    if (tail.every((w, i) => w === want[i])) {
      return words
        .slice(0, words.length - want.length)
        .join(" ")
        .replace(/[\s,;:\u2013\u2014-]+$/u, "");
    }
  }
  return null;
}

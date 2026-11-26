/** The backend's cap on one tag, in bytes. */
export const MAX_TAG_LENGTH = 32;

/** The backend's cap on the tags of one channel or thread. */
export const MAX_TAGS = 20;

const TAG_PATTERN = /^[\p{Ll}\p{N}][\p{Ll}\p{N}_./-]*$/u;

/** A tag as the backend stores it: trimmed, lowercased, without a leading '#'. */
export function normalizeTag(value: string): string {
  return value.trim().replace(/^#/, "").toLowerCase();
}

/**
 * Why the backend would reject value as a tag, or null when it takes it.
 * Same rules: letters, digits and - _ . /, starting with a letter or digit,
 * at most MAX_TAG_LENGTH bytes. An empty value is no tag at all, not an error.
 */
export function tagError(value: string): string | null {
  const tag = normalizeTag(value);
  if (!tag) return null;
  if (new TextEncoder().encode(tag).length > MAX_TAG_LENGTH) return `Longer than ${MAX_TAG_LENGTH} characters`;
  if (!TAG_PATTERN.test(tag)) return "Only letters, digits and - _ . /, starting with a letter or digit";
  return null;
}

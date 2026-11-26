import { useEffect, useRef, useState } from "react";
import type { TagCount } from "../../api/channels";
import { useTheme } from "../../ThemeContext";
import { MAX_TAG_LENGTH, MAX_TAGS, normalizeTag, tagError } from "../../utils/tags";

interface TagsDialogProps {
  /** The row's tags, which the dialog starts from. */
  currentTags: string[];
  /** The row's name, shown in the title. */
  name: string;
  /** Loads the tags in use, offered to pick from. */
  loadTags: () => Promise<TagCount[]>;
  onCancel: () => void;
  /** Called with the tags to add and to remove; only when there are some. */
  onSubmit: (add: string[], remove: string[]) => void;
}

/**
 * Modal for a channel's or thread's tags, opened from its sidebar menu. A tag
 * is added by typing it (Enter adds it) or by picking one already in use;
 * each tag's × removes it. Enter on an empty input saves.
 */
export function TagsDialog({ currentTags, name, loadTags, onCancel, onSubmit }: TagsDialogProps) {
  const { colors } = useTheme();
  const [tags, setTags] = useState<string[]>(() => [...currentTags].sort());
  const [input, setInput] = useState("");
  const [existing, setExisting] = useState<TagCount[]>([]);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
    let live = true;
    loadTags()
      .then((list) => {
        if (live) setExisting(list);
      })
      .catch((err) => console.warn("[tags] listing tags failed:", err));
    return () => {
      live = false;
    };
  }, [loadTags]);

  const typed = normalizeTag(input);
  const full = tags.length >= MAX_TAGS;
  const error = tagError(input) ?? (typed && full && !tags.includes(typed) ? `At most ${MAX_TAGS} tags` : null);
  const add = tags.filter((t) => !currentTags.includes(t));
  const remove = currentTags.filter((t) => !tags.includes(t));
  const changed = add.length > 0 || remove.length > 0;
  // Tags in use that this row doesn't carry yet, narrowed by what's typed.
  const suggestions = existing.filter((t) => !tags.includes(t.tag) && t.tag.includes(typed));

  const addTag = (tag: string) => {
    if (!tags.includes(tag) && tags.length < MAX_TAGS) setTags([...tags, tag].sort());
    setInput("");
    inputRef.current?.focus();
  };
  const removeTag = (tag: string) => setTags(tags.filter((t) => t !== tag));
  const submit = () => {
    if (changed) onSubmit(add, remove);
  };

  const chip = {
    display: "inline-flex",
    alignItems: "center",
    gap: 4,
    padding: "2px 8px",
    borderRadius: 10,
    fontSize: 12,
    border: `1px solid ${colors.border}`,
  };
  return (
    <div
      data-testid="tags-dialog"
      style={{ position: "fixed", inset: 0, zIndex: 9999, display: "flex", alignItems: "center", justifyContent: "center", backgroundColor: "rgba(0,0,0,0.5)" }}
      onClick={onCancel}
    >
      <div
        style={{ backgroundColor: colors.surface, borderRadius: 12, padding: "20px 24px", maxWidth: 480, width: "90%", boxShadow: "0 8px 32px rgba(0,0,0,0.3)" }}
        onClick={(e) => e.stopPropagation()}
      >
        <div style={{ fontSize: 14, fontWeight: 600, color: colors.text, marginBottom: 12, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>Tags of {name}</div>
        <div data-testid="tags-current" style={{ display: "flex", flexWrap: "wrap", gap: 6, marginBottom: 10, minHeight: 22 }}>
          {tags.length === 0 && <span style={{ fontSize: 12, color: colors.textDisabled }}>No tags</span>}
          {tags.map((tag) => (
            <span key={tag} data-testid={`tags-chip-${tag}`} style={{ ...chip, color: colors.text, background: colors.bg }}>
              {tag}
              <button
                data-testid={`tags-remove-${tag}`}
                aria-label={`Remove ${tag}`}
                onClick={() => removeTag(tag)}
                style={{ border: "none", background: "transparent", color: colors.textDim, cursor: "pointer", padding: 0, fontSize: 13, lineHeight: 1 }}
              >
                ×
              </button>
            </span>
          ))}
        </div>
        <input
          ref={inputRef}
          data-testid="tags-input"
          value={input}
          maxLength={MAX_TAG_LENGTH + 1}
          placeholder="Add a tag, e.g. backend"
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              if (!typed) submit();
              else if (!error) addTag(typed);
            }
            if (e.key === "Escape") {
              e.preventDefault();
              onCancel();
            }
          }}
          style={{
            padding: "8px 10px",
            borderRadius: 6,
            border: `1px solid ${colors.border}`,
            background: colors.bg,
            color: colors.text,
            fontSize: 14,
            fontFamily: "inherit",
            outline: "none",
            width: "100%",
            boxSizing: "border-box",
          }}
        />
        {suggestions.length > 0 && (
          <div data-testid="tags-existing" style={{ display: "flex", flexWrap: "wrap", gap: 6, marginTop: 10, maxHeight: 120, overflowY: "auto" }}>
            {suggestions.map((t) => (
              <button
                key={t.tag}
                data-testid={`tags-pick-${t.tag}`}
                disabled={full}
                onClick={() => addTag(t.tag)}
                style={{ ...chip, background: "transparent", color: colors.textDim, cursor: full ? "default" : "pointer", fontFamily: "inherit" }}
              >
                {t.tag}
                <span style={{ color: colors.textDisabled }}>{t.count}</span>
              </button>
            ))}
          </div>
        )}
        <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 12 }}>
          <span data-testid="tags-error" style={{ flex: 1, fontSize: 11, color: error ? colors.error : colors.textDisabled }}>
            {error ?? "Enter adds a tag; search with tag:name"}
          </span>
          <button
            onClick={onCancel}
            style={{ padding: "6px 14px", borderRadius: 6, border: `1px solid ${colors.border}`, background: "transparent", color: colors.textDim, fontSize: 13, cursor: "pointer" }}
          >
            Cancel
          </button>
          <button
            data-testid="tags-submit"
            onClick={submit}
            disabled={!changed}
            style={{
              padding: "6px 14px",
              borderRadius: 6,
              border: "none",
              background: changed ? colors.active : colors.border,
              color: colors.white,
              fontSize: 13,
              cursor: changed ? "pointer" : "default",
              opacity: changed ? 1 : 0.6,
            }}
          >
            Save
          </button>
        </div>
      </div>
    </div>
  );
}

import { useEffect, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { type FileLinkTarget, getValidationStatus, requestValidation, retain, subscribe } from "../../utils/fileLinks";

export interface FileLinkOpenDetail {
  channelId: string;
  target: FileLinkTarget;
  line: number | null;
  /** Open as a kept tab rather than the preview tab. */
  pin?: boolean;
}

export interface FileLinkProps {
  channelId: string;
  raw: string;
  line: number | null;
  /** The path to look up, when it differs from raw: raw joined to the dir
   * a shell command moved to. */
  checkPath?: string;
}

export function FileLink({ channelId, raw, line, checkPath }: FileLinkProps) {
  const { colors } = useTheme();
  const path = checkPath ?? raw;
  const [status, setStatus] = useState(() => getValidationStatus(channelId, path));

  useEffect(() => {
    const release = retain(channelId, path);
    requestValidation(channelId, path);
    const unsub = subscribe(channelId, () => {
      setStatus(getValidationStatus(channelId, path));
    });
    setStatus(getValidationStatus(channelId, path));
    return () => {
      unsub();
      release();
    };
  }, [channelId, path]);

  const display = line ? `${raw}:${line}` : raw;

  if (status.kind !== "valid") {
    // Render as plain text while pending/unknown/invalid — keeps layout stable.
    return <span>{display}</span>;
  }

  const handleClick = (e: React.MouseEvent) => {
    e.preventDefault();
    const detail: FileLinkOpenDetail = { channelId, target: status.target, line };
    window.dispatchEvent(new CustomEvent<FileLinkOpenDetail>("loop:open-file", { detail }));
  };

  return (
    <a href="#" onClick={handleClick} style={{ color: colors.active, textDecoration: "underline", cursor: "pointer" }}>
      {display}
    </a>
  );
}

import { useEffect, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { getValidationStatus, requestValidation, subscribe, type FileLinkTarget } from "../../utils/fileLinks";

export interface FileLinkOpenDetail {
  channelId: string;
  target: FileLinkTarget;
  line: number | null;
}

export interface FileLinkProps {
  channelId: string;
  raw: string;
  line: number | null;
}

export function FileLink({ channelId, raw, line }: FileLinkProps) {
  const { colors } = useTheme();
  const [status, setStatus] = useState(() => getValidationStatus(channelId, raw));

  useEffect(() => {
    requestValidation(channelId, raw);
    const unsub = subscribe(channelId, () => {
      setStatus(getValidationStatus(channelId, raw));
    });
    setStatus(getValidationStatus(channelId, raw));
    return unsub;
  }, [channelId, raw]);

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
    <a
      href="#"
      onClick={handleClick}
      style={{ color: colors.active, textDecoration: "underline", cursor: "pointer" }}
    >
      {display}
    </a>
  );
}

import { checkFilesExist } from "../api/files";
import type { FileLinkOpenDetail } from "../components/chat/FileLink";
import type { FileLinkTarget } from "./fileLinks";

/** openChannelFile opens a file of the channel's roots (a path relative to
 *  one, or absolute) in its editor, at line when given, and returns where it
 *  is; it rejects when there's no such file. */
export async function openChannelFile(channelId: string, path: string, line?: number): Promise<FileLinkTarget> {
  const [found] = await checkFilesExist(channelId, [path]);
  if (!found?.exists || found.root_index === undefined || found.rel_path === undefined) throw new Error(`no file "${path}" in the channel's roots`);
  const target = { rootIndex: found.root_index, relPath: found.rel_path };
  const detail: FileLinkOpenDetail = { channelId, target, line: line ?? null };
  window.dispatchEvent(new CustomEvent<FileLinkOpenDetail>("loop:open-file", { detail }));
  return target;
}

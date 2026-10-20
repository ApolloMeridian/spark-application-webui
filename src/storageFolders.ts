import { validStorageKey, type StorageObject } from "./objectStorage";

// Draft prefixes live only in page memory. Never create folder marker objects.
export function draftFolderPath(parent: string, name: string): string {
  const relative = name.trim().replace(/\/$/, "");
  const key = parent + relative + "/";
  if (
    !relative ||
    relative.startsWith("/") ||
    relative
      .split("/")
      .some((part) => !part || part === "." || part === "..") ||
    !validStorageKey(key)
  )
    throw new Error("invalid_folder_name");
  return key;
}

export function draftFolderRows(
  drafts: string[],
  prefix: string,
  remote: StorageObject[],
  allowed: (key: string) => boolean,
): StorageObject[] {
  const seen = new Set(remote.map((object) => object.key));
  const rows: StorageObject[] = [];
  for (const key of drafts) {
    if (!key.startsWith(prefix) || key === prefix) continue;
    const child = prefix + key.slice(prefix.length).split("/")[0] + "/";
    if (seen.has(child) || !allowed(child)) continue;
    seen.add(child);
    rows.push({
      key: child,
      size: 0,
      modified: "",
      version: "",
      directory: true,
    });
  }
  return rows.sort((a, b) => a.key.localeCompare(b.key));
}

export function materializeDraftFolders(drafts: string[], objectKey: string) {
  return drafts.filter((directory) => !objectKey.startsWith(directory));
}

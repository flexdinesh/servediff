import type { ChangedFile } from "@servediff/shared";

export type FileTreeNode =
  | { kind: "folder"; name: string; path: string; children: FileTreeNode[] }
  | { kind: "file"; name: string; path: string; file: ChangedFile };

// Keep each path segment distinct so collapsing a parent hides its whole subtree.
export function buildFileTree(files: readonly ChangedFile[]): FileTreeNode[] {
  const root: FileTreeNode[] = [];
  for (const file of files) {
    const segments = file.path.split("/");
    let children = root;
    let path = "";
    for (const [index, name] of segments.entries()) {
      path = path ? `${path}/${name}` : name;
      if (index === segments.length - 1) {
        children.push({ kind: "file", name, path, file });
      } else {
        let folder = children.find(
          (entry) => entry.kind === "folder" && entry.name === name,
        );
        if (!folder) {
          folder = { kind: "folder", name, path, children: [] };
          children.push(folder);
        }
        if (folder.kind === "folder") children = folder.children;
      }
    }
  }
  const sort = (nodes: FileTreeNode[]) => {
    nodes.sort((a, b) =>
      a.kind === b.kind
        ? a.name.localeCompare(b.name)
        : a.kind === "folder"
          ? -1
          : 1,
    );
    for (const node of nodes) if (node.kind === "folder") sort(node.children);
  };
  sort(root);
  return root;
}

export function filesInTreeOrder(files: readonly ChangedFile[]): ChangedFile[] {
  const ordered: ChangedFile[] = [];
  function visit(nodes: readonly FileTreeNode[]) {
    for (const node of nodes) {
      if (node.kind === "folder") visit(node.children);
      else ordered.push(node.file);
    }
  }
  visit(buildFileTree(files));
  return ordered;
}

export function ancestorPaths(path: string): string[] {
  const parts = path.split("/");
  return parts
    .slice(0, -1)
    .map((_part, index) => parts.slice(0, index + 1).join("/"));
}

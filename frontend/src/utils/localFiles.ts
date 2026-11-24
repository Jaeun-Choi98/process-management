import { formatSize } from './format';
import { TarEntry } from './tar';
import { UploadInfo } from '../types/config';

/** 폴더 · 파일 */
export type Kind = 'dir' | 'file';

/** 내 PC 에서 고른 폴더 · 파일의 한 항목. 폴더는 아래 항목의 파일 수 · 크기 합을 가진다. */
export interface LocalNode {
  name: string;
  kind: Kind;
  file?: File;
  children: LocalNode[];
  files: number;
  bytes: number;
  /** 압축 파일 안의 실행 권한이 있는 파일 */
  exec?: boolean;
  /** 압축 파일을 연 경우 그 압축 파일 (맨 위 항목에만) */
  upload?: UploadInfo;
}

const newDir = (name: string): LocalNode => ({ name, kind: 'dir', children: [], files: 0, bytes: 0 });

/**
 * 고른 파일 목록으로 트리를 만든다. 반환값은 고른 폴더(또는 파일) 하나를 담은 가상의 뿌리다.
 * 폴더를 고르면 각 파일의 webkitRelativePath 가 "고른폴더/sub/a.txt" 이다. (빈 폴더는 브라우저가 알려주지 않는다)
 */
export const buildLocalTree = (files: File[], folderName?: string): LocalNode => {
  const root = newDir('');
  if (folderName !== undefined) root.children.push(newDir(folderName));
  const index = new Map<string, LocalNode>([['', root]]);
  for (const file of files) {
    const parts = (file.webkitRelativePath || file.name).split('/');
    let node = root;
    let key = '';
    parts.forEach((part, i) => {
      node.files += 1;
      node.bytes += file.size;
      key += `/${part}`;
      let child = index.get(key);
      if (!child) {
        child = i === parts.length - 1 ? { name: part, kind: 'file', file, children: [], files: 0, bytes: file.size } : newDir(part);
        // 폴더를 골랐는데 파일이 없으면 위에서 미리 넣은 빈 폴더를 쓴다.
        const existing = i === 0 ? node.children.find(c => c.name === part && c.kind === child!.kind) : undefined;
        if (existing) child = existing;
        else node.children.push(child);
        index.set(key, child);
      }
      node = child;
    });
  }
  const sort = (node: LocalNode) => {
    node.children.sort((a, b) => (a.kind === b.kind ? a.name.localeCompare(b.name) : a.kind === 'dir' ? -1 : 1));
    node.children.forEach(sort);
  };
  sort(root);
  return root;
};

/** 서버 쪽 대상 이름(name) 아래로 tar 항목을 만든다. (내 PC 의 이름과 달라도 서버 이름을 그대로 쓴다) */
export const tarEntries = (node: LocalNode, name: string): TarEntry[] =>
  node.kind === 'file'
    ? [{ path: name, file: node.file, modified: node.file?.lastModified }]
    : [{ path: name }, ...node.children.flatMap(child => tarEntries(child, `${name}/${child.name}`))];

export const describeLocal = (node: LocalNode) =>
  node.kind === 'dir' ? `파일 ${node.files.toLocaleString()}개, ${formatSize(node.bytes)}` : formatSize(node.bytes);

/** 폴더 안의 모든 파일 (상대 경로, 예: bin/start.sh) */
export const listFiles = (node: LocalNode, prefix = ''): { path: string; node: LocalNode }[] =>
  node.children.flatMap(child =>
    child.kind === 'file' ? [{ path: prefix + child.name, node: child }] : listFiles(child, `${prefix}${child.name}/`)
  );

/**
 * Go 서버에 받아 둔 압축 파일의 목록으로 트리를 만든다. 맨 위 항목(name)이 압축 파일 전체(폴더)다.
 * 링크는 파일처럼 보여준다. 압축 파일 안에 폴더 항목이 따로 없어도 경로로 폴더를 만든다.
 */
export const treeFromUpload = (upload: UploadInfo, name: string): LocalNode => {
  const root: LocalNode = { ...newDir(name), upload };
  const dirs = new Map<string, LocalNode>([['', root]]);
  const dirOf = (path: string): LocalNode => {
    const existing = dirs.get(path);
    if (existing) return existing;
    const slash = path.lastIndexOf('/');
    const node = newDir(path.slice(slash + 1));
    dirOf(slash < 0 ? '' : path.slice(0, slash)).children.push(node);
    dirs.set(path, node);
    return node;
  };
  for (const entry of upload.entries) {
    if (entry.type === 'dir') {
      dirOf(entry.path);
      continue;
    }
    const slash = entry.path.lastIndexOf('/');
    const parentPath = slash < 0 ? '' : entry.path.slice(0, slash);
    dirOf(parentPath).children.push({
      name: entry.path.slice(slash + 1),
      kind: 'file',
      children: [],
      files: 1,
      bytes: entry.size ?? 0,
      exec: entry.exec,
    });
    // 위 폴더들의 파일 수 · 크기에 더한다.
    for (let path = parentPath; ; path = path.slice(0, Math.max(path.lastIndexOf('/'), 0))) {
      const dir = dirs.get(path)!;
      dir.files += 1;
      dir.bytes += entry.size ?? 0;
      if (path === '') break;
    }
  }
  const sort = (node: LocalNode) => {
    node.children.sort((a, b) => (a.kind === b.kind ? a.name.localeCompare(b.name) : a.kind === 'dir' ? -1 : 1));
    node.children.forEach(sort);
  };
  sort(root);
  return root;
};

/** 폴더 안의 모든 폴더 (상대 경로, 예: app, app/bin) */
export const listDirs = (node: LocalNode, prefix = ''): { path: string; node: LocalNode }[] =>
  node.children.flatMap(child =>
    child.kind === 'dir' ? [{ path: prefix + child.name, node: child }, ...listDirs(child, `${prefix}${child.name}/`)] : []
  );

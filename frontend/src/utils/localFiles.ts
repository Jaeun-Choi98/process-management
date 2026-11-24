import { formatSize } from './format';
import { TarEntry } from './tar';

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

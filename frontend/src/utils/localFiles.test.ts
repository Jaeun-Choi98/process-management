import { describeLocal, listFiles, treeFromUpload } from './localFiles';
import { UploadInfo } from '../types/config';

test('압축 파일 목록으로 트리를 만든다 (폴더 항목이 없어도 경로로 만든다)', () => {
  const upload: UploadInfo = {
    id: 'x',
    name: 'myapp-1.2.zip',
    format: 'zip',
    root: 'myapp-1.2',
    files: 3,
    bytes: 30,
    entries: [
      { path: 'bin/server', type: 'file', size: 10, exec: true },
      { path: 'lib/a.so', type: 'link', target: 'a.so.1' },
      { path: 'lib/a.so.1', type: 'file', size: 15 },
      { path: 'README', type: 'file', size: 5 },
      { path: 'empty', type: 'dir' },
    ],
  };
  const root = treeFromUpload(upload, 'myapp-1.2');
  expect(root.upload).toBe(upload);
  expect(root.children.map(c => `${c.kind}:${c.name}`)).toEqual(['dir:bin', 'dir:empty', 'dir:lib', 'file:README']);
  expect(listFiles(root).map(f => f.path)).toEqual(['bin/server', 'lib/a.so', 'lib/a.so.1', 'README']);
  expect(listFiles(root)[0].node.exec).toBe(true);
  expect(describeLocal(root)).toBe('파일 4개, 30 B');
});

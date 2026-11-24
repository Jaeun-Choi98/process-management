/**
 * @jest-environment node
 */
// jsdom 에는 TextEncoder · Blob.arrayBuffer 가 없어 Node 환경에서 돌린다. (Blob 은 Node 의 것을 쓴다)
import { Blob as NodeBlob } from 'buffer';
import { buildTar, paxRecord } from './tar';

Object.assign(globalThis, { Blob: NodeBlob });

const readBlob = async (blob: Blob) => new Uint8Array(await blob.arrayBuffer());

const text = (bytes: Uint8Array) => new TextDecoder().decode(bytes).replace(/\0.*$/s, '');

/** 헤더를 차례로 읽어 [타입, 이름, 내용] 목록으로 돌려준다. 체크섬도 확인한다. */
const parse = (data: Uint8Array) => {
  const entries: [string, string, string][] = [];
  for (let offset = 0; offset + 512 <= data.length; ) {
    const block = data.subarray(offset, offset + 512);
    if (block.every(b => b === 0)) break;
    const stored = parseInt(text(block.subarray(148, 154)), 8);
    const sum = block.reduce((total, b, i) => total + (i >= 148 && i < 156 ? 0x20 : b), 0);
    expect(stored).toBe(sum);
    expect(text(block.subarray(257, 263))).toBe('ustar');
    const size = parseInt(text(block.subarray(124, 135)), 8);
    const content = new TextDecoder().decode(data.subarray(offset + 512, offset + 512 + size));
    entries.push([String.fromCharCode(block[156]), text(block.subarray(0, 100)), content]);
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  return entries;
};

test('폴더 · 파일 · 한글 이름을 tar 로 묶는다', async () => {
  const blob = buildTar([
    { path: 'app' },
    { path: 'app/run.sh', file: new Blob(['echo hi\n']) },
    { path: 'app/설정.ini', file: new Blob(['a=1']) },
  ]);
  const data = await readBlob(blob);
  expect(data.length % 512).toBe(0);
  expect(parse(data)).toEqual([
    ['5', 'app/', ''],
    ['0', 'app/run.sh', 'echo hi\n'],
    ['x', 'PaxHeader', paxRecord('path', 'app/설정.ini')],
    ['0', 'app/__.ini', 'a=1'],
  ]);
});

test('PAX 레코드 길이는 자기 자릿수를 포함한 바이트 수다', () => {
  for (const value of ['a', 'x'.repeat(90), 'x'.repeat(95), '가'.repeat(40)]) {
    const record = paxRecord('path', value);
    expect(Number(record.split(' ')[0])).toBe(new TextEncoder().encode(record).length);
  }
});

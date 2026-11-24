/**
 * 브라우저에서 고른 폴더 · 파일을 tar(ustar + PAX) 로 묶는다. (프로그램 업데이트 업로드용)
 * 파일 내용은 Blob 조각으로 이어 붙이기만 하므로 큰 파일도 메모리에 읽어 들이지 않는다.
 * 권한 · 소유자는 넣지 않는다. (Go 서버가 정해진 값으로 다시 묶는다)
 */
export interface TarEntry {
  /** tar 안의 경로 (예: app/lib/a.jar). 폴더는 끝에 '/' 를 붙이지 않는다. */
  path: string;
  /** 없으면 폴더 */
  file?: Blob;
  /** 수정 시각 (ms) */
  modified?: number;
}

const BLOCK = 512;
/** ustar 크기 칸(8진수 11자리)의 한계 */
const MAX_SIZE = 8 ** 11 - 1;
const encoder = new TextEncoder();

/** 8진수 숫자 칸: 앞을 0 으로 채우고 마지막은 NUL */
const writeOctal = (block: Uint8Array, offset: number, length: number, value: number) => {
  block.set(encoder.encode(Math.floor(value).toString(8).padStart(length - 1, '0')), offset);
};

const header = (name: string, size: number, type: '0' | '5' | 'x', modified: number): Uint8Array => {
  const block = new Uint8Array(BLOCK);
  block.set(encoder.encode(name).subarray(0, 100), 0);
  writeOctal(block, 100, 8, type === '5' ? 0o755 : 0o644);
  writeOctal(block, 108, 8, 0);
  writeOctal(block, 116, 8, 0);
  writeOctal(block, 124, 12, size);
  writeOctal(block, 136, 12, modified / 1000);
  block[156] = type.charCodeAt(0);
  // magic "ustar\0" + version "00"
  block.set(encoder.encode('ustar\0'), 257);
  block.set(encoder.encode('00'), 263);
  // 체크섬: 체크섬 칸을 공백으로 둔 채 모든 바이트를 더한다.
  block.fill(0x20, 148, 156);
  const sum = block.reduce((total, byte) => total + byte, 0);
  block.set(encoder.encode(sum.toString(8).padStart(6, '0') + '\0 '), 148);
  return block;
};

/** PAX 레코드 "<길이> key=value\n". 길이는 자기 자신의 자릿수까지 포함한 바이트 수다. */
export const paxRecord = (key: string, value: string): string => {
  const body = ` ${key}=${value}\n`;
  const bodyLength = encoder.encode(body).length;
  let length = bodyLength + 1;
  while (String(length).length + bodyLength !== length) length = String(length).length + bodyLength;
  return `${length}${body}`;
};

const padding = (size: number) => new Uint8Array((BLOCK - (size % BLOCK)) % BLOCK);

/** 영문 · 숫자 등 ASCII 로만 된 100 바이트 미만 이름은 ustar 헤더에 바로 넣고, 아니면 PAX 로 넣는다. */
const fitsUstar = (name: string) => name.length < 100 && /^[\x20-\x7e]*$/.test(name);

export function buildTar(entries: TarEntry[]): Blob {
  const parts: BlobPart[] = [];
  for (const entry of entries) {
    const name = entry.file ? entry.path : `${entry.path}/`;
    const size = entry.file?.size ?? 0;
    const modified = entry.modified ?? Date.now();
    if (size > MAX_SIZE) throw new Error(`${entry.path}: 8GB 를 넘는 파일은 올릴 수 없습니다`);
    if (!fitsUstar(name)) {
      const pax = encoder.encode(paxRecord('path', name));
      parts.push(header('PaxHeader', pax.length, 'x', modified), pax, padding(pax.length));
    }
    parts.push(header(fitsUstar(name) ? name : name.replace(/[^\x20-\x7e]/g, '_'), size, entry.file ? '0' : '5', modified));
    if (entry.file) parts.push(entry.file, padding(size));
  }
  // 끝 표시: 빈 블록 두 개
  parts.push(new Uint8Array(BLOCK * 2));
  return new Blob(parts, { type: 'application/x-tar' });
}

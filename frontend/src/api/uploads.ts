import { requestApi, uploadApi } from './client';
import { UploadInfo } from '../types/config';

/**
 * 압축 파일(zip · tar.gz · tgz · tar)로 설치 · 업데이트: 먼저 Go 서버에 받아 두고(목록 확인), 그 id 로 설치 · 업데이트한다.
 * 받아 둔 파일은 쓰고 나면 Go 서버가 지우고, 쓰지 않으면 1시간 뒤 지운다. 창을 닫으면 바로 지운다.
 */
const API = `${process.env.PUBLIC_URL}/api/uploads`;

/** 압축 파일로 볼 수 있는 이름 */
export const ARCHIVE_ACCEPT = '.zip,.tar.gz,.tgz,.tar';
export const isArchiveName = (name: string) => /\.(zip|tar\.gz|tgz|tar)$/i.test(name);

/** 압축 파일 이름에서 확장자를 뺀 이름 (myapp-1.2.tar.gz → myapp-1.2) */
export const archiveBaseName = (name: string) => name.replace(/\.(zip|tar\.gz|tgz|tar)$/i, '');

export const saveUpload = (file: File, onProgress: (loaded: number, total: number) => void) =>
  uploadApi<UploadInfo>(`${API}?${new URLSearchParams({ name: file.name })}`, file, 'application/octet-stream', onProgress);

/** 받아 둔 압축 파일을 지운다. (실패해도 1시간 뒤 지워지므로 무시한다) */
export const deleteUpload = (id: string) => requestApi<void>(`${API}/${encodeURIComponent(id)}`, { method: 'DELETE' }).catch(() => undefined);

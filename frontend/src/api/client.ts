/**
 * 사이트 API(Go 서버, npm start 에서는 src/setupProxy.js 가 Go 서버로 전달)를 호출한다.
 * 실패하면 서버가 준 에러 문구를 담아 던진다.
 */
export async function requestApi<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, { cache: 'no-store', ...init });
  const isJson = (response.headers.get('content-type') ?? '').includes('application/json');
  const body = isJson ? await response.json() : null;
  if (!response.ok) {
    throw new Error(body?.error ?? `http ${response.status} ${response.statusText}`);
  }
  // Go 서버가 꺼져 있으면 개발 서버가 index.html 을 돌려주기도 한다.
  if (!isJson) throw new Error(`${url}: JSON 응답이 아닙니다. Go 서버가 실행 중인지 확인하세요`);
  return body as T;
}

/**
 * 파일을 올리면서 진행률을 알려준다. fetch 는 올리는 진행률을 알 수 없어 XMLHttpRequest 를 쓴다.
 * 응답 · 에러 처리는 requestApi 와 같다. onProgress(loaded, total) 는 브라우저가 보낸 바이트 수다.
 */
export function uploadApi<T>(url: string, body: Blob, contentType: string, onProgress: (loaded: number, total: number) => void): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', url);
    xhr.setRequestHeader('Content-Type', contentType);
    xhr.upload.onprogress = e => onProgress(e.loaded, e.lengthComputable ? e.total : body.size);
    xhr.onerror = () => reject(new Error('서버에 연결하지 못했거나 연결이 끊겼습니다'));
    xhr.onload = () => {
      const isJson = (xhr.getResponseHeader('content-type') ?? '').includes('application/json');
      let parsed: { error?: string } | null = null;
      if (isJson) {
        try {
          parsed = JSON.parse(xhr.responseText);
        } catch {
          parsed = null;
        }
      }
      if (xhr.status < 200 || xhr.status >= 300) {
        reject(new Error(parsed?.error ?? `http ${xhr.status} ${xhr.statusText}`));
        return;
      }
      if (!isJson || parsed === null) {
        reject(new Error(`${url}: JSON 응답이 아닙니다. Go 서버가 실행 중인지 확인하세요`));
        return;
      }
      resolve(parsed as T);
    };
    xhr.send(body);
  });
}

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

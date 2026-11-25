/**
 * 로그인 토큰은 브라우저 탭마다 따로 둔다(sessionStorage). 새로고침해도 유지되지만,
 * 새 탭 · 새 브라우저 창에서는 비어 있어 다시 로그인해야 한다.
 */
const TOKEN_KEY = 'authToken';
/** 토큰이 없거나 만료되어 로그인 화면으로 가야 할 때 window 에 보내는 이벤트 */
export const AUTH_REQUIRED_EVENT = 'auth-required';
/** 저장소를 쓸 수 없는 브라우저(사생활 보호 모드 등)에서는 메모리에만 둔다. (새로고침하면 다시 로그인) */
let fallback = '';

export function getToken(): string {
  try {
    return sessionStorage.getItem(TOKEN_KEY) ?? '';
  } catch {
    return fallback;
  }
}

export function setToken(token: string): void {
  fallback = token;
  try {
    if (token) sessionStorage.setItem(TOKEN_KEY, token);
    else sessionStorage.removeItem(TOKEN_KEY);
  } catch {
    // 메모리(fallback)에만 남는다.
  }
}

/** 토큰을 지우고 로그인 화면으로 보낸다. (401 응답 · 로그아웃) */
export function requireLogin(): void {
  setToken('');
  window.dispatchEvent(new Event(AUTH_REQUIRED_EVENT));
}

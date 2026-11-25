import { requestApi } from './client';
import { requireLogin, setToken } from './token';

const API = `${process.env.PUBLIC_URL}/api`;

export async function login(user: string, password: string): Promise<void> {
  const { token } = await requestApi<{ token: string }>(`${API}/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ user, password }),
  });
  setToken(token);
}

export async function logout(): Promise<void> {
  try {
    await requestApi<void>(`${API}/logout`, { method: 'POST' });
  } finally {
    requireLogin();
  }
}

/** 헤더를 실을 수 없는 내려받기 링크용 1회용 표 (30초 안에 한 번) */
export async function downloadTicket(): Promise<string> {
  const { ticket } = await requestApi<{ ticket: string }>(`${API}/download-ticket`, { method: 'POST' });
  return ticket;
}

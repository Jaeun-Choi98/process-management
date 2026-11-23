import { useCallback, useEffect, useState } from 'react';
import { validateProcessConfig } from '../utils/schema';
import { requestApi } from '../api/client';
import { ProcessConfig } from '../types/config';

const API_URL = `${process.env.PUBLIC_URL}/api/config`;

/** 그룹 · 서버 · 프로세스 설정을 읽고 저장한다. */
export function useProcessConfig() {
  /** 아직 못 읽었으면 null */
  const [config, setConfig] = useState<ProcessConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const reload = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = validateProcessConfig(await requestApi(API_URL));
      if (!result.ok) throw new Error(`설정이 올바르지 않습니다\n${result.errors.join('\n')}`);
      setConfig(result.config);
    } catch (e) {
      setConfig(null);
      setError(`설정을 불러오지 못했습니다: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    reload();
  }, [reload]);

  /** 서버에 저장하고, 서버가 저장한 내용(비밀번호 제외)으로 화면을 갱신한다. 실패하면 에러를 던진다. */
  const save = useCallback(async (next: ProcessConfig) => {
    setConfig(
      await requestApi<ProcessConfig>(API_URL, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(next),
      })
    );
  }, []);

  /**
   * 최신 설정을 읽어 화면을 갱신한다. reload 와 달리 실패해도 지금 화면을 그대로 두고 에러를 던진다.
   * (설정 창을 열 때, 다른 사용자가 바꾼 내용 위에서 편집하도록)
   */
  const refresh = useCallback(async () => {
    const result = validateProcessConfig(await requestApi(API_URL));
    if (!result.ok) throw new Error(`설정이 올바르지 않습니다\n${result.errors.join('\n')}`);
    setConfig(result.config);
  }, []);

  /** 다른 API(예: PUT /api/processes/:id/watch)가 돌려준 저장된 설정으로 화면을 갱신한다. */
  const apply = useCallback((next: ProcessConfig) => setConfig(next), []);

  return { config, loading, error, reload, refresh, save, apply };
}

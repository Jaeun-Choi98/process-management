import React, { useEffect, useState } from 'react';
import Icon from '../Icon/Icon';
import { ProcessConfig, ProcessEntry, ProcessInstance, ServerStatus } from '../../types/config';
import { requestApi } from '../../api/client';
import { conditionFromCommand } from '../../utils/schema';
import './WatchEditor.css';

interface WatchEditorProps {
  process: ProcessEntry;
  serverLabel: string;
  /** 지금 상태 (확인 전이거나 접속 실패면 undefined) */
  status: ServerStatus['processes'][number] | undefined;
  /** 저장에 성공하면 서버가 돌려준 전체 설정으로 화면을 갱신한다. */
  onSaved: (config: ProcessConfig) => void;
  onClose: () => void;
}

/** 아래에서 process 를 프로세스 항목 이름으로 쓰므로 환경 변수는 여기서 읽어 둔다. */
const PROCESSES_API = `${process.env.PUBLIC_URL}/api/processes`;
const SERVERS_API = `${process.env.PUBLIC_URL}/api/servers`;

type Condition = { matchName: string; matchArgs: string; matchRegex: string };

/** 실행 판단 조건 방식: 이름 · 문자열(정확한 비교) 또는 정규식 */
type Mode = 'text' | 'regex';

/**
 * 모니터링 화면에서 프로세스 하나의 실행 판단 조건과 자동 재시작을 바꾼다.
 * 상태 표시 · 중지 · 자동 재시작이 모두 이 조건 하나를 쓴다.
 * '지금 맞는 프로세스 확인' 으로 저장 전에 서버의 ps 에서 조건에 맞는 프로세스를 미리 볼 수 있다.
 */
const WatchEditor: React.FC<WatchEditorProps> = ({ process: target, serverLabel, status, onSaved, onClose }) => {
  const [mode, setMode] = useState<Mode>(target.matchRegex ? 'regex' : 'text');
  const [matchName, setMatchName] = useState(target.matchName ?? '');
  const [matchArgs, setMatchArgs] = useState(target.matchArgs ?? '');
  const [matchRegex, setMatchRegex] = useState(target.matchRegex ?? '');
  const [autoRestart, setAutoRestart] = useState(Boolean(target.autoRestart));
  /** '확인' 결과. 조건을 바꾸면 지운다. */
  const [found, setFound] = useState<ProcessInstance[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  /** '실행 중인 프로세스에서 고르기' 목록 (닫혀 있으면 null) */
  const [running, setRunning] = useState<ProcessInstance[] | null>(null);
  const [runningFilter, setRunningFilter] = useState('');

  // 고른 방식의 값만 보낸다. (다른 방식 칸에 남은 값은 저장하지 않는다)
  const condition: Condition =
    mode === 'regex'
      ? { matchName: '', matchArgs: '', matchRegex: matchRegex.trim() }
      : { matchName: matchName.trim(), matchArgs: matchArgs.trim(), matchRegex: '' };
  const hasCondition = Boolean(condition.matchName || condition.matchArgs || condition.matchRegex);
  const base = `${PROCESSES_API}/${encodeURIComponent(target.id)}`;
  const watch = status?.watch;

  useEffect(() => {
    const closeOnEscape = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', closeOnEscape);
    return () => window.removeEventListener('keydown', closeOnEscape);
  }, [onClose]);

  const test = async (checked: Condition = condition) => {
    setBusy(true);
    setError(null);
    try {
      const query = new URLSearchParams({ name: checked.matchName, args: checked.matchArgs, regex: checked.matchRegex });
      setFound((await requestApi<{ instances: ProcessInstance[] }>(`${base}/watch-test?${query}`)).instances);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      onSaved(
        await requestApi<ProcessConfig>(`${base}/watch`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ autoRestart, ...condition }),
        })
      );
      onClose();
    } catch (e) {
      setError(`저장하지 못했습니다: ${e instanceof Error ? e.message : String(e)}`);
      setBusy(false);
    }
  };

  /** 서버에서 지금 실행 중인 프로세스 목록을 연다. */
  const openRunning = async () => {
    setBusy(true);
    setError(null);
    try {
      const response = await requestApi<{ processes: ProcessInstance[] }>(`${SERVERS_API}/${encodeURIComponent(target.server)}/ps`);
      setRunning(response.processes);
      setRunningFilter('');
    } catch (e) {
      setError(`실행 중인 프로세스를 읽지 못했습니다: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setBusy(false);
    }
  };

  /** 목록에서 고른 프로세스의 명령줄로 조건을 채우고, 바로 몇 개가 맞는지 확인한다. */
  const pickRunning = (instance: ProcessInstance) => {
    const picked = conditionFromCommand(instance.command);
    setMode('text');
    setMatchName(picked.matchName);
    setMatchArgs(picked.matchArgs);
    setRunning(null);
    void test({ ...picked, matchRegex: '' });
  };

  const lowerFilter = runningFilter.toLowerCase();
  const shownRunning = running?.filter(r => r.command.toLowerCase().includes(lowerFilter) || String(r.pid).includes(lowerFilter)) ?? [];

  const changeCondition = (setter: (value: string) => void) => (e: React.ChangeEvent<HTMLInputElement>) => {
    setter(e.target.value);
    setFound(null);
  };
  const changeMode = (next: Mode) => {
    setMode(next);
    setFound(null);
  };

  return (
    <div className="watch-editor-backdrop" onMouseDown={e => e.target === e.currentTarget && onClose()}>
      <div className="watch-editor" role="dialog" aria-modal="true" aria-label="실행 조건 · 자동 재시작">
        <header className="watch-editor-header">
          <div>
            <strong>{target.label || target.id}</strong>
            <span>{serverLabel}</span>
            <span>실행 조건 · 자동 재시작</span>
          </div>
          <button className="btn-icon" onClick={onClose} title="닫기 (Esc)">
            <Icon name="close" />
          </button>
        </header>

        <div className="watch-editor-body">
          {status && (
            <div className="watch-editor-status">
              지금: 조건에 맞는 프로세스 {status.instances.length}개
              {watch && ` · 자동 시작 ${watch.restarts}회`}
              {watch?.lastRestartAt && ` (마지막 ${new Date(watch.lastRestartAt).toLocaleString()})`}
              {watch?.paused && ' · 화면에서 중지해 자동 재시작이 멈춘 상태 (시작 · 재시작하면 다시 켜집니다)'}
              {watch?.lastError && <div className="watch-editor-error-text">오류: {watch.lastError}</div>}
            </div>
          )}

          <fieldset className="watch-editor-mode">
            <legend>실행 판단 조건 — 화면의 상태 · 중지 대상 · 자동 재시작이 모두 이 조건을 씁니다</legend>
            <label>
              <input type="radio" name="mode" checked={mode === 'text'} onChange={() => changeMode('text')} />
              이름 · 문자열 (권장)
            </label>
            <label>
              <input type="radio" name="mode" checked={mode === 'regex'} onChange={() => changeMode('regex')} />
              정규식
            </label>
          </fieldset>

          <div className="watch-editor-test">
            <button className="btn btn-secondary" onClick={running ? () => setRunning(null) : openRunning} disabled={busy}>
              {running ? '목록 닫기' : '실행 중인 프로세스에서 고르기'}
            </button>
            {!running && <small className="watch-editor-hint">지금 떠 있는 프로세스를 고르면 아래 칸을 자동으로 채웁니다.</small>}
          </div>
          {running && (
            <div className="watch-editor-running">
              <input
                className="input"
                placeholder="찾기 (예: java, app.jar, PID)"
                value={runningFilter}
                onChange={e => setRunningFilter(e.target.value)}
                autoFocus
              />
              <ul className="watch-editor-found">
                {shownRunning.length === 0 && <li>맞는 프로세스가 없습니다.</li>}
                {shownRunning.map(instance => (
                  <li key={instance.pid}>
                    <button className="watch-editor-running-row" onClick={() => pickRunning(instance)} title={instance.command}>
                      <code>{instance.pid}</code> {instance.command}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {mode === 'text' ? (
            <>
              <label className="watch-editor-field">
                <span>명령줄 문자열</span>
                <input
                  className="input"
                  placeholder="-jar /opt/app/app.jar"
                  value={matchArgs}
                  onChange={changeCondition(setMatchArgs)}
                  autoFocus
                />
                <small>ps 명령줄에 들어 있어야 하는 글자 (정규식 아님). 이 프로세스만 가진 글자로 적으세요.</small>
              </label>
              <label className="watch-editor-field">
                <span>실행 이름 (선택)</span>
                <input className="input" placeholder="java" value={matchName} onChange={changeCondition(setMatchName)} />
                <small>실행 파일 이름. 적으면 명령줄 첫 단어의 파일 이름이 정확히 같아야 합니다. 둘 다 적으면 둘 다 맞아야 합니다.</small>
              </label>
            </>
          ) : (
            <label className="watch-editor-field">
              <span>정규식</span>
              <input
                className="input"
                placeholder="java .*app\.jar"
                value={matchRegex}
                onChange={changeCondition(setMatchRegex)}
                autoFocus
              />
              <small>ps 명령줄 전체에 대한 정규식. 이름 · 문자열로 구분할 수 없을 때만 쓰세요.</small>
            </label>
          )}

          <div className="watch-editor-test">
            <button className="btn btn-secondary" onClick={() => test()} disabled={!hasCondition || busy}>
              지금 맞는 프로세스 확인
            </button>
            {found && (
              <span className={found.length === 1 ? 'watch-editor-ok-text' : 'watch-editor-warn-text'}>
                {found.length === 0 ? '맞는 프로세스가 없습니다' : `${found.length}개`}
                {found.length > 1 && ' — 여러 개가 맞습니다. 다른 프로세스가 섞이지 않았는지 보세요 (중지하면 모두 중지됩니다)'}
              </span>
            )}
          </div>
          {found && found.length > 0 && (
            <ul className="watch-editor-found">
              {found.map(instance => (
                <li key={instance.pid} title={instance.command}>
                  <code>{instance.pid}</code> {instance.command}
                </li>
              ))}
            </ul>
          )}

          <label className="watch-editor-check">
            <input type="checkbox" checked={autoRestart} onChange={e => setAutoRestart(e.target.checked)} />
            자동 재시작 — 조건에 맞는 프로세스가 없으면 시작 스크립트를 실행합니다
          </label>
          {autoRestart && !target.startScript && (
            <div className="watch-editor-warn">시작 스크립트가 등록되지 않았습니다. 설정에서 먼저 등록하세요.</div>
          )}
          {autoRestart && !hasCondition && <div className="watch-editor-warn">자동 재시작에는 실행 판단 조건이 필요합니다.</div>}

          {error && <div className="watch-editor-error">{error}</div>}
        </div>

        <footer className="watch-editor-footer">
          <button className="btn btn-secondary" onClick={onClose}>
            취소
          </button>
          <button className="btn btn-primary" onClick={save} disabled={busy || (autoRestart && !hasCondition)}>
            저장
          </button>
        </footer>
      </div>
    </div>
  );
};

export default WatchEditor;

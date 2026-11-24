import React, { useEffect, useMemo, useRef, useState } from 'react';
import Icon from '../Icon/Icon';
import RemoteFilePicker from '../RemoteFilePicker/RemoteFilePicker';
import { ProcessConfig, ProcessInstance, ServerEntry } from '../../types/config';
import { requestApi, uploadApi } from '../../api/client';
import { ARCHIVE_ACCEPT, archiveBaseName, deleteUpload, saveUpload } from '../../api/uploads';
import { detectStarted, fetchRunning, saveConditionFrom, startProcess } from '../../api/startDetect';
import { buildTar } from '../../utils/tar';
import { formatSize } from '../../utils/format';
import { buildLocalTree, describeLocal, listDirs, listFiles, LocalNode, tarEntries, treeFromUpload } from '../../utils/localFiles';
import { baseDirOf, conditionFromCommand } from '../../utils/schema';
import './ProgramInstall.css';

interface ProgramInstallProps {
  server: ServerEntry;
  /** 설치(프로세스 등록)가 끝나면 호출한다. (설정 · 상태 새로고침) */
  onInstalled: () => void;
  /** 실행 판단 조건을 저장하면 서버가 돌려준 전체 설정으로 호출한다. */
  onConfigSaved: (config: ProcessConfig) => void;
  onClose: () => void;
}

interface InstallResult {
  path: string;
  processId: string;
  files: number;
  bytes: number;
}

const SERVERS_API = `${process.env.PUBLIC_URL}/api/servers`;

/** 설치 폴더 이름 · 시작 파일 경로에 쓸 수 있는 글자 (설정 경로 규칙과 같다) */
const NAME_PATTERN = /^[A-Za-z0-9_-][A-Za-z0-9._-]*$/;
const RELATIVE_PATTERN = /^[A-Za-z0-9._-][A-Za-z0-9._/-]*$/;

/** 내 PC 폴더 이름 → 설치 폴더 이름 (쓸 수 없는 글자는 '-') */
const toInstallName = (name: string) => name.replace(/[^A-Za-z0-9._-]/g, '-').replace(/^[.]+/, '') || 'app';

/**
 * 시작 파일 후보 순서: start*.sh → *.sh · (압축 파일에서) 실행 권한이 있는 파일 → 확장자 없는 파일(실행 파일일 수 있음) → 나머지.
 * 같으면 얕은 것 · 이름 순. 점수 2 까지만 처음에 골라 둔다.
 */
const startScore = (path: string, exec?: boolean) => {
  const name = path.slice(path.lastIndexOf('/') + 1);
  return /^start.*\.sh$/i.test(name) ? 0 : /\.sh$/i.test(name) || exec ? 1 : !name.includes('.') ? 2 : 3;
};

/** 폴더 안의 파일을 시작 파일 후보 순서로 (점수와 함께) */
const startCandidates = (node: LocalNode) =>
  listFiles(node)
    .map(f => ({ path: f.path, score: startScore(f.path, f.node.exec) }))
    .sort((a, b) => a.score - b.score || a.path.split('/').length - b.path.split('/').length || a.path.localeCompare(b.path));

type Step = { kind: 'info' | 'ok' | 'error'; text: string };

/**
 * 프로그램 설치: 내 PC 의 폴더를 서버의 새 폴더(기본: 기본 폴더/폴더 이름)에 올리고 프로세스로 등록한다.
 * '설치 후 바로 시작' 이면 시작한 뒤 새로 뜬 프로세스를 찾아 실행 판단 조건을 자동으로 정한다.
 */
const ProgramInstall: React.FC<ProgramInstallProps> = ({ server, onInstalled, onConfigSaved, onClose }) => {
  const serverLabel = server.label || server.id;
  const folderInput = useRef<HTMLInputElement>(null);
  const archiveInput = useRef<HTMLInputElement>(null);

  const [folder, setFolder] = useState<LocalNode | null>(null);
  const [parentDir, setParentDir] = useState(baseDirOf(server));
  const [name, setName] = useState('');
  const [startFile, setStartFile] = useState('');
  const [label, setLabel] = useState('');
  const [startAfter, setStartAfter] = useState(true);
  const [autoRestart, setAutoRestart] = useState(false);
  const [picking, setPicking] = useState(false);

  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<{ loaded: number; total: number; doneText: string } | null>(null);
  const [steps, setSteps] = useState<Step[]>([]);
  const [installed, setInstalled] = useState<InstallResult | null>(null);
  /** 시작한 뒤 하나로 좁히지 못한 새 프로세스 (사용자가 고른다) */
  const [candidates, setCandidates] = useState<ProcessInstance[] | null>(null);

  useEffect(() => {
    folderInput.current?.setAttribute('webkitdirectory', '');
  }, []);
  /** Esc 로 닫을 때도 최신 close 를 부른다. (올려 둔 압축 파일 정리) */
  const closeRef = useRef<() => void>(onClose);

  useEffect(() => {
    const closeOnEscape = (e: KeyboardEvent) => e.key === 'Escape' && !busy && !picking && closeRef.current();
    window.addEventListener('keydown', closeOnEscape);
    return () => window.removeEventListener('keydown', closeOnEscape);
  }, [busy, picking]);

  /**
   * 압축 파일 안에서 고른 기준 폴더 (맨 위 폴더를 벗긴 경로, 압축 파일 전체면 '').
   * 안이 app/app/start.sh 처럼 한 번 더 감싸져 있으면 안쪽 app 을 골라 그 내용만 설치한다.
   */
  const [archiveSub, setArchiveSub] = useState('');
  /** 압축 파일 안의 폴더 (기준 폴더 목록) */
  const archiveDirs = useMemo(() => (folder?.upload ? listDirs(folder) : []), [folder]);
  /** 실제로 설치할 내용: 기준 폴더를 골랐으면 그 폴더 */
  const source = (archiveSub && archiveDirs.find(d => d.path === archiveSub)?.node) || folder;

  /** 시작 파일 후보 (후보 순서대로) */
  const files = useMemo(() => (source ? startCandidates(source).map(c => c.path) : []), [source]);

  /** 시작 파일을 후보 첫 번째로 미리 골라 둔다. (없으면 비운다) */
  const presetStart = (node: LocalNode) => {
    const first = startCandidates(node).find(c => c.score <= 2 && RELATIVE_PATTERN.test(c.path))?.path ?? '';
    setStartFile(first);
    setStartAfter(first !== '');
  };

  /** 설치할 내용(폴더 또는 압축 파일)을 정하고 이름 · 시작 파일을 미리 채운다. 전에 올려 둔 압축 파일은 지운다. */
  const chooseSource = (node: LocalNode, displayName: string) => {
    if (folder?.upload && folder.upload.id !== node.upload?.id) void deleteUpload(folder.upload.id);
    // 압축 파일 맨 위에 파일 없이 폴더 하나만 있으면 그 폴더를 기준으로 미리 골라 둔다. (화면에서 바꿀 수 있다)
    const only = node.upload && node.children.length === 1 && node.children[0].kind === 'dir' ? node.children[0] : null;
    setFolder(node);
    setArchiveSub(only ? only.name : '');
    setName(toInstallName(displayName));
    setLabel(displayName);
    presetStart(only ?? node);
    setInstalled(null);
    setCandidates(null);
  };

  const changeArchiveSub = (sub: string) => {
    setArchiveSub(sub);
    const node = (sub && archiveDirs.find(d => d.path === sub)?.node) || folder;
    if (node) presetStart(node);
  };

  const openFolder = (e: React.ChangeEvent<HTMLInputElement>) => {
    const picked = Array.from(e.target.files ?? []);
    e.target.value = '';
    if (picked.length === 0) return;
    const folderName = picked[0].webkitRelativePath.split('/')[0];
    setSteps([]);
    chooseSource(buildLocalTree(picked, folderName).children[0], folderName);
  };

  /** 압축 파일은 바로 Go 서버에 올려 두고(목록 확인) 설치할 때 그것을 쓴다. */
  const openArchive = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    setBusy(true);
    setSteps([]);
    setProgress({ loaded: 0, total: file.size, doneText: '다 올렸습니다. 압축 파일 목록을 읽는 중...' });
    try {
      const upload = await saveUpload(file, (loaded, total) => setProgress({ loaded, total, doneText: '다 올렸습니다. 압축 파일 목록을 읽는 중...' }));
      const displayName = upload.root || archiveBaseName(file.name);
      chooseSource(treeFromUpload(upload, displayName), displayName);
      log({ kind: 'info', text: `${file.name} 을(를) 올려 두었습니다. (${upload.root ? `맨 위 폴더 ${upload.root}/ 는 벗겨서 설치합니다. ` : ''}1시간 안에 설치하세요)` });
    } catch (err) {
      log({ kind: 'error', text: `압축 파일을 올리지 못했습니다: ${err instanceof Error ? err.message : String(err)}` });
    } finally {
      setProgress(null);
      setBusy(false);
    }
  };

  /** 닫을 때 설치하지 않은 압축 파일은 Go 서버에서 지운다. */
  const close = () => {
    if (folder?.upload && !installed) void deleteUpload(folder.upload.id);
    onClose();
  };

  closeRef.current = close;

  const target = `${parentDir.replace(/\/+$/, '')}/${name}`;
  const problem = !folder
    ? '설치할 폴더나 압축 파일을 여세요'
    : !NAME_PATTERN.test(name)
      ? '설치 폴더 이름은 영문 · 숫자 · . _ - 로 적어야 합니다'
      : startFile && !RELATIVE_PATTERN.test(startFile)
        ? '시작 파일 경로에 한글 · 공백 등 쓸 수 없는 글자가 있습니다 (파일 이름을 바꿔 다시 여세요)'
        : null;

  const log = (step: Step) => setSteps(prev => [...prev, step]);

  /** 고른(또는 찾은) 프로세스로 실행 판단 조건을 저장한다. */
  const applyCondition = async (processId: string, instance: ProcessInstance) => {
    const condition = conditionFromCommand(instance.command);
    onConfigSaved(await saveConditionFrom(processId, instance, autoRestart));
    setCandidates(null);
    log({
      kind: 'ok',
      text:
        `실행 판단 조건을 정했습니다: 실행 이름 ${condition.matchName}${condition.matchArgs ? `, 명령줄 문자열 ${condition.matchArgs}` : ''}` +
        (autoRestart ? ' · 자동 재시작 켬' : ''),
    });
  };

  const install = async () => {
    if (!folder || problem) return;
    if (!source) return;
    const sourceText = folder.upload ? `${folder.upload.name}${archiveSub ? ` 안의 ${archiveSub}/` : ''}` : folder.name;
    if (!window.confirm(`${serverLabel} 의\n${target}\n에 '${sourceText}' (${describeLocal(source)}) 을(를) 설치할까요?${startFile && startAfter ? '\n설치한 뒤 바로 시작합니다.' : ''}`)) {
      return;
    }
    setBusy(true);
    setCandidates(null);
    try {
      let result: InstallResult;
      if (folder.upload) {
        // 압축 파일: 이미 Go 서버에 있으므로 풀어서 서버로 보내기만 한다.
        log({ kind: 'info', text: '압축을 풀어 서버로 보내는 중...' });
        result = await requestApi<InstallResult>(`${SERVERS_API}/${encodeURIComponent(server.id)}/install-upload`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ upload: folder.upload.id, path: target, start: startFile, label: label.trim(), sub: archiveSub }),
        });
      } else {
        const body = buildTar(tarEntries(folder, name));
        setProgress({ loaded: 0, total: body.size, doneText: '다 올렸습니다. 서버에서 설치하는 중...' });
        const query = new URLSearchParams({ path: target, start: startFile, label: label.trim() });
        result = await uploadApi<InstallResult>(
          `${SERVERS_API}/${encodeURIComponent(server.id)}/install?${query}`,
          body,
          'application/x-tar',
          (loaded, total) => setProgress({ loaded, total, doneText: '다 올렸습니다. 서버에서 설치하는 중...' })
        );
      }
      setProgress(null);
      setInstalled(result);
      log({ kind: 'ok', text: `설치했습니다: ${result.path} (파일 ${result.files.toLocaleString()}개, ${formatSize(result.bytes)}) · 프로세스 '${result.processId}' 등록` });
      onInstalled();

      if (startFile && startAfter) {
        const before = await fetchRunning(server.id);
        log({ kind: 'info', text: `시작했습니다: ${(await startProcess(result.processId)).message}. 새로 뜬 프로세스를 찾는 중...` });
        const detection = await detectStarted(server.id, before, result.path);
        if (detection.picked) {
          await applyCondition(result.processId, detection.picked);
        } else if (detection.candidates.length > 0) {
          setCandidates(detection.candidates);
          log({ kind: 'info', text: '새로 뜬 프로세스가 여러 개입니다. 아래에서 이 프로그램을 고르세요.' });
        } else {
          log({
            kind: 'error',
            text: '새로 뜬 프로세스를 찾지 못했습니다. 시작 파일이 바로 끝났거나 시작에 실패했을 수 있습니다. 로그를 확인한 뒤 카드의 [조건]에서 정하세요.',
          });
        }
        onInstalled();
      } else if (startFile) {
        log({ kind: 'info', text: '설정을 고친 뒤 카드의 [시작]을 누르세요. 처음 시작할 때 실행 판단 조건을 자동으로 정합니다.' });
      }
    } catch (e) {
      log({ kind: 'error', text: e instanceof Error ? e.message : String(e) });
    } finally {
      setProgress(null);
      setBusy(false);
    }
  };

  const pickCandidate = async (instance: ProcessInstance) => {
    if (!installed) return;
    setBusy(true);
    try {
      await applyCondition(installed.processId, instance);
      onInstalled();
    } catch (e) {
      log({ kind: 'error', text: `조건을 저장하지 못했습니다: ${e instanceof Error ? e.message : String(e)}` });
    } finally {
      setBusy(false);
    }
  };

  const percent = progress && progress.total > 0 ? Math.floor((progress.loaded / progress.total) * 100) : 0;
  const locked = busy || installed !== null;

  return (
    <div className="program-install-backdrop" onMouseDown={e => e.target === e.currentTarget && !busy && !picking && close()}>
      <div className="program-install" role="dialog" aria-modal="true" aria-label="프로그램 설치">
        <header className="program-install-header">
          <div>
            <strong>프로그램 설치</strong>
            <span>{serverLabel}</span>
            <span>
              {server.user}@{server.host}
            </span>
          </div>
          <button className="btn-icon" onClick={close} disabled={busy} title="닫기 (Esc)">
            <Icon name="close" />
          </button>
        </header>

        <div className="program-install-body">
          <section>
            <h3>1. 내 PC 의 프로그램 (폴더 또는 압축 파일)</h3>
            <div className="program-install-row">
              <button className="btn btn-secondary" onClick={() => folderInput.current?.click()} disabled={locked}>
                <Icon name="folder" />
                폴더 열기
              </button>
              <button className="btn btn-secondary" onClick={() => archiveInput.current?.click()} disabled={locked}>
                <Icon name="file" />
                압축 파일 열기
              </button>
              <input ref={folderInput} type="file" hidden onChange={openFolder} />
              <input ref={archiveInput} type="file" accept={ARCHIVE_ACCEPT} hidden onChange={openArchive} />
              {folder ? (
                <code>
                  {folder.upload ? folder.upload.name : folder.name} ({describeLocal(folder)})
                </code>
              ) : (
                <small>폴더: 브라우저가 '업로드할까요?' 를 물으면 허용하세요. 압축 파일(zip · tar.gz · tgz · tar)은 열면 바로 올려 목록을 확인합니다.</small>
              )}
            </div>
            {folder?.upload && archiveDirs.length > 0 && (
              <label className="program-install-field">
                <span>기준 폴더</span>
                <select className="input" value={archiveSub} onChange={e => changeArchiveSub(e.target.value)} disabled={locked}>
                  <option value="">(압축 파일 전체)</option>
                  {archiveDirs.map(d => (
                    <option key={d.path} value={d.path}>
                      {d.path}/ ({describeLocal(d.node)})
                    </option>
                  ))}
                </select>
                <small>
                  고른 폴더의 내용만 설치 폴더에 들어갑니다. 압축 파일 안이 app/app/start.sh 처럼 한 번 더 감싸져 있으면 안쪽 폴더를 고르세요.
                </small>
              </label>
            )}
          </section>

          <section>
            <h3>2. 설치 위치</h3>
            <div className="program-install-row">
              <code title={parentDir}>{parentDir}/</code>
              <input className="input program-install-name" value={name} onChange={e => setName(e.target.value)} disabled={locked} placeholder="폴더 이름" />
              <button className="btn btn-secondary" onClick={() => setPicking(true)} disabled={locked} title="설치할 위치(상위 폴더)를 바꿉니다">
                위치 바꾸기
              </button>
            </div>
            <small>
              설치 폴더: <code>{target}</code> (이미 있으면 설치하지 않습니다. 바꾸려면 프로그램 업데이트를 쓰세요)
            </small>
          </section>

          <section>
            <h3>3. 시작 파일</h3>
            <select className="input" value={startFile} onChange={e => setStartFile(e.target.value)} disabled={locked || !folder}>
              <option value="">(시작 파일 없음 — 설치 · 등록만)</option>
              {files.map(path => (
                <option key={path} value={path} disabled={!RELATIVE_PATTERN.test(path)}>
                  {path}
                  {!RELATIVE_PATTERN.test(path) ? ' (쓸 수 없는 글자)' : ''}
                </option>
              ))}
            </select>
            <small>시작 · 재시작 때 실행할 파일입니다. 스크립트(.sh)나 실행 파일 모두 됩니다. 고른 파일과 모든 .sh 파일에 실행 권한을 줍니다.</small>
          </section>

          <section>
            <h3>4. 프로세스</h3>
            <label className="program-install-field">
              <span>표시 이름</span>
              <input className="input" value={label} onChange={e => setLabel(e.target.value)} disabled={locked} placeholder={name} />
            </label>
            <label className="program-install-check">
              <input type="checkbox" checked={startAfter && startFile !== ''} onChange={e => setStartAfter(e.target.checked)} disabled={locked || !startFile} />
              설치 후 바로 시작 — 새로 뜬 프로세스로 실행 판단 조건을 자동으로 정합니다
            </label>
            <label className="program-install-check">
              <input type="checkbox" checked={autoRestart && startAfter && startFile !== ''} onChange={e => setAutoRestart(e.target.checked)} disabled={locked || !startFile || !startAfter} />
              자동 재시작 — 조건을 정한 뒤 켭니다
            </label>
            {!startAfter && startFile && <small>설정 파일을 먼저 고쳐야 하면 끄세요. 나중에 카드의 [시작]을 누르면 그때 조건을 자동으로 정합니다.</small>}
          </section>

          {(progress || steps.length > 0) && (
            <section className="program-install-steps">
              {progress && (
                <div className="program-install-progress">
                  <progress value={progress.loaded} max={progress.total || 1} />
                  <span>
                    {progress.loaded >= progress.total
                      ? progress.doneText
                      : `올리는 중 ${formatSize(progress.loaded)} / ${formatSize(progress.total)} (${percent}%)`}
                  </span>
                </div>
              )}
              {steps.map((step, i) => (
                <div key={i} className={`program-install-step program-install-step-${step.kind}`}>
                  {step.text}
                </div>
              ))}
              {candidates && (
                <ul className="program-install-candidates">
                  {candidates.map(instance => (
                    <li key={instance.pid}>
                      <button onClick={() => pickCandidate(instance)} disabled={busy} title={instance.command}>
                        <code>{instance.pid}</code> {instance.command}
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          )}
        </div>

        <footer className="program-install-footer">
          {problem && !installed && <span className="program-install-problem">{problem}</span>}
          <button className="btn btn-secondary" onClick={close} disabled={busy}>
            닫기
          </button>
          <button className="btn btn-primary" onClick={install} disabled={problem !== null || locked}>
            <Icon name="upload" />
            {busy && progress ? `올리는 중 ${percent}%` : busy ? '진행 중...' : installed ? '설치했습니다' : '설치'}
          </button>
        </footer>
      </div>

      {picking && (
        <RemoteFilePicker
          serverId={server.id}
          serverLabel={serverLabel}
          startDir={parentDir}
          title="설치할 위치 고르기"
          mode="dir"
          onPick={dir => {
            setParentDir(dir);
            setPicking(false);
          }}
          onClose={() => setPicking(false)}
        />
      )}
    </div>
  );
};

export default ProgramInstall;

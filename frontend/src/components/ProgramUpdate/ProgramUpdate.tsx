import React, { useCallback, useEffect, useRef, useState } from 'react';
import Icon from '../Icon/Icon';
import { ProcessEntry, RemoteDir, RemoteEntry, ServerEntry, UpdateResult } from '../../types/config';
import { requestApi, uploadApi } from '../../api/client';
import { buildTar, TarEntry } from '../../utils/tar';
import { formatDateTime, formatSize } from '../../utils/format';
import { hasCondition, scriptPath } from '../../utils/schema';
import './ProgramUpdate.css';

interface ProgramUpdateProps {
  server: ServerEntry;
  /** 이 서버의 프로세스 (업데이트 후 재시작할 대상 고르기) */
  processes: ProcessEntry[];
  /** 재시작한 뒤 호출한다. (상태 새로고침) */
  onRestarted: () => void;
  onClose: () => void;
}

const SERVERS_API = `${process.env.PUBLIC_URL}/api/servers`;
const PROCESSES_API = `${process.env.PUBLIC_URL}/api/processes`;

/** 재시작할 수 없는 이유. 재시작은 실행 판단 조건으로 중지한 뒤 시작 스크립트를 실행한다. */
const restartProblem = (p: ProcessEntry) =>
  !p.startScript ? '시작 스크립트가 없습니다' : !hasCondition(p) ? '실행 판단 조건이 없습니다' : null;

/**
 * 업데이트 대상과 관련 있어 보이는 프로세스: 시작 스크립트가 바꾸는 폴더 안에 있거나(폴더),
 * 바꾸는 파일과 같은 폴더에 있는(파일) 프로세스. 경로는 글자로만 비교하므로 처음 체크 상태로만 쓴다.
 */
const isRelated = (p: ProcessEntry, baseDir: string | undefined, target: string, kind: Kind) => {
  const script = scriptPath(p.startScript, baseDir) ?? '';
  return kind === 'dir' ? script.startsWith(`${target}/`) : script.startsWith(`${target.slice(0, target.lastIndexOf('/'))}/`);
};

type RestartResult = { name: string; ok: boolean; text: string };

type Kind = 'dir' | 'file';
const KIND_TEXT: Record<Kind, string> = { dir: '폴더', file: '파일' };

/** 내 PC 에서 고른 폴더 · 파일의 한 항목. 폴더는 아래 항목의 파일 수 · 크기 합을 가진다. */
interface LocalNode {
  name: string;
  kind: Kind;
  file?: File;
  children: LocalNode[];
  files: number;
  bytes: number;
}

const newDir = (name: string): LocalNode => ({ name, kind: 'dir', children: [], files: 0, bytes: 0 });

/**
 * 고른 파일 목록으로 트리를 만든다. 반환값은 고른 폴더(또는 파일) 하나를 담은 가상의 뿌리다.
 * 폴더를 고르면 각 파일의 webkitRelativePath 가 "고른폴더/sub/a.txt" 이다. (빈 폴더는 브라우저가 알려주지 않는다)
 */
const buildLocalTree = (files: File[], folderName?: string): LocalNode => {
  const root = newDir('');
  if (folderName !== undefined) root.children.push(newDir(folderName));
  const index = new Map<string, LocalNode>([['', root]]);
  for (const file of files) {
    const parts = (file.webkitRelativePath || file.name).split('/');
    let node = root;
    let key = '';
    parts.forEach((part, i) => {
      node.files += 1;
      node.bytes += file.size;
      key += `/${part}`;
      let child = index.get(key);
      if (!child) {
        child = i === parts.length - 1 ? { name: part, kind: 'file', file, children: [], files: 0, bytes: file.size } : newDir(part);
        // 폴더를 골랐는데 파일이 없으면 위에서 미리 넣은 빈 폴더를 쓴다.
        const existing = i === 0 ? node.children.find(c => c.name === part && c.kind === child!.kind) : undefined;
        if (existing) child = existing;
        else node.children.push(child);
        index.set(key, child);
      }
      node = child;
    });
  }
  const sort = (node: LocalNode) => {
    node.children.sort((a, b) => (a.kind === b.kind ? a.name.localeCompare(b.name) : a.kind === 'dir' ? -1 : 1));
    node.children.forEach(sort);
  };
  sort(root);
  return root;
};

/** 서버 쪽 대상 이름(name) 아래로 tar 항목을 만든다. (내 PC 의 이름과 달라도 서버 이름을 그대로 쓴다) */
const tarEntries = (node: LocalNode, name: string): TarEntry[] =>
  node.kind === 'file'
    ? [{ path: name, file: node.file, modified: node.file?.lastModified }]
    : [{ path: name }, ...node.children.flatMap(child => tarEntries(child, `${name}/${child.name}`))];

const baseName = (path: string) => path.slice(path.lastIndexOf('/') + 1);
const parentPath = (path: string) => path.slice(0, path.lastIndexOf('/')) || '/';
const remoteKind = (entry: RemoteEntry): Kind | null => (entry.type === 'dir' || entry.type === 'file' ? entry.type : null);
const describeLocal = (node: LocalNode) =>
  node.kind === 'dir' ? `파일 ${node.files.toLocaleString()}개, ${formatSize(node.bytes)}` : formatSize(node.bytes);

/**
 * 프로그램 업데이트: 왼쪽은 서버(SSH 계정의 홈 아래), 오른쪽은 내 PC 에서 고른 폴더 · 파일.
 * 양쪽에서 하나씩 고르고 '프로그램 업데이트' 를 누르면 서버의 대상은 '<이름>.bak.<날짜-시각>' 으로 남고
 * 내 PC 의 내용이 같은 이름으로 올라간다. 폴더는 폴더로, 파일은 파일로만 바꿀 수 있다.
 */
const ProgramUpdate: React.FC<ProgramUpdateProps> = ({ server, processes, onRestarted, onClose }) => {
  const base = `${SERVERS_API}/${encodeURIComponent(server.id)}`;
  const serverLabel = server.label || server.id;

  const [remote, setRemote] = useState<RemoteDir | null>(null);
  const [remoteLoading, setRemoteLoading] = useState(false);
  const [remoteError, setRemoteError] = useState<string | null>(null);
  const [remoteSelected, setRemoteSelected] = useState<RemoteEntry | null>(null);

  /** 내 PC 쪽: 뿌리부터 지금 폴더까지 */
  const [localPath, setLocalPath] = useState<LocalNode[]>([]);
  const [localSelected, setLocalSelected] = useState<LocalNode | null>(null);
  const folderInput = useRef<HTMLInputElement>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<UpdateResult | null>(null);
  /** 업데이트 후 재시작할 프로세스 id */
  const [restartIds, setRestartIds] = useState<Set<string>>(new Set());
  /** 업데이트 후 재시작 중인 프로세스 이름 (진행 표시) */
  const [restarting, setRestarting] = useState<string | null>(null);
  const [restartResults, setRestartResults] = useState<RestartResult[]>([]);
  /** 올린 바이트 수 / 전체. 다 올린 뒤에는 서버가 확인하고 바꾸는 동안 기다린다. */
  const [progress, setProgress] = useState<{ loaded: number; total: number } | null>(null);

  const loadRemote = useCallback(
    async (path: string) => {
      setRemoteLoading(true);
      setRemoteError(null);
      try {
        const query = path ? `?${new URLSearchParams({ path })}` : '';
        setRemote(await requestApi<RemoteDir>(`${base}/browse${query}`));
        setRemoteSelected(null);
      } catch (e) {
        setRemoteError(e instanceof Error ? e.message : String(e));
      } finally {
        setRemoteLoading(false);
      }
    },
    [base]
  );

  // 서버 기본 폴더에서 시작한다. 없거나 열 수 없으면(홈 밖 등) 홈 폴더.
  const baseDir = server.baseDir;
  useEffect(() => {
    const open = async () => {
      if (baseDir) {
        try {
          setRemote(await requestApi<RemoteDir>(`${base}/browse?${new URLSearchParams({ path: baseDir })}`));
          return;
        } catch {
          // 홈 폴더로 연다.
        }
      }
      await loadRemote('');
    };
    void open();
  }, [base, baseDir, loadRemote]);

  // React 의 input 속성에는 webkitdirectory 가 없어 직접 붙인다.
  useEffect(() => {
    folderInput.current?.setAttribute('webkitdirectory', '');
  }, []);

  useEffect(() => {
    const closeOnEscape = (e: KeyboardEvent) => e.key === 'Escape' && !busy && onClose();
    window.addEventListener('keydown', closeOnEscape);
    return () => window.removeEventListener('keydown', closeOnEscape);
  }, [busy, onClose]);

  const pickLocal = (e: React.ChangeEvent<HTMLInputElement>, folder: boolean) => {
    const files = Array.from(e.target.files ?? []);
    e.target.value = '';
    if (files.length === 0) return;
    const folderName = folder ? files[0].webkitRelativePath.split('/')[0] : undefined;
    const root = buildLocalTree(files, folderName);
    setLocalPath([root]);
    // 고른 것이 하나뿐이므로 바로 선택해 둔다.
    setLocalSelected(root.children[0] ?? null);
    setResult(null);
  };

  // 서버 쪽 대상을 고르면 관련 있어 보이는 프로세스를 재시작 대상으로 미리 체크한다.
  const remoteSelectedPath = remoteSelected?.path;
  const remoteSelectedType = remoteSelected ? remoteKind(remoteSelected) : null;
  useEffect(() => {
    if (!remoteSelectedPath || !remoteSelectedType) return;
    setRestartIds(new Set(processes.filter(p => !restartProblem(p) && isRelated(p, server.baseDir, remoteSelectedPath, remoteSelectedType)).map(p => p.id)));
  }, [remoteSelectedPath, remoteSelectedType, processes, server.baseDir]);

  const toggleRestart = (id: string, checked: boolean) =>
    setRestartIds(prev => {
      const next = new Set(prev);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });

  /** 고른 프로세스를 하나씩 재시작한다. 실패해도 나머지는 계속한다. */
  const restartSelected = async (targets: ProcessEntry[]) => {
    const results: RestartResult[] = [];
    for (const p of targets) {
      const name = p.label || p.id;
      setRestarting(name);
      try {
        const response = await requestApi<{ message: string }>(`${PROCESSES_API}/${encodeURIComponent(p.id)}/restart`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: '{}',
        });
        results.push({ name, ok: true, text: response.message });
      } catch (e) {
        results.push({ name, ok: false, text: e instanceof Error ? e.message : String(e) });
      }
      setRestartResults([...results]);
    }
    setRestarting(null);
    onRestarted();
  };

  const localDir = localPath[localPath.length - 1];
  const remoteSelectedKind = remoteSelected ? remoteKind(remoteSelected) : null;
  const mismatch = remoteSelectedKind !== null && localSelected !== null && remoteSelectedKind !== localSelected.kind;
  const ready = remoteSelectedKind !== null && localSelected !== null && !mismatch && !busy;

  const runUpdate = async () => {
    if (!remoteSelected || !localSelected || !remoteSelectedKind) return;
    const name = baseName(remoteSelected.path);
    const kindText = KIND_TEXT[remoteSelectedKind];
    const restartTargets = processes.filter(p => restartIds.has(p.id) && !restartProblem(p));
    if (
      !window.confirm(
        `${serverLabel} 의 ${kindText}\n${remoteSelected.path}\n\n` +
          `내 PC 의 '${localSelected.name}' (${describeLocal(localSelected)}) 로 바꿀까요?\n` +
          `기존 ${kindText}은(는) ${name}.bak.<날짜-시각> 으로 이름을 바꿔 남깁니다.` +
          (localSelected.name !== name ? `\n올라간 ${kindText}의 이름은 서버 쪽 이름(${name})을 따릅니다.` : '') +
          (restartTargets.length > 0
            ? `\n\n업데이트가 끝나면 다음 프로세스를 재시작합니다: ${restartTargets.map(p => p.label || p.id).join(', ')}`
            : '\n\n재시작할 프로세스를 고르지 않았습니다. (프로세스는 그대로 실행됩니다)')
      )
    )
      return;

    setBusy(true);
    setError(null);
    setResult(null);
    setRestartResults([]);
    try {
      const body = buildTar(tarEntries(localSelected, name));
      setProgress({ loaded: 0, total: body.size });
      const query = new URLSearchParams({ path: remoteSelected.path, kind: remoteSelectedKind });
      const updated = await uploadApi<UpdateResult>(`${base}/update?${query}`, body, 'application/x-tar', (loaded, total) =>
        setProgress({ loaded, total })
      );
      setResult(updated);
    } catch (e) {
      // 업데이트가 실패하면 재시작하지 않는다.
      setError(`업데이트하지 못했습니다: ${e instanceof Error ? e.message : String(e)}`);
      setBusy(false);
      setProgress(null);
      return;
    }
    setProgress(null);
    try {
      if (restartTargets.length > 0) await restartSelected(restartTargets);
      await loadRemote(remote?.path ?? '');
    } finally {
      setBusy(false);
    }
  };

  const uploaded = progress !== null && progress.loaded >= progress.total;
  const percent = progress && progress.total > 0 ? Math.floor((progress.loaded / progress.total) * 100) : 0;

  /** 목록의 한 줄: 누르면 선택, 두 번 누르거나 Enter 면 폴더를 연다. */
  const row = (key: string, kind: Kind | null, name: string, selected: boolean, onSelect: () => void, onOpen: (() => void) | null, meta: React.ReactNode) => (
    <li key={key}>
      <button
        className={`program-update-row${selected ? ' program-update-row-selected' : ''}`}
        onClick={onSelect}
        onDoubleClick={() => onOpen?.()}
        onKeyDown={e => {
          if (e.key === 'Enter' && onOpen) {
            e.preventDefault();
            onOpen();
          }
        }}
        disabled={busy}
        title={name}
      >
        <Icon name={kind === 'file' ? 'file' : 'folder'} className={kind ? undefined : 'program-update-muted'} />
        <span className="program-update-name">{name}</span>
        <span className="program-update-meta">{meta}</span>
      </button>
    </li>
  );

  const upRow = (key: string, onOpen: () => void) => (
    <li key={key}>
      <button className="program-update-row" onClick={onOpen} disabled={busy} title="상위 폴더">
        <Icon name="arrow-up" />
        <span className="program-update-name">..</span>
      </button>
    </li>
  );

  return (
    <div className="program-update-backdrop" onMouseDown={e => e.target === e.currentTarget && !busy && onClose()}>
      <div className="program-update" role="dialog" aria-modal="true" aria-label="프로그램 업데이트">
        <header className="program-update-header">
          <div>
            <strong>프로그램 업데이트</strong>
            <span>{serverLabel}</span>
            <span>
              {server.user}@{server.host}
            </span>
          </div>
          <button className="btn-icon" onClick={onClose} disabled={busy} title="닫기 (Esc)">
            <Icon name="close" />
          </button>
        </header>

        <div className="program-update-panes">
          {/* 왼쪽: 서버 */}
          <section className="program-update-pane" aria-label="서버">
            <div className="program-update-pane-title">
              <strong>서버</strong>
              <code title={remote?.path}>{remote?.path ?? '...'}</code>
              <button className="btn-icon" onClick={() => loadRemote(remote?.path ?? '')} disabled={remoteLoading || busy} title="새로고침">
                <Icon name="refresh" />
              </button>
            </div>
            {remoteError && <div className="program-update-error">{remoteError}</div>}
            {remoteLoading && !remote ? (
              <div className="program-update-placeholder">불러오는 중...</div>
            ) : remote ? (
              <ul className="program-update-list">
                {remote.path !== remote.home && upRow('..', () => loadRemote(parentPath(remote.path)))}
                {remote.entries.length === 0 && <li className="program-update-placeholder">빈 폴더입니다.</li>}
                {remote.entries.map(entry => {
                  const kind = remoteKind(entry);
                  return row(
                    entry.path,
                    kind,
                    baseName(entry.path),
                    remoteSelected?.path === entry.path,
                    () => kind && setRemoteSelected(entry),
                    kind === 'dir' ? () => loadRemote(entry.path) : null,
                    kind === 'file' ? `${formatSize(entry.size)} · ${entry.modified}` : kind === 'dir' ? entry.modified : '바꿀 수 없는 항목'
                  );
                })}
              </ul>
            ) : null}
            <small className="program-update-hint">SSH 계정의 홈 폴더 아래만 볼 수 있습니다. 두 번 누르면 폴더를 엽니다.</small>
          </section>

          {/* 오른쪽: 내 PC */}
          <section className="program-update-pane" aria-label="내 PC">
            <div className="program-update-pane-title">
              <strong>내 PC</strong>
              <code title={localPath.slice(1).map(n => n.name).join('/')}>{localPath.length > 1 ? localPath.slice(1).map(n => n.name).join('/') : ''}</code>
              <button className="btn btn-secondary" onClick={() => folderInput.current?.click()} disabled={busy}>
                <Icon name="folder" />
                폴더 열기
              </button>
              <button className="btn btn-secondary" onClick={() => fileInput.current?.click()} disabled={busy}>
                <Icon name="file" />
                파일 열기
              </button>
              <input ref={folderInput} type="file" hidden onChange={e => pickLocal(e, true)} />
              <input ref={fileInput} type="file" hidden onChange={e => pickLocal(e, false)} />
            </div>
            {!localDir ? (
              <div className="program-update-placeholder">
                올릴 폴더나 파일을 여세요. 브라우저가 '업로드할까요?' 를 물으면 허용하세요. (프로그램 업데이트를 누르기 전에는 서버로 보내지 않습니다)
              </div>
            ) : (
              <ul className="program-update-list">
                {localPath.length > 1 && upRow('..', () => setLocalPath(path => path.slice(0, -1)))}
                {localDir.children.length === 0 && <li className="program-update-placeholder">빈 폴더입니다.</li>}
                {localDir.children.map(node =>
                  row(
                    node.name,
                    node.kind,
                    node.name,
                    localSelected === node,
                    () => setLocalSelected(node),
                    node.kind === 'dir' ? () => setLocalPath(path => [...path, node]) : null,
                    node.kind === 'file' && node.file
                      ? `${formatSize(node.bytes)} · ${formatDateTime(node.file.lastModified)}`
                      : `파일 ${node.files.toLocaleString()}개 · ${formatSize(node.bytes)}`
                  )
                )}
              </ul>
            )}
            <small className="program-update-hint">빈 폴더는 올라가지 않습니다. 두 번 누르면 폴더를 엽니다.</small>
          </section>
        </div>

        <footer className="program-update-footer">
          <div className="program-update-summary">
            <div>
              <span>서버</span>
              {remoteSelected && remoteSelectedKind ? (
                <code>
                  {remoteSelected.path} ({KIND_TEXT[remoteSelectedKind]})
                </code>
              ) : (
                <em>바꿀 폴더 · 파일을 고르세요</em>
              )}
            </div>
            <div>
              <span>내 PC</span>
              {localSelected ? (
                <code>
                  {localSelected.name} ({KIND_TEXT[localSelected.kind]}, {describeLocal(localSelected)})
                </code>
              ) : (
                <em>올릴 폴더 · 파일을 고르세요</em>
              )}
            </div>
            {processes.length > 0 && (
              <fieldset className="program-update-restart" disabled={busy}>
                <legend>업데이트 후 재시작</legend>
                {processes.map(p => {
                  const problem = restartProblem(p);
                  return (
                    <label key={p.id} title={problem ?? scriptPath(p.startScript, server.baseDir)}>
                      <input type="checkbox" checked={!problem && restartIds.has(p.id)} disabled={problem !== null} onChange={e => toggleRestart(p.id, e.target.checked)} />
                      {p.label || p.id}
                      {problem && <em> ({problem})</em>}
                    </label>
                  );
                })}
              </fieldset>
            )}
            {mismatch && (
              <div className="program-update-warn">
                폴더는 폴더로, 파일은 파일로만 바꿀 수 있습니다. (서버: {KIND_TEXT[remoteSelectedKind!]}, 내 PC: {KIND_TEXT[localSelected!.kind]})
              </div>
            )}
            {progress && (
              <div className="program-update-progress">
                <progress value={progress.loaded} max={progress.total || 1} />
                <span>
                  {uploaded
                    ? '다 올렸습니다. 서버에서 확인하고 바꾸는 중...'
                    : `올리는 중 ${formatSize(progress.loaded)} / ${formatSize(progress.total)} (${percent}%)`}
                </span>
              </div>
            )}
            {error && <div className="program-update-error">{error}</div>}
            {result && (
              <div className="program-update-ok">
                업데이트했습니다: {result.path} (파일 {result.files.toLocaleString()}개, {formatSize(result.bytes)}). 이전 내용은 {result.backup} 에 있습니다.
                {restartResults.length === 0 && !restarting && ' 재시작한 프로세스는 없습니다. (실행 중이면 다시 시작해야 반영됩니다)'}
              </div>
            )}
            {restarting && <div className="program-update-progress">재시작 중: {restarting}...</div>}
            {restartResults.map(r => (
              <div key={r.name} className={r.ok ? 'program-update-ok' : 'program-update-error'}>
                {r.name} 재시작: {r.text}
              </div>
            ))}
          </div>
          <div className="program-update-actions">
            <button className="btn btn-secondary" onClick={onClose} disabled={busy}>
              닫기
            </button>
            <button className="btn btn-primary" onClick={runUpdate} disabled={!ready}>
              <Icon name="upload" />
              {busy ? (restarting ? '재시작 중...' : uploaded ? '바꾸는 중...' : `올리는 중 ${percent}%`) : '프로그램 업데이트'}
            </button>
          </div>
        </footer>
      </div>
    </div>
  );
};

export default ProgramUpdate;

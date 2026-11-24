import React, { useCallback, useEffect, useState } from 'react';
import Icon from '../Icon/Icon';
import { RemoteDir, RemoteEntry } from '../../types/config';
import { requestApi } from '../../api/client';
import { formatSize } from '../../utils/format';
import './RemoteFilePicker.css';

interface RemoteFilePickerProps {
  /** 저장되어 있는 서버 id (접속 정보는 Go 서버의 설정에서 읽는다) */
  serverId: string;
  serverLabel: string;
  /** 처음 열 폴더. 비었거나 열 수 없으면(홈 밖 등) 홈 폴더를 연다. */
  startDir?: string;
  title: string;
  /** file: 파일 하나를 고른다. dir: 폴더 하나를 고른다. (고르지 않고 '선택' 하면 지금 폴더) */
  mode?: 'file' | 'dir';
  /** file 모드에서 고를 수 있는 파일 이름 (예: .ini 만). 나머지는 흐리게 보이고 고를 수 없다. */
  fileFilter?: (name: string) => boolean;
  /** 고를 수 있는 파일 설명 (예: '.ini 파일') */
  fileFilterText?: string;
  onPick: (path: string) => void;
  onClose: () => void;
}

const SERVERS_API = `${process.env.PUBLIC_URL}/api/servers`;

const baseName = (path: string) => path.slice(path.lastIndexOf('/') + 1);
const parentPath = (path: string) => path.slice(0, path.lastIndexOf('/')) || '/';

/**
 * 서버의 파일(또는 폴더) 하나를 고르는 창. (SSH 계정의 홈 폴더 아래만, GET /api/servers/:id/browse)
 * 파일 모드: 파일을 누르면 선택, 두 번 누르면 바로 고른다. 폴더 모드: 폴더를 누르면 선택, 아무것도 고르지 않으면 지금 폴더.
 * 폴더는 어느 모드든 두 번 누르거나 Enter 로 연다.
 */
const RemoteFilePicker: React.FC<RemoteFilePickerProps> = ({
  serverId,
  serverLabel,
  startDir,
  title,
  mode = 'file',
  fileFilter,
  fileFilterText,
  onPick,
  onClose,
}) => {
  const [dir, setDir] = useState<RemoteDir | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [selected, setSelected] = useState<RemoteEntry | null>(null);

  const load = useCallback(
    async (path: string) => {
      setLoading(true);
      setError(null);
      const url = (p: string) => `${SERVERS_API}/${encodeURIComponent(serverId)}/browse${p ? `?${new URLSearchParams({ path: p })}` : ''}`;
      try {
        setDir(await requestApi<RemoteDir>(url(path)));
        setSelected(null);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        setLoading(false);
      }
    },
    [serverId]
  );

  // 기본 폴더부터 연다. 열 수 없으면 홈 폴더로.
  useEffect(() => {
    const open = async () => {
      if (startDir) {
        try {
          setLoading(true);
          setDir(await requestApi<RemoteDir>(`${SERVERS_API}/${encodeURIComponent(serverId)}/browse?${new URLSearchParams({ path: startDir })}`));
          setLoading(false);
          return;
        } catch (e) {
          setNotice(`기본 폴더(${startDir})를 열 수 없어 홈 폴더를 엽니다: ${e instanceof Error ? e.message : String(e)}`);
        }
      }
      await load('');
    };
    void open();
  }, [serverId, startDir, load]);

  // 설정 창 위에 열리므로 Esc 는 이 창만 닫는다.
  useEffect(() => {
    const closeOnEscape = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      e.stopImmediatePropagation();
      onClose();
    };
    window.addEventListener('keydown', closeOnEscape, { capture: true });
    return () => window.removeEventListener('keydown', closeOnEscape, { capture: true });
  }, [onClose]);

  return (
    <div className="remote-file-picker-backdrop" onMouseDown={e => e.target === e.currentTarget && onClose()}>
      <div className="remote-file-picker" role="dialog" aria-modal="true" aria-label={title}>
        <header className="remote-file-picker-header">
          <div>
            <strong>{title}</strong>
            <span>{serverLabel}</span>
          </div>
          <button className="btn-icon" onClick={onClose} title="닫기 (Esc)">
            <Icon name="close" />
          </button>
        </header>

        <div className="remote-file-picker-path">
          <code title={dir?.path}>{dir?.path ?? '...'}</code>
          <button className="btn-icon" onClick={() => dir && load(dir.path)} disabled={loading || !dir} title="새로고침">
            <Icon name="refresh" />
          </button>
        </div>
        {notice && <div className="remote-file-picker-notice">{notice}</div>}
        {error && <div className="remote-file-picker-error">{error}</div>}

        {loading && !dir ? (
          <div className="remote-file-picker-placeholder">불러오는 중...</div>
        ) : dir ? (
          <ul className="remote-file-picker-list">
            {dir.path !== dir.home && (
              <li>
                <button className="remote-file-picker-row" onClick={() => load(parentPath(dir.path))} title="상위 폴더">
                  <Icon name="arrow-up" />
                  <span className="remote-file-picker-name">..</span>
                </button>
              </li>
            )}
            {dir.entries.length === 0 && <li className="remote-file-picker-placeholder">빈 폴더입니다.</li>}
            {dir.entries.map(entry => {
              const isDir = entry.type === 'dir';
              const isFile = entry.type === 'file';
              // 고를 수 있는 항목: 파일 모드는 (조건에 맞는) 파일, 폴더 모드는 폴더
              const selectable = mode === 'dir' ? isDir : isFile && (!fileFilter || fileFilter(baseName(entry.path)));
              return (
                <li key={entry.path}>
                  <button
                    className={`remote-file-picker-row${selected?.path === entry.path ? ' remote-file-picker-row-selected' : ''}`}
                    onClick={() => selectable && setSelected(entry)}
                    onDoubleClick={() => (isDir ? load(entry.path) : selectable ? onPick(entry.path) : undefined)}
                    onKeyDown={e => {
                      if (e.key === 'Enter' && isDir) {
                        e.preventDefault();
                        void load(entry.path);
                      }
                    }}
                    title={entry.path}
                  >
                    <Icon name={isDir ? 'folder' : 'file'} className={isDir || selectable ? undefined : 'remote-file-picker-muted'} />
                    <span className={`remote-file-picker-name${isDir || selectable ? '' : ' remote-file-picker-muted'}`}>{baseName(entry.path)}</span>
                    <span className="remote-file-picker-meta">{isFile ? `${formatSize(entry.size)} · ${entry.modified}` : entry.modified}</span>
                  </button>
                </li>
              );
            })}
          </ul>
        ) : null}

        <footer className="remote-file-picker-footer">
          <code className="remote-file-picker-selected">
            {selected
              ? selected.path
              : mode === 'dir'
                ? `${dir?.path ?? ''} (지금 폴더. 폴더는 두 번 눌러 엽니다)`
                : `${fileFilterText ?? '파일'}을 고르세요 (폴더는 두 번 눌러 엽니다)`}
          </code>
          <button className="btn btn-secondary" onClick={onClose}>
            취소
          </button>
          <button
            className="btn btn-primary"
            onClick={() => (selected ? onPick(selected.path) : mode === 'dir' && dir ? onPick(dir.path) : undefined)}
            disabled={!selected && !(mode === 'dir' && dir)}
          >
            선택
          </button>
        </footer>
      </div>
    </div>
  );
};

export default RemoteFilePicker;

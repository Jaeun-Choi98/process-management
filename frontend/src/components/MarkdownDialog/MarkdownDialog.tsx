import React, { useEffect, useState } from 'react';
import Icon, { IconName } from '../Icon/Icon';
import './MarkdownDialog.css';

interface MarkdownDialogProps {
  /** public/config 아래의 md 파일 이름 (예: README.md) */
  file: string;
  title: string;
  /** 제목 앞 아이콘 */
  icon?: IconName;
  onClose: () => void;
}

/**
 * README 에 흔히 쓰는 마크다운만 HTML 로 바꾼다.
 * 지원: 제목(#~####), 목록(- / 1.), 코드 블록(```), 인용(>), 표(|), 구분선(---), 문단,
 *       줄 안의 `코드`, **굵게**, [링크](https://...)
 * 원문은 먼저 이스케이프하므로 README 에 HTML 을 써도 그대로 글자로 보인다.
 */
function renderMarkdown(markdown: string): string {
  const escape = (text: string) =>
    text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  const inline = (text: string) =>
    escape(text)
      .replace(/`([^`]+)`/g, '<code>$1</code>')
      .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
      // http(s) · 사이트 내부 경로만 링크로 만든다. (javascript: 등은 글자로 남는다)
      .replace(/\[([^\]]+)\]\(((?:https?:\/\/|\/|#)[^)\s]*)\)/g, '<a href="$2" target="_blank" rel="noreferrer">$1</a>');

  const lines = markdown.replace(/\r\n/g, '\n').split('\n');
  const html: string[] = [];
  let i = 0;
  // pattern 에 맞는 줄이 이어지는 동안 모은다.
  const collect = (pattern: RegExp) => {
    const block: string[] = [];
    while (i < lines.length && pattern.test(lines[i])) block.push(lines[i++]);
    return block;
  };

  while (i < lines.length) {
    const line = lines[i];
    const heading = line.match(/^(#{1,4})\s+(.*)$/);

    if (line.trim() === '') {
      i++;
    } else if (line.startsWith('```')) {
      i++;
      const code = collect(/^(?!```)/);
      i++; // 닫는 ```
      html.push(`<pre><code>${escape(code.join('\n'))}</code></pre>`);
    } else if (heading) {
      i++;
      html.push(`<h${heading[1].length}>${inline(heading[2])}</h${heading[1].length}>`);
    } else if (/^(-{3,}|\*{3,})\s*$/.test(line)) {
      i++;
      html.push('<hr>');
    } else if (/^\s*[-*]\s+/.test(line)) {
      const items = collect(/^\s*[-*]\s+/).map(item => `<li>${inline(item.replace(/^\s*[-*]\s+/, ''))}</li>`);
      html.push(`<ul>${items.join('')}</ul>`);
    } else if (/^\s*\d+\.\s+/.test(line)) {
      const items = collect(/^\s*\d+\.\s+/).map(item => `<li>${inline(item.replace(/^\s*\d+\.\s+/, ''))}</li>`);
      html.push(`<ol>${items.join('')}</ol>`);
    } else if (line.startsWith('>')) {
      const quote = collect(/^>/).map(q => inline(q.replace(/^>\s?/, '')));
      html.push(`<blockquote>${quote.join('<br>')}</blockquote>`);
    } else if (line.startsWith('|')) {
      // 첫 줄은 제목 행, |---|---| 구분 행은 건너뛴다.
      const rows = collect(/^\|/)
        .filter(row => !/^\|[\s:|-]+$/.test(row.trim()))
        .map(row => row.trim().replace(/^\||\|$/g, '').split('|').map(cell => inline(cell.trim())));
      const [head = [], ...body] = rows;
      html.push(
        `<table><thead><tr>${head.map(c => `<th>${c}</th>`).join('')}</tr></thead>` +
          `<tbody>${body.map(r => `<tr>${r.map(c => `<td>${c}</td>`).join('')}</tr>`).join('')}</tbody></table>`
      );
    } else {
      // 문단: 다른 블록이 시작되거나 빈 줄이 나올 때까지 이어 붙인다.
      const paragraph = [lines[i++], ...collect(/^(?!\s*$|#{1,4}\s|```|>|\||\s*[-*]\s+|\s*\d+\.\s+)/)];
      html.push(`<p>${paragraph.map(inline).join(' ')}</p>`);
    }
  }
  return html.join('\n');
}

/** config 폴더의 md 파일(소개, API 명세 등)을 팝업 창으로 보여준다. Esc 나 바깥을 누르면 닫힌다. */
const MarkdownDialog: React.FC<MarkdownDialogProps> = ({ file, title, icon, onClose }) => {
  const url = `${process.env.PUBLIC_URL}/config/${file}`;
  /** null: 읽는 중 */
  const [content, setContent] = useState<{ html: string } | { error: string } | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    fetch(url, { cache: 'no-store', signal: controller.signal })
      .then(async response => {
        // 파일이 없으면 Go 서버는 404 를, 개발 서버는 index.html 을 돌려준다.
        const isHtml = (response.headers.get('content-type') ?? '').includes('text/html');
        setContent(response.ok && !isHtml ? { html: renderMarkdown(await response.text()) } : { error: `안내문(${file})이 없습니다.` });
      })
      .catch(e => {
        if (!controller.signal.aborted) setContent({ error: `안내문을 읽지 못했습니다: ${e instanceof Error ? e.message : String(e)}` });
      });
    return () => controller.abort();
  }, [url, file]);

  useEffect(() => {
    const closeOnEscape = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', closeOnEscape);
    return () => window.removeEventListener('keydown', closeOnEscape);
  }, [onClose]);

  return (
    <div className="markdown-dialog-backdrop" onMouseDown={e => e.target === e.currentTarget && onClose()}>
      <div className="markdown-dialog" role="dialog" aria-modal="true" aria-label={title}>
        <header className="markdown-dialog-header">
          <strong>
            {icon && <Icon name={icon} />}
            {title}
          </strong>
          <button className="btn-icon" onClick={onClose} title="닫기 (Esc)">
            <Icon name="close" />
          </button>
        </header>
        {content === null ? (
          <div className="markdown-dialog-placeholder">불러오는 중...</div>
        ) : 'error' in content ? (
          <div className="markdown-dialog-placeholder">{content.error}</div>
        ) : (
          <div className="markdown-dialog-body" dangerouslySetInnerHTML={{ __html: content.html }} />
        )}
      </div>
    </div>
  );
};

export default MarkdownDialog;

package processes

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
)

// 프로그램 업데이트: 서버의 폴더(또는 파일)를 브라우저 PC 에서 고른 폴더(또는 파일)로 바꾼다.
//
//  1. 화면이 고른 내용을 tar 로 묶어 올린다. 이름은 서버 쪽 대상 이름으로 맞춘다. (app → app/...)
//  2. Go 서버는 tar 를 한 항목씩 확인(대상 이름 아래의 폴더 · 일반 파일만)해 다시 묶으며 SSH 로 흘려보내고,
//     서버는 대상과 같은 폴더의 임시 폴더(.app.update.XXXXXX)에 푼다.
//  3. 끝까지 받은 것을 확인한 뒤에만 대상을 'app.bak.20261124-153012' 로 바꾸고 새 내용을 'app' 으로 옮긴다.
//     옮기지 못하면 원래 이름으로 되돌린다. 중간에 실패하면 임시 폴더만 지우고 대상은 건드리지 않는다.
//
// 서버 쪽은 SSH 계정의 홈 폴더 아래만 둘러보고 바꿀 수 있다. (심볼릭 링크를 따라간 실제 경로 기준)
const (
	UpdateDir  = "dir"
	UpdateFile = "file"
	// 큰 프로그램 폴더도 올릴 수 있게 길게 둔다.
	updateTimeout = 30 * time.Minute
	// 받은 파일의 권한. 실행 권한은 같은 경로의 기존 파일을 따른다. (서버의 umask 가 적용된다)
	updateFileMode = 0o644
	updateDirMode  = 0o755
)

// RemoteDir 은 서버의 폴더 하나의 목록이다.
type RemoteDir struct {
	// SSH 계정의 홈 폴더 (실제 경로). 이 위로는 올라갈 수 없다.
	Home string `json:"home"`
	// 지금 폴더 (실제 경로)
	Path string `json:"path"`
	// 폴더 먼저, 이름 순. 심볼릭 링크는 가리키는 대상의 종류로 보인다.
	Entries []FileEntry `json:"entries"`
}

// UpdateResult 는 업데이트 결과다.
type UpdateResult struct {
	// 바뀐 대상 (실제 경로)
	Path string `json:"path"`
	// 이전 내용을 옮겨 둔 경로
	Backup string `json:"backup"`
	// 받은 파일 수 · 크기
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

// 서버에서 실행하는 스크립트는 고정이고, 값은 'set --' 의 인자($1, $2 ...)로만 넘긴다.
const (
	// $1: 열 폴더 (비어 있으면 홈)
	browseScript = `fail() { echo "$1" >&2; exit 1; }
home=$(cd ~ && pwd -P) || fail '홈 폴더를 찾지 못했습니다'
d=$(cd -- "${1:-$home}" 2>/dev/null && pwd -P) || fail '폴더를 열 수 없습니다 (없거나 권한이 없습니다)'
case "$d/" in "$home"/*) ;; *) fail '홈 폴더 밖은 열 수 없습니다' ;; esac
printf '%s\n%s\n' "$home" "$d"
LC_ALL=C ls -lnAL -- "$d" 2>/dev/null
exit 0`

	// $1: 대상 경로, $2: dir · file. 표준 입력의 tar 를 대상 옆 임시 폴더에 풀고 그 경로를 출력한다.
	updatePrepareScript = `t=$1 kind=$2
fail() { echo "$1" >&2; exit 1; }
home=$(cd ~ && pwd -P) || fail '홈 폴더를 찾지 못했습니다'
n=${t##*/}
p=$(cd -- "${t%/*}/" 2>/dev/null && pwd -P) || fail '대상이 있는 폴더를 열 수 없습니다'
case "$p/" in "$home"/*) ;; *) fail '홈 폴더 밖은 바꿀 수 없습니다' ;; esac
dst=$p/$n
[ -L "$dst" ] && fail '심볼릭 링크는 바꿀 수 없습니다'
if [ "$kind" = dir ]; then
  [ -d "$dst" ] || fail '서버 쪽 대상이 폴더가 아닙니다'
else
  [ -f "$dst" ] || fail '서버 쪽 대상이 파일이 아닙니다'
fi
[ -w "$p" ] || fail "$p 에 쓰기 권한이 없습니다"
tmp=$(mktemp -d "$p/.$n.update.XXXXXX") || fail '임시 폴더를 만들지 못했습니다'
trap 'rm -rf -- "$tmp"' EXIT
tar --no-same-owner -xf - -C "$tmp" || fail '받은 내용을 풀지 못했습니다'
new=$tmp/$n
[ "$(ls -A -- "$tmp")" = "$n" ] || fail '받은 내용이 올바르지 않습니다'
if [ "$kind" = dir ]; then
  [ -d "$new" ] || fail '받은 내용이 폴더가 아닙니다'
  # 기존에 실행 권한이 있던 파일은 새 파일에도 실행 권한을 준다.
  (cd -- "$new" && find . -type f -exec sh -c 'for f; do [ -x "$0/$f" ] && chmod +x "$f"; done; exit 0' "$dst" {} +) || fail '실행 권한을 맞추지 못했습니다'
else
  [ -f "$new" ] || fail '받은 내용이 파일이 아닙니다'
  if [ -x "$dst" ]; then chmod +x "$new" || fail '실행 권한을 맞추지 못했습니다'; fi
fi
trap - EXIT
printf '%s\n' "$tmp"`

	// $1: 대상 경로(실제 경로), $2: 임시 폴더, $3: 백업 이름 뒤에 붙일 시각
	updateSwapScript = `dst=$1 tmp=$2 ts=$3
new=$tmp/${dst##*/}
bak=$dst.bak.$ts
fail() { rm -rf -- "$tmp"; echo "$1" >&2; exit 1; }
[ -e "$bak" ] || [ -L "$bak" ] && fail "$bak 이(가) 이미 있습니다"
mv -- "$dst" "$bak" || fail '기존 대상의 이름을 바꾸지 못했습니다'
if ! mv -- "$new" "$dst"; then
  mv -- "$bak" "$dst"
  fail '새 내용을 옮기지 못해 기존 대상을 되돌렸습니다'
fi
rmdir -- "$tmp"
printf '%s\n' "$bak"`

	// $1: 대상 경로. 대상 옆에 남은 임시 폴더를 지운다. (같은 대상의 업데이트는 동시에 하나뿐이다)
	updateCleanupScript = `t=$1
p=$(cd -- "${t%/*}/" 2>/dev/null && pwd -P) || exit 0
rm -rf -- "$p/.${t##*/}.update."*`
)

// BrowseRemote 는 서버의 폴더 목록을 돌려준다. dir 이 비어 있으면 SSH 계정의 홈 폴더를 연다.
func (m *Monitor) BrowseRemote(ctx context.Context, serverID, dir string) (*RemoteDir, error) {
	server, err := m.findServer(serverID)
	if err != nil {
		return nil, err
	}
	if dir != "" {
		if err := checkRemotePath(dir); err != nil {
			return nil, err
		}
	}

	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := m.remote.run(ctx, server, scriptWithArgs(browseScript, dir))
	if err != nil {
		return nil, err
	}
	lines := strings.SplitN(output, "\n", 3)
	if len(lines) < 3 {
		return nil, fmt.Errorf("폴더 목록을 읽지 못했습니다: %q", output)
	}
	result := &RemoteDir{Home: lines[0], Path: lines[1], Entries: []FileEntry{}}
	for _, entry := range parseLS(lines[2]) {
		entry.Path = path.Join(result.Path, entry.Path)
		result.Entries = append(result.Entries, entry)
	}
	sort.SliceStable(result.Entries, func(i, j int) bool {
		a, b := result.Entries[i], result.Entries[j]
		if (a.Type == "dir") != (b.Type == "dir") {
			return a.Type == "dir"
		}
		return a.Path < b.Path
	})
	return result, nil
}

// UpdateRemote 는 서버의 target(kind: dir · file)을 body 의 tar 내용으로 바꾼다.
// body 의 항목은 모두 target 의 이름(폴더면 그 아래)이어야 한다. size 는 요청 본문 길이(모르면 -1)로,
// 끝까지 받았는지 확인하는 데 쓴다.
func (m *Monitor) UpdateRemote(ctx context.Context, serverID, target, kind string, body io.Reader, size int64) (*UpdateResult, error) {
	server, err := m.findServer(serverID)
	if err != nil {
		return nil, err
	}
	if kind != UpdateDir && kind != UpdateFile {
		return nil, &UserError{http.StatusBadRequest, fmt.Sprintf("알 수 없는 종류입니다: %s", kind)}
	}
	if err := checkRemotePath(target); err != nil {
		return nil, err
	}
	if target == "/" {
		return nil, &UserError{http.StatusBadRequest, "바꿀 대상을 골라야 합니다"}
	}

	// 같은 대상을 동시에 바꾸지 않는다.
	key := "update:" + serverID + ":" + target
	m.mu.Lock()
	if m.busy[key] {
		m.mu.Unlock()
		return nil, &UserError{http.StatusConflict, "이 대상을 업데이트하는 중입니다"}
	}
	m.busy[key] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.busy, key)
		m.mu.Unlock()
	}()

	// 받은 tar 를 확인하며 다시 묶어 서버로 흘려보낸다.
	reader, writer := io.Pipe()
	result := &UpdateResult{}
	copied := make(chan error, 1)
	go func() {
		err := repackUpdate(body, writer, path.Base(target), kind, size, result)
		writer.CloseWithError(err)
		copied <- err
	}()

	prepareCtx, cancel := context.WithTimeout(ctx, updateTimeout)
	output, err := m.remote.runReader(prepareCtx, server, scriptWithArgs(updatePrepareScript, target, kind), reader)
	cancel()
	// 서버가 먼저 끝났으면(오류) 남은 본문을 기다리지 않도록 파이프를 닫는다.
	reader.Close()
	uploadErr := <-copied

	tmp := strings.TrimSuffix(output, "\n")
	// 브라우저가 끊어도 정리 · 바꾸기는 끝까지 한다.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commandTimeout)
	defer cancel()
	if uploadErr != nil || err != nil {
		// 서버 쪽이 끝까지 풀었더라도 올린 내용에 문제가 있으면 임시 폴더를 지운다.
		m.remote.run(finishCtx, server, scriptWithArgs(updateCleanupScript, target))
	}
	var userErr *UserError
	switch {
	case errors.As(uploadErr, &userErr):
		return nil, uploadErr
	case err != nil:
		// 서버가 먼저 실패하면 올리던 쪽은 '닫힌 파이프' 로 끝나므로 서버의 이유를 보여준다.
		return nil, fmt.Errorf("업데이트하지 못했습니다: %w", err)
	case uploadErr != nil:
		return nil, fmt.Errorf("올리는 중에 끊겼습니다: %w", uploadErr)
	}

	// 임시 폴더는 실제 대상 폴더 안의 '.<이름>.update.XXXXXX' 이다.
	dir, name := path.Dir(tmp), path.Base(target)
	if !strings.HasPrefix(path.Base(tmp), "."+name+".update.") || checkRemotePath(tmp) != nil {
		return nil, fmt.Errorf("임시 폴더 경로가 올바르지 않습니다: %q", output)
	}
	result.Path = path.Join(dir, name)
	output, err = m.remote.run(finishCtx, server, scriptWithArgs(updateSwapScript, result.Path, tmp, time.Now().Format("20060102-150405")))
	if err != nil {
		return nil, fmt.Errorf("업데이트하지 못했습니다: %w", err)
	}
	result.Backup = strings.TrimSuffix(output, "\n")
	return result, nil
}

// repackUpdate 는 화면이 올린 tar 를 한 항목씩 확인해 w 에 다시 묶는다.
//   - 항목은 name(파일) 이거나 name/ 아래(폴더)여야 하고, '..' 나 절대 경로는 안 된다.
//   - 폴더 · 일반 파일만 받는다. 소유자는 비우고 권한은 정해진 값으로 바꾼다.
//   - size 를 알면 본문을 끝까지(그 길이만큼) 받았는지 확인한다. (중간에 끊겨 일부만 올라가지 않도록)
func repackUpdate(body io.Reader, w io.Writer, name, kind string, size int64, result *UpdateResult) error {
	counter := &countingReader{r: body}
	in, out := tar.NewReader(counter), tar.NewWriter(w)
	invalid := func(format string, args ...any) error {
		return &UserError{http.StatusBadRequest, "올린 내용이 올바르지 않습니다: " + fmt.Sprintf(format, args...)}
	}
	hasRoot := false
	for {
		header, err := in.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		entry := strings.TrimSuffix(header.Name, "/")
		if entry == "" || path.Clean(entry) != entry || strings.HasPrefix(entry, "/") || entry == ".." || strings.HasPrefix(entry, "../") {
			return invalid("경로 %q", header.Name)
		}
		if entry != name && (kind == UpdateFile || !strings.HasPrefix(entry, name+"/")) {
			return invalid("%q 는 %q 아래가 아닙니다", header.Name, name)
		}
		clean := &tar.Header{Name: entry, ModTime: header.ModTime, Format: tar.FormatPAX}
		switch header.Typeflag {
		case tar.TypeDir:
			if kind == UpdateFile {
				return invalid("파일 대신 폴더 %q", header.Name)
			}
			clean.Typeflag, clean.Mode, clean.Name = tar.TypeDir, updateDirMode, entry+"/"
		case tar.TypeReg:
			if kind == UpdateDir && entry == name {
				return invalid("폴더 대신 파일 %q", header.Name)
			}
			clean.Typeflag, clean.Mode, clean.Size = tar.TypeReg, updateFileMode, header.Size
			result.Files++
			result.Bytes += header.Size
		default:
			return invalid("폴더 · 파일이 아닌 항목 %q", header.Name)
		}
		hasRoot = hasRoot || entry == name
		if err := out.WriteHeader(clean); err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			return err
		}
	}
	if kind == UpdateFile && result.Files != 1 || kind == UpdateDir && !hasRoot {
		return invalid("%q 이(가) 없습니다", name)
	}
	// 끝 표시 뒤에 남은 채움 부분까지 읽어 전체 길이를 맞춰 본다.
	if _, err := io.Copy(io.Discard, counter); err != nil {
		return err
	}
	if size >= 0 && counter.n != size {
		return fmt.Errorf("%d 바이트 중 %d 바이트만 받았습니다", size, counter.n)
	}
	return out.Close()
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (m *Monitor) findServer(serverID string) (Server, error) {
	config, err := m.store.Load()
	if err != nil {
		return Server{}, err
	}
	for _, s := range config.Servers {
		if s.ID == serverID {
			return s, nil
		}
	}
	return Server{}, &UserError{http.StatusNotFound, fmt.Sprintf("'%s' 서버가 없습니다", serverID)}
}

// checkRemotePath 는 화면이 보낸 서버 경로를 확인한다. 작은따옴표로 감싸 명령에 넣으므로
// 작은따옴표와 제어 문자는 거부한다. (홈 폴더 밖인지는 서버에서 실제 경로로 확인한다)
func checkRemotePath(p string) error {
	invalid := &UserError{http.StatusBadRequest, fmt.Sprintf("경로가 올바르지 않습니다: %s", p)}
	if !strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return invalid
	}
	for _, r := range p {
		if r == '\'' || r < 0x20 || r == 0x7f {
			return invalid
		}
	}
	return nil
}

// scriptWithArgs 는 고정 스크립트 앞에 값을 위치 인자로 넣는다. 값은 checkRemotePath 등으로 작은따옴표가 없음을 확인한 것이다.
func scriptWithArgs(script string, args ...string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + arg + "'"
	}
	return "set -- " + strings.Join(quoted, " ") + "\n" + script
}

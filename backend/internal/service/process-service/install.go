package processes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// 프로그램 설치: 아무것도 없는 서버에 브라우저 PC 의 폴더를 새로 올리고, 프로세스로 등록한다.
//
//  1. 업데이트와 같이 화면이 묶은 tar 를 확인하며 다시 묶어 대상 옆 임시 폴더에 푼다.
//  2. 실행 권한: 브라우저는 파일 권한을 보낼 수 없으므로 *.sh 와 고른 시작 파일에 실행 권한을 준다.
//  3. 끝까지 받은 것을 확인한 뒤 임시 폴더를 대상 이름으로 옮긴다. 대상이 이미 있으면 설치하지 않는다. (업데이트를 쓴다)
//  4. 설정에 프로세스를 더한다. (시작 파일은 기본 폴더 아래면 상대 경로) 실행 판단 조건은 화면이 시작한 뒤 정한다.
//
// 설치 위치 · 이름 검사는 올리기 전에 먼저 한다. (다 올린 뒤에 이름 때문에 실패하지 않도록)

// 설치 폴더 이름: 설정 경로 규칙(영문 · 숫자 · . _ -)에 맞고 '.' 로 시작하지 않는다.
var installNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*$`)

type InstallResult struct {
	// 설치한 폴더 (실제 경로)
	Path string `json:"path"`
	// 등록한 프로세스 id
	ProcessID string `json:"processId"`
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"`
}

// InstallRequest 는 설치 위치와 등록할 프로세스 정보다.
type InstallRequest struct {
	// 설치할 폴더 (절대 경로, 아직 없어야 한다)
	Target string
	// 설치 폴더 안의 시작 파일 (상대 경로, 비우면 시작 파일 없이 등록)
	StartFile string
	// 프로세스 표시 이름 (비우면 폴더 이름)
	Label string
}

const (
	// $1: 대상 경로, $2: 시작 파일(대상 안 상대 경로, 비어 있을 수 있음). 표준 입력의 tar 를 대상 옆 임시 폴더에 풀고 그 경로를 출력한다.
	installPrepareScript = `t=$1 start=$2
fail() { echo "$1" >&2; exit 1; }
home=$(cd ~ && pwd -P) || fail '홈 폴더를 찾지 못했습니다'
n=${t##*/}
p=$(cd -- "${t%/*}/" 2>/dev/null && pwd -P) || fail '설치할 위치의 폴더가 없습니다'
case "$p/" in "$home"/*) ;; *) fail '홈 폴더 밖에는 설치할 수 없습니다' ;; esac
dst=$p/$n
if [ -e "$dst" ] || [ -L "$dst" ]; then fail "$dst 이(가) 이미 있습니다. 바꾸려면 프로그램 업데이트를 쓰세요"; fi
[ -w "$p" ] || fail "$p 에 쓰기 권한이 없습니다"
tmp=$(mktemp -d "$p/.$n.update.XXXXXX") || fail '임시 폴더를 만들지 못했습니다'
trap 'rm -rf -- "$tmp"' EXIT
tar --no-same-owner -xf - -C "$tmp" || fail '받은 내용을 풀지 못했습니다'
new=$tmp/$n
[ "$(ls -A -- "$tmp")" = "$n" ] && [ -d "$new" ] || fail '받은 내용이 올바르지 않습니다'
find "$new" -type f -name '*.sh' -exec chmod +x {} + || fail '실행 권한을 주지 못했습니다'
if [ -n "$start" ]; then
  [ -f "$new/$start" ] || fail "시작 파일($start)이 올린 폴더에 없습니다"
  chmod +x "$new/$start" || fail '실행 권한을 주지 못했습니다'
fi
trap - EXIT
printf '%s\n' "$tmp"`

	// $1: 대상 경로(실제 경로), $2: 임시 폴더
	installFinishScript = `dst=$1 tmp=$2
fail() { rm -rf -- "$tmp"; echo "$1" >&2; exit 1; }
if [ -e "$dst" ] || [ -L "$dst" ]; then fail "$dst 이(가) 이미 있습니다"; fi
mv -- "$tmp/${dst##*/}" "$dst" || fail '설치 폴더로 옮기지 못했습니다'
rmdir -- "$tmp"`
)

// InstallRemote 는 body(tar)를 서버의 req.Target 에 새로 설치하고 프로세스로 등록한다.
func (m *Monitor) InstallRemote(ctx context.Context, serverID string, req InstallRequest, body io.Reader, size int64) (*InstallResult, error) {
	server, err := m.findServer(serverID)
	if err != nil {
		return nil, err
	}
	process, err := installProcess(server, req)
	if err != nil {
		return nil, err
	}
	// 등록할 수 있는지 먼저 본다. (다 올린 뒤에 막히지 않도록)
	if _, _, err := m.store.AddProcess(process, true); err != nil {
		return nil, err
	}

	key := "update:" + serverID + ":" + req.Target
	m.mu.Lock()
	if m.busy[key] {
		m.mu.Unlock()
		return nil, &UserError{http.StatusConflict, "이 위치에 설치하는 중입니다"}
	}
	m.busy[key] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.busy, key)
		m.mu.Unlock()
	}()

	name := path.Base(req.Target)
	reader, writer := io.Pipe()
	upload := &UpdateResult{}
	copied := make(chan error, 1)
	go func() {
		err := repackUpdate(body, writer, name, UpdateDir, size, upload)
		writer.CloseWithError(err)
		copied <- err
	}()

	prepareCtx, cancel := context.WithTimeout(ctx, updateTimeout)
	output, err := m.remote.runReader(prepareCtx, server, scriptWithArgs(installPrepareScript, req.Target, req.StartFile), reader)
	cancel()
	reader.Close()
	uploadErr := <-copied

	tmp := strings.TrimSuffix(output, "\n")
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commandTimeout)
	defer cancel()
	if uploadErr != nil || err != nil {
		m.remote.run(finishCtx, server, scriptWithArgs(updateCleanupScript, req.Target))
	}
	var userErr *UserError
	switch {
	case errors.As(uploadErr, &userErr):
		return nil, uploadErr
	case err != nil:
		return nil, fmt.Errorf("설치하지 못했습니다: %w", err)
	case uploadErr != nil:
		return nil, fmt.Errorf("올리는 중에 끊겼습니다: %w", uploadErr)
	}
	if !strings.HasPrefix(path.Base(tmp), "."+name+".update.") || checkRemotePath(tmp) != nil {
		return nil, fmt.Errorf("임시 폴더 경로가 올바르지 않습니다: %q", output)
	}
	installed := path.Join(path.Dir(tmp), name)
	if _, err := m.remote.run(finishCtx, server, scriptWithArgs(installFinishScript, installed, tmp)); err != nil {
		return nil, fmt.Errorf("설치하지 못했습니다: %w", err)
	}

	_, id, err := m.store.AddProcess(process, false)
	if err != nil {
		return nil, fmt.Errorf("%s 에 설치했지만 프로세스를 등록하지 못했습니다 (설정에서 추가하세요): %w", installed, err)
	}
	return &InstallResult{Path: installed, ProcessID: id, Files: upload.Files, Bytes: upload.Bytes}, nil
}

// installProcess 는 설치 요청을 확인하고 등록할 프로세스를 만든다.
// 시작 파일은 설치 폴더가 기본 폴더 아래면 기본 폴더 기준 상대 경로로 적는다.
func installProcess(server Server, req InstallRequest) (Process, error) {
	if err := checkRemotePath(req.Target); err != nil {
		return Process{}, err
	}
	name := path.Base(req.Target)
	if !installNamePattern.MatchString(name) || !pathPattern.MatchString(req.Target) {
		return Process{}, &UserError{http.StatusBadRequest, "설치 폴더 이름은 영문 · 숫자 · . _ - 로 적어야 합니다"}
	}
	process := Process{ID: strings.ToLower(name), Server: server.ID, Label: strings.TrimSpace(req.Label)}
	if process.Label == "" {
		process.Label = name
	}
	if req.StartFile != "" {
		if !relativePathPattern.MatchString(req.StartFile) || path.Clean(req.StartFile) != req.StartFile || req.StartFile == ".." || strings.HasPrefix(req.StartFile, "../") {
			return Process{}, &UserError{http.StatusBadRequest, "시작 파일 경로는 영문 · 숫자 · . _ - / 로 된 설치 폴더 안의 경로여야 합니다"}
		}
		script := req.Target + "/" + req.StartFile
		if base := server.baseDir(); strings.HasPrefix(script, base+"/") {
			script = strings.TrimPrefix(script, base+"/")
		}
		process.StartScript = script
	}
	return process, nil
}

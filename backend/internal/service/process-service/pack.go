package processes

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

// 업데이트 · 설치에서 서버로 보내는 내용
//
//	Source 가 항목을 대상 이름(name) 아래로 하나씩 확인해 tar 로 묶고, gzip 으로 압축해 SSH 로 흘려보낸다.
//	서버는 대상 옆 임시 폴더에 'tar -xzf -' 로 푼다. (gzip 은 거의 모든 리눅스에 기본으로 있다)
//	내용은 화면이 묶은 tar(BrowserTar) 이거나, 미리 올려 둔 압축 파일(Upload.Source)이다.

// Source 는 서버로 보낼 항목을 p 에 더한다.
type Source func(p *packer) error

// packer 는 항목을 확인해 tar 에 쓴다.
//   - 항목은 name(파일) 이거나 name/ 아래(폴더)여야 하고, '..' 나 절대 경로는 안 된다.
//   - 폴더 · 일반 파일 · (압축 파일만) 폴더 안을 가리키는 심볼릭 링크만 받는다. 소유자는 비운다.
//   - 권한: 폴더 755, 파일 644. 압축 파일에 실행 권한이 있던 파일은 755. (서버의 umask 가 적용된다)
type packer struct {
	tw     *tar.Writer
	name   string
	kind   string
	result *UpdateResult
	// 대상 이름의 폴더(또는 파일) 항목이 있었는지
	hasRoot bool
}

func invalidContent(format string, args ...any) error {
	return &UserError{http.StatusBadRequest, "올린 내용이 올바르지 않습니다: " + fmt.Sprintf(format, args...)}
}

// cleanEntry 는 압축 파일 · tar 안의 경로를 정리한다. ("./a/b/" → "a/b") 올바르지 않으면 ok 가 false 다.
func cleanEntry(name string) (string, bool) {
	entry := strings.TrimSuffix(strings.TrimPrefix(name, "./"), "/")
	ok := entry != "" && entry != "." && path.Clean(entry) == entry && !strings.HasPrefix(entry, "/") &&
		entry != ".." && !strings.HasPrefix(entry, "../")
	return entry, ok
}

// add 는 항목 하나를 확인해 쓴다. typ: tar.TypeDir · TypeReg · TypeSymlink, exec: 실행 권한(파일), link: 링크가 가리키는 곳.
func (p *packer) add(entry string, typ byte, size int64, exec bool, link string, modified time.Time, content io.Reader) error {
	if entry != p.name && (p.kind == UpdateFile || !strings.HasPrefix(entry, p.name+"/")) {
		return invalidContent("%q 는 %q 아래가 아닙니다", entry, p.name)
	}
	header := &tar.Header{Name: entry, ModTime: modified, Format: tar.FormatPAX}
	switch typ {
	case tar.TypeDir:
		if p.kind == UpdateFile {
			return invalidContent("파일 대신 폴더 %q", entry)
		}
		header.Typeflag, header.Mode, header.Name = tar.TypeDir, updateDirMode, entry+"/"
	case tar.TypeReg:
		if p.kind == UpdateDir && entry == p.name {
			return invalidContent("폴더 대신 파일 %q", entry)
		}
		header.Typeflag, header.Mode, header.Size = tar.TypeReg, updateFileMode, size
		if exec {
			header.Mode = updateDirMode
		}
		p.result.Files++
		p.result.Bytes += size
	case tar.TypeSymlink:
		// 링크는 폴더 안의 다른 항목만 가리킬 수 있다. (서버의 다른 곳을 가리키는 링크로 바꿔치기하지 않도록)
		resolved := path.Clean(path.Join(path.Dir(entry), link))
		if link == "" || strings.HasPrefix(link, "/") || entry == p.name || !strings.HasPrefix(resolved, p.name+"/") {
			return invalidContent("폴더 밖을 가리키는 링크 %q -> %q", entry, link)
		}
		header.Typeflag, header.Mode, header.Linkname = tar.TypeSymlink, 0o777, link
	default:
		return invalidContent("폴더 · 파일이 아닌 항목 %q", entry)
	}
	p.hasRoot = p.hasRoot || entry == p.name
	if err := p.tw.WriteHeader(header); err != nil {
		return err
	}
	if typ == tar.TypeReg {
		if _, err := io.CopyN(p.tw, content, size); err != nil {
			return fmt.Errorf("%s 을(를) 읽지 못했습니다: %w", entry, err)
		}
	}
	return nil
}

// BrowserTar 는 화면이 묶어 올린 tar 다. 폴더 · 일반 파일만 받고, 권한은 정해진 값으로 바꾼다.
// size 를 알면(-1 이 아니면) 본문을 끝까지(그 길이만큼) 받았는지 확인한다. (중간에 끊겨 일부만 올라가지 않도록)
func BrowserTar(body io.Reader, size int64) Source {
	return func(p *packer) error {
		counter := &countingReader{r: body}
		in := tar.NewReader(counter)
		for {
			header, err := in.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			entry, ok := cleanEntry(header.Name)
			if !ok {
				return invalidContent("경로 %q", header.Name)
			}
			if header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeReg {
				return invalidContent("폴더 · 파일이 아닌 항목 %q", header.Name)
			}
			if err := p.add(entry, header.Typeflag, header.Size, false, "", header.ModTime, in); err != nil {
				return err
			}
		}
		// 끝 표시 뒤에 남은 채움 부분까지 읽어 전체 길이를 맞춰 본다.
		if _, err := io.Copy(io.Discard, counter); err != nil {
			return err
		}
		if size >= 0 && counter.n != size {
			return fmt.Errorf("%d 바이트 중 %d 바이트만 받았습니다", size, counter.n)
		}
		return nil
	}
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

// sendPacked 는 src 의 내용을 확인 · 압축해 서버의 prepare 스크립트(표준 입력으로 받아 임시 폴더에 푸는)로 보내고,
// 그 임시 폴더 경로를 돌려준다. 실패하면 대상 옆에 남은 임시 폴더를 지운다. what 은 오류 문구용("업데이트" · "설치")이다.
func (m *Monitor) sendPacked(ctx context.Context, server Server, prepare string, target, kind, what string, src Source) (string, *UpdateResult, error) {
	name := path.Base(target)
	reader, writer := io.Pipe()
	result := &UpdateResult{}
	copied := make(chan error, 1)
	go func() {
		// 네트워크가 느린 경우가 많으므로 빠른 압축으로 크기만 줄인다.
		gz, _ := gzip.NewWriterLevel(writer, gzip.BestSpeed)
		p := &packer{tw: tar.NewWriter(gz), name: name, kind: kind, result: result}
		err := src(p)
		if err == nil && (p.kind == UpdateFile && result.Files != 1 || p.kind == UpdateDir && !p.hasRoot) {
			err = invalidContent("%q 이(가) 없습니다", name)
		}
		if err == nil {
			err = p.tw.Close()
		}
		if err == nil {
			err = gz.Close()
		}
		writer.CloseWithError(err)
		copied <- err
	}()

	prepareCtx, cancel := context.WithTimeout(ctx, updateTimeout)
	output, err := m.remote.runReader(prepareCtx, server, prepare, reader)
	cancel()
	// 서버가 먼저 끝났으면(오류) 남은 내용을 기다리지 않도록 파이프를 닫는다.
	reader.Close()
	uploadErr := <-copied

	if uploadErr != nil || err != nil {
		// 서버 쪽이 끝까지 풀었더라도 보낸 내용에 문제가 있으면 임시 폴더를 지운다. (브라우저가 끊어도 정리는 끝까지)
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commandTimeout)
		m.remote.run(cleanupCtx, server, scriptWithArgs(updateCleanupScript, target))
		cancel()
	}
	var userErr *UserError
	switch {
	case errors.As(uploadErr, &userErr):
		return "", nil, uploadErr
	case err != nil:
		// 서버가 먼저 실패하면 보내던 쪽은 '닫힌 파이프' 로 끝나므로 서버의 이유를 보여준다.
		return "", nil, fmt.Errorf("%s하지 못했습니다: %w", what, err)
	case uploadErr != nil:
		return "", nil, fmt.Errorf("보내는 중에 끊겼습니다: %w", uploadErr)
	}

	// 임시 폴더는 실제 대상 폴더 안의 '.<이름>.update.XXXXXX' 이다.
	tmp := strings.TrimSuffix(output, "\n")
	if !strings.HasPrefix(path.Base(tmp), "."+name+".update.") || checkRemotePath(tmp) != nil {
		return "", nil, fmt.Errorf("임시 폴더 경로가 올바르지 않습니다: %q", output)
	}
	return tmp, result, nil
}

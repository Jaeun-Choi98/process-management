package processes

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 압축 파일로 설치 · 업데이트
//
//	화면이 압축 파일(.zip · .tar.gz · .tgz · .tar)을 그대로 올리면 Go 서버의 임시 폴더(env.ini [UPLOAD] DIR)에 받아 두고
//	안의 목록을 돌려준다. (시작 파일 고르기용) 설치 · 업데이트를 누르면 압축을 풀며 항목을 확인해 서버로 보낸다.
//	  - 맨 위가 폴더 하나뿐이면(myapp-1.2/...) 그 폴더를 벗겨 설치 · 업데이트 폴더 이름 아래에 둔다.
//	  - 실행 권한은 압축 파일에 담긴 것을 따른다. 폴더 안을 가리키는 심볼릭 링크는 그대로 둔다.
//	  - macOS 가 압축할 때 넣는 정보(__MACOSX/, ._<이름>, .DS_Store)는 뺀다.
//	받아 둔 파일은 쓰고 나면 지우고, 쓰지 않으면 uploadTTL 뒤에 지운다. (Go 서버를 다시 켜면 모두 지운다)
const (
	uploadTTL = time.Hour
	// 압축을 푼 크기의 한계 (압축 폭탄 방지): 받은 크기의 이 배수
	maxExpandRatio = 50
)

// Upload 는 받아 둔 압축 파일이다.
type Upload struct {
	ID string `json:"id"`
	// 올린 파일 이름
	Name string `json:"name"`
	// zip · tar.gz · tar
	Format string `json:"format"`
	// 벗겨 낼 맨 위 폴더 (없으면 "")
	Root string `json:"root,omitempty"`
	// Root 를 벗긴 경로. 폴더는 따로 들어 있지 않을 수 있다.
	Entries []UploadEntry `json:"entries"`
	Files   int           `json:"files"`
	// 압축을 푼 크기
	Bytes int64 `json:"bytes"`

	file    string
	created time.Time
}

type UploadEntry struct {
	Path string `json:"path"`
	// dir · file · link
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
	// 실행 권한이 있는 파일
	Exec bool `json:"exec,omitempty"`
	// 링크가 가리키는 곳
	Target string `json:"target,omitempty"`
}

// Uploads 는 받아 둔 압축 파일을 관리한다.
type Uploads struct {
	dir      string
	maxBytes int64

	mu    sync.Mutex
	items map[string]*Upload
}

// NewUploads 는 전에 남은 파일을 지운다. 폴더는 처음 받을 때 만든다.
func NewUploads(dir string, maxBytes int64) *Uploads {
	leftovers, _ := filepath.Glob(filepath.Join(dir, "upload-*"))
	for _, name := range leftovers {
		os.Remove(name)
	}
	return &Uploads{dir: dir, maxBytes: maxBytes, items: map[string]*Upload{}}
}

// archiveFormat 은 파일 이름으로 압축 형식을 정한다.
func archiveFormat(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tar.gz"
	case strings.HasSuffix(lower, ".tar"):
		return "tar"
	}
	return ""
}

// Save 는 body 를 받아 두고 목록을 읽는다. 압축 파일이 아니거나 올바르지 않으면 지우고 400 이다.
func (u *Uploads) Save(name string, body io.Reader) (*Upload, error) {
	u.expire()
	format := archiveFormat(name)
	if format == "" {
		return nil, &UserError{http.StatusBadRequest, "zip · tar.gz · tgz · tar 파일만 올릴 수 있습니다"}
	}
	random := make([]byte, 12)
	rand.Read(random)
	id := hex.EncodeToString(random)
	file := filepath.Join(u.dir, "upload-"+id)
	if err := os.MkdirAll(u.dir, 0o700); err != nil {
		return nil, fmt.Errorf("받아 둘 폴더를 만들지 못했습니다: %w", err)
	}

	out, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	written, err := io.Copy(out, io.LimitReader(body, u.maxBytes+1))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	fail := func(err error) (*Upload, error) {
		os.Remove(file)
		return nil, err
	}
	switch {
	case err != nil:
		return fail(fmt.Errorf("받는 중에 끊겼습니다: %w", err))
	case written > u.maxBytes:
		return fail(&UserError{http.StatusRequestEntityTooLarge, fmt.Sprintf("%dMB 를 넘는 파일은 올릴 수 없습니다", u.maxBytes>>20)})
	}

	upload := &Upload{ID: id, Name: name, Format: format, Entries: []UploadEntry{}, file: file, created: time.Now()}
	if err := upload.scan(written * maxExpandRatio); err != nil {
		return fail(err)
	}
	u.mu.Lock()
	u.items[id] = upload
	u.mu.Unlock()
	return upload, nil
}

// Get 은 받아 둔 압축 파일이다. 없거나 시간이 지났으면 404 다.
func (u *Uploads) Get(id string) (*Upload, error) {
	u.expire()
	u.mu.Lock()
	defer u.mu.Unlock()
	if upload := u.items[id]; upload != nil {
		return upload, nil
	}
	return nil, &UserError{http.StatusNotFound, "올린 압축 파일이 없습니다 (시간이 지나 지워졌으면 다시 올려 주세요)"}
}

// Delete 는 받아 둔 압축 파일을 지운다. (설치 · 업데이트를 마쳤거나 창을 닫았을 때)
func (u *Uploads) Delete(id string) {
	u.mu.Lock()
	upload := u.items[id]
	delete(u.items, id)
	u.mu.Unlock()
	if upload != nil {
		os.Remove(upload.file)
	}
}

func (u *Uploads) expire() {
	u.mu.Lock()
	var old []string
	for id, upload := range u.items {
		if time.Since(upload.created) > uploadTTL {
			old = append(old, id)
		}
	}
	u.mu.Unlock()
	for _, id := range old {
		u.Delete(id)
	}
}

// archiveEntry 는 압축 파일 안의 항목 하나다. (zip · tar 공통)
type archiveEntry struct {
	name     string
	typ      byte
	size     int64
	exec     bool
	link     string
	modified time.Time
}

// walk 는 압축 파일의 항목을 차례로 fn 에 넘긴다. 파일이면 content 로 내용을 읽을 수 있다.
func (up *Upload) walk(fn func(entry archiveEntry, content io.Reader) error) error {
	if up.Format == "zip" {
		archive, err := zip.OpenReader(up.file)
		if err != nil {
			return &UserError{http.StatusBadRequest, "zip 파일을 읽지 못했습니다: " + err.Error()}
		}
		defer archive.Close()
		for _, f := range archive.File {
			mode := f.Mode()
			entry := archiveEntry{name: f.Name, size: int64(f.UncompressedSize64), exec: mode&0o111 != 0, modified: f.Modified}
			var content io.ReadCloser
			switch {
			case mode&os.ModeSymlink != 0:
				// zip 의 링크는 내용에 가리키는 곳이 들어 있다.
				r, err := f.Open()
				if err != nil {
					return err
				}
				target, err := io.ReadAll(io.LimitReader(r, 4096))
				r.Close()
				if err != nil {
					return err
				}
				entry.typ, entry.link, entry.size = tar.TypeSymlink, string(target), 0
			case f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/"):
				entry.typ, entry.size = tar.TypeDir, 0
			case mode.IsRegular():
				entry.typ = tar.TypeReg
				if content, err = f.Open(); err != nil {
					return err
				}
			default:
				entry.typ = tar.TypeChar // 장치 파일 등: 아래에서 거부한다.
			}
			err := fn(entry, content)
			if content != nil {
				content.Close()
			}
			if err != nil {
				return err
			}
		}
		return nil
	}

	file, err := os.Open(up.file)
	if err != nil {
		return err
	}
	defer file.Close()
	var r io.Reader = file
	if up.Format == "tar.gz" {
		gz, err := gzip.NewReader(file)
		if err != nil {
			return &UserError{http.StatusBadRequest, "gzip 파일을 읽지 못했습니다: " + err.Error()}
		}
		defer gz.Close()
		r = gz
	}
	in := tar.NewReader(r)
	for {
		header, err := in.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return &UserError{http.StatusBadRequest, "tar 파일을 읽지 못했습니다: " + err.Error()}
		}
		entry := archiveEntry{name: header.Name, size: header.Size, exec: header.Mode&0o111 != 0, link: header.Linkname, modified: header.ModTime}
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck // 오래된 tar 의 일반 파일
			entry.typ = tar.TypeReg
		case tar.TypeDir, tar.TypeSymlink:
			entry.typ, entry.size = header.Typeflag, 0
		case tar.TypeXGlobalHeader:
			continue
		default:
			entry.typ = header.Typeflag // 하드 링크 · 장치 파일 등: 아래에서 거부한다.
		}
		if err := fn(entry, in); err != nil {
			return err
		}
	}
}

// skipEntry 는 설치하지 않는 항목이다. macOS 가 압축할 때 넣는 정보: zip 의 __MACOSX/, tar 의 ._<이름>(AppleDouble), .DS_Store
func skipEntry(name string) bool {
	base := path.Base(name)
	return name == "__MACOSX" || strings.HasPrefix(name, "__MACOSX/") || base == ".DS_Store" || strings.HasPrefix(base, "._")
}

// scan 은 목록을 읽고 확인한다. 맨 위가 폴더 하나뿐이면 Root 로 정한다. maxBytes 는 푼 크기의 한계다.
func (up *Upload) scan(maxBytes int64) error {
	var entries []archiveEntry
	err := up.walk(func(entry archiveEntry, _ io.Reader) error {
		name, ok := cleanEntry(entry.name)
		if !ok {
			return invalidContent("경로 %q", entry.name)
		}
		if skipEntry(name) {
			return nil
		}
		if entry.typ != tar.TypeDir && entry.typ != tar.TypeReg && entry.typ != tar.TypeSymlink {
			return invalidContent("폴더 · 파일 · 링크가 아닌 항목 %q", entry.name)
		}
		entry.name = name
		entries = append(entries, entry)
		up.Bytes += entry.size
		if up.Bytes > maxBytes {
			return invalidContent("압축을 푼 크기가 너무 큽니다")
		}
		return nil
	})
	if err != nil {
		var userErr *UserError
		if errors.As(err, &userErr) {
			return err
		}
		return &UserError{http.StatusBadRequest, "압축 파일을 읽지 못했습니다: " + err.Error()}
	}
	if len(entries) == 0 {
		return invalidContent("빈 압축 파일입니다")
	}

	// 맨 위 이름이 하나뿐이고 그 아래 항목이 있으면(폴더) 벗겨 낸다.
	first := strings.SplitN(entries[0].name, "/", 2)[0]
	strip := true
	nested := false
	for _, e := range entries {
		top, rest, found := strings.Cut(e.name, "/")
		strip = strip && top == first && (found || e.typ == tar.TypeDir)
		nested = nested || (found && rest != "")
	}
	if strip && nested {
		up.Root = first
	}

	for _, e := range entries {
		rel := up.relative(e.name)
		if rel == "" {
			continue
		}
		item := UploadEntry{Path: rel, Size: e.size}
		switch e.typ {
		case tar.TypeDir:
			item.Type = "dir"
		case tar.TypeReg:
			item.Type, item.Exec = "file", e.exec
			up.Files++
		case tar.TypeSymlink:
			// 링크는 압축 파일 안의 다른 항목만 가리킬 수 있다.
			resolved := path.Clean(path.Join(path.Dir(rel), e.link))
			if e.link == "" || strings.HasPrefix(e.link, "/") || resolved == ".." || strings.HasPrefix(resolved, "../") {
				return invalidContent("압축 파일 밖을 가리키는 링크 %q -> %q", e.name, e.link)
			}
			item.Type, item.Target = "link", e.link
		}
		up.Entries = append(up.Entries, item)
	}
	sort.SliceStable(up.Entries, func(i, j int) bool { return up.Entries[i].Path < up.Entries[j].Path })
	return nil
}

// relative 는 맨 위 폴더(Root)를 벗긴 경로다. 맨 위 폴더 자체면 "" 이다.
func (up *Upload) relative(name string) string {
	if up.Root == "" {
		return name
	}
	if name == up.Root {
		return ""
	}
	return strings.TrimPrefix(name, up.Root+"/")
}

// Source 는 압축을 풀며 항목을 설치 · 업데이트 폴더 이름(name) 아래로 옮겨 보낸다. 압축 파일은 폴더로만 쓴다.
func (up *Upload) Source() Source {
	return func(p *packer) error {
		if p.kind != UpdateDir {
			return &UserError{http.StatusBadRequest, "압축 파일은 폴더로만 바꿀 수 있습니다"}
		}
		if err := p.add(p.name, tar.TypeDir, 0, false, "", up.created, nil); err != nil {
			return err
		}
		return up.walk(func(entry archiveEntry, content io.Reader) error {
			name, ok := cleanEntry(entry.name)
			if !ok {
				return invalidContent("경로 %q", entry.name)
			}
			rel := up.relative(name)
			if skipEntry(name) || rel == "" {
				return nil
			}
			return p.add(p.name+"/"+rel, entry.typ, entry.size, entry.exec, entry.link, entry.modified, content)
		})
	}
}

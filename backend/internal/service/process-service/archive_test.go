package processes

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type archiveItem struct {
	name, content, link string
	dir                 bool
	mode                int64
}

func makeZip(t *testing.T, items ...archiveItem) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, item := range items {
		header := &zip.FileHeader{Name: item.name, Method: zip.Deflate}
		mode := fs.FileMode(item.mode)
		switch {
		case item.dir:
			header.Name += "/"
			mode = fs.ModeDir | 0o755
		case item.link != "":
			mode = fs.ModeSymlink | 0o777
		case mode == 0:
			mode = 0o644
		}
		header.SetMode(mode)
		f, err := w.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if item.link != "" {
			f.Write([]byte(item.link))
		} else {
			f.Write([]byte(item.content))
		}
	}
	w.Close()
	return buf.Bytes()
}

func makeTarGz(t *testing.T, items ...archiveItem) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	w := tar.NewWriter(gz)
	for _, item := range items {
		header := &tar.Header{Name: item.name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(item.content))}
		switch {
		case item.dir:
			header = &tar.Header{Name: item.name + "/", Mode: 0o755, Typeflag: tar.TypeDir}
		case item.link != "":
			header = &tar.Header{Name: item.name, Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: item.link}
		case item.mode != 0:
			header.Mode = item.mode
		}
		w.WriteHeader(header)
		w.Write([]byte(item.content))
	}
	w.Close()
	gz.Close()
	return buf.Bytes()
}

func TestUploadScan(t *testing.T) {
	uploads := NewUploads(t.TempDir(), 1<<20)

	// zip: 맨 위 폴더 하나(myapp-1.2)는 벗기고, __MACOSX 는 빼고, 실행 권한 · 링크를 읽는다.
	up, err := uploads.Save("myapp-1.2.zip", bytes.NewReader(makeZip(t,
		archiveItem{name: "myapp-1.2", dir: true},
		archiveItem{name: "myapp-1.2/bin/server", content: "#!/bin/sh", mode: 0o755},
		archiveItem{name: "myapp-1.2/lib/a.so.1", content: "so"},
		archiveItem{name: "myapp-1.2/lib/a.so", link: "a.so.1"},
		archiveItem{name: "__MACOSX/myapp-1.2/._server", content: "x"},
	)))
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	for _, e := range up.Entries {
		paths = append(paths, e.Type+":"+e.Path)
	}
	if up.Format != "zip" || up.Root != "myapp-1.2" || up.Files != 2 ||
		strings.Join(paths, ",") != "file:bin/server,link:lib/a.so,file:lib/a.so.1" || !up.Entries[0].Exec || up.Entries[2].Exec {
		t.Errorf("zip upload = %+v %v", up, paths)
	}
	if got, _ := uploads.Get(up.ID); got != up {
		t.Error("get")
	}
	uploads.Delete(up.ID)
	if _, err := uploads.Get(up.ID); !isStatus(err, http.StatusNotFound) {
		t.Errorf("deleted: %v", err)
	}

	// macOS 에서 만든 tar.gz: ._<이름> 은 빼고 맨 위 폴더를 벗긴다.
	up, err = uploads.Save("mac.tar.gz", bytes.NewReader(makeTarGz(t,
		archiveItem{name: "._myapp", content: "meta"},
		archiveItem{name: "myapp", dir: true},
		archiveItem{name: "myapp/._NEW", content: "meta"},
		archiveItem{name: "myapp/NEW", content: "x"},
	)))
	if err != nil || up.Root != "myapp" || up.Files != 1 || up.Entries[0].Path != "NEW" {
		t.Errorf("mac tgz = %+v, %v", up, err)
	}
	uploads.Delete(up.ID)

	// tar.gz: 맨 위에 파일이 여러 개면 벗기지 않는다.
	up, err = uploads.Save("app.tgz", bytes.NewReader(makeTarGz(t, archiveItem{name: "start.sh", content: "x"}, archiveItem{name: "conf/a.ini", content: "y"})))
	if err != nil || up.Root != "" || up.Format != "tar.gz" || up.Files != 2 {
		t.Errorf("tgz = %+v, %v", up, err)
	}

	// 거부: 형식 · 밖을 가리키는 링크 · '..' · 너무 큰 파일 · 깨진 파일
	for name, data := range map[string][]byte{
		"a.rar":     []byte("x"),
		"link.zip":  makeZip(t, archiveItem{name: "app/x", link: "../../etc/passwd"}),
		"abs.tgz":   makeTarGz(t, archiveItem{name: "app/x", link: "/etc/passwd"}),
		"dots.tgz":  makeTarGz(t, archiveItem{name: "../x", content: "x"}),
		"broken.gz": []byte("not gzip"),
	} {
		if name == "broken.gz" {
			name = "broken.tar.gz"
		}
		if _, err := uploads.Save(name, bytes.NewReader(data)); !isStatus(err, http.StatusBadRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := uploads.Save("big.zip", bytes.NewReader(make([]byte, 2<<20))); !isStatus(err, http.StatusRequestEntityTooLarge) {
		t.Errorf("big: %v", err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(uploads.dir, "upload-*")); len(leftovers) != 1 {
		t.Errorf("only the valid tgz should remain: %v", leftovers)
	}
}

func TestInstallAndUpdateFromArchiveOverSSH(t *testing.T) {
	host, port := startSSHServer(t, "pw")
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)

	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "processes.json"))
	body, _ := json.Marshal(Config{
		Groups:    []Group{{Slug: "uw", Name: "의왕"}},
		Servers:   []Server{{ID: "local", Group: "uw", Host: host, Port: port, User: "ops", Password: "pw", BaseDir: home}},
		Processes: []Process{},
	})
	if _, err := store.Save(body); err != nil {
		t.Fatal(err)
	}
	monitor := NewMonitor(store, filepath.Join(dir, "known_hosts"))
	defer monitor.Close()
	uploads := NewUploads(filepath.Join(dir, "upload"), 1<<20)
	ctx := context.Background()

	// zip 으로 설치: 맨 위 폴더를 벗겨 myapp/ 아래에. 압축 파일의 실행 권한 · 링크를 그대로 둔다.
	up, err := uploads.Save("myapp-1.2.zip", bytes.NewReader(makeZip(t,
		archiveItem{name: "myapp-1.2/bin/server", content: "#!/bin/sh", mode: 0o755},
		archiveItem{name: "myapp-1.2/lib/a.so.1", content: "so"},
		archiveItem{name: "myapp-1.2/lib/a.so", link: "a.so.1"},
		archiveItem{name: "myapp-1.2/README", content: "doc"},
	)))
	if err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(home, "myapp")
	result, err := monitor.InstallRemote(ctx, "local", InstallRequest{Target: app, StartFile: "bin/server"}, up.Source())
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 3 {
		t.Errorf("result = %+v", result)
	}
	if info, err := os.Stat(filepath.Join(app, "bin", "server")); err != nil || info.Mode()&0o100 == 0 {
		t.Errorf("server mode: %v %v", info, err)
	}
	if info, _ := os.Stat(filepath.Join(app, "README")); info.Mode()&0o111 != 0 {
		t.Errorf("README mode = %v", info.Mode())
	}
	if target, err := os.Readlink(filepath.Join(app, "lib", "a.so")); err != nil || target != "a.so.1" {
		t.Errorf("link = %q, %v", target, err)
	}

	// tar.gz 로 업데이트: 기존 폴더는 .bak 으로 남는다.
	up, err = uploads.Save("myapp-1.3.tar.gz", bytes.NewReader(makeTarGz(t,
		archiveItem{name: "myapp-1.3/bin/server", content: "v2", mode: 0o755},
		archiveItem{name: "myapp-1.3/NEW", content: "new"},
	)))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := monitor.UpdateRemote(ctx, "local", app, UpdateDir, up.Source())
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(app, "NEW")); string(data) != "new" || !strings.HasPrefix(updated.Backup, app+".bak.") {
		t.Errorf("update = %+v", updated)
	}
	if _, err := os.Stat(filepath.Join(updated.Backup, "README")); err != nil {
		t.Error("backup must keep the old files")
	}
	// 압축 파일로 파일 하나를 바꿀 수는 없다.
	os.WriteFile(filepath.Join(home, "one.jar"), []byte("x"), 0o644)
	if _, err := monitor.UpdateRemote(ctx, "local", filepath.Join(home, "one.jar"), UpdateFile, up.Source()); !isStatus(err, http.StatusBadRequest) {
		t.Errorf("archive onto file: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(home, ".*.update.*")); len(matches) > 0 {
		t.Errorf("temporary folders left: %v", matches)
	}
}

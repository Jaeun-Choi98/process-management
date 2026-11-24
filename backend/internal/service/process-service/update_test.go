package processes

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tarEntry struct {
	name    string
	content string
	dir     bool
}

func buildTar(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for _, e := range entries {
		header := &tar.Header{Name: e.name, Mode: 0o600, Typeflag: tar.TypeReg, Size: int64(len(e.content))}
		if e.dir {
			header = &tar.Header{Name: e.name + "/", Mode: 0o700, Typeflag: tar.TypeDir}
		}
		if err := w.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e.content))
	}
	w.Close()
	return buf.Bytes()
}

func TestUpdateOverSSH(t *testing.T) {
	host, port := startSSHServer(t, "pw")
	// 테스트용 SSH 서버는 이 프로세스의 환경으로 sh 를 실행하므로 HOME 이 SSH 계정의 홈이 된다.
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	outside, _ := filepath.EvalSymlinks(t.TempDir())

	app := filepath.Join(home, "app")
	os.MkdirAll(filepath.Join(app, "lib"), 0o755)
	os.WriteFile(filepath.Join(app, "run.sh"), []byte("old"), 0o755)
	os.WriteFile(filepath.Join(app, "lib", "a.txt"), []byte("old"), 0o644)
	jar := filepath.Join(home, "app.jar")
	os.WriteFile(jar, []byte("old jar"), 0o644)

	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "processes.json"))
	body, _ := json.Marshal(Config{
		Groups:    []Group{{Slug: "uw", Name: "의왕"}},
		Servers:   []Server{{ID: "local", Group: "uw", Host: host, Port: port, User: "ops", Password: "pw"}},
		Processes: []Process{},
	})
	if _, err := store.Save(body); err != nil {
		t.Fatal(err)
	}
	monitor := NewMonitor(store, filepath.Join(dir, "known_hosts"))
	defer monitor.Close()
	ctx := context.Background()

	// 둘러보기: 홈에서 시작하고, 폴더가 먼저 나오며, 홈 밖은 거부한다.
	listing, err := monitor.BrowseRemote(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if listing.Home != home || listing.Path != home || len(listing.Entries) != 2 ||
		listing.Entries[0].Path != app || listing.Entries[0].Type != "dir" || listing.Entries[1].Path != jar || listing.Entries[1].Type != "file" {
		t.Fatalf("browse home = %+v", listing)
	}
	if listing, err := monitor.BrowseRemote(ctx, "local", app); err != nil || len(listing.Entries) != 2 || listing.Entries[0].Path != filepath.Join(app, "lib") {
		t.Errorf("browse app = %+v, %v", listing, err)
	}
	if _, err := monitor.BrowseRemote(ctx, "local", outside); err == nil || !strings.Contains(err.Error(), "홈 폴더 밖") {
		t.Errorf("browse outside home: %v", err)
	}
	if _, err := monitor.BrowseRemote(ctx, "local", home+"/../x'y"); !isStatus(err, http.StatusBadRequest) {
		t.Errorf("browse bad path: %v", err)
	}
	if _, err := monitor.BrowseRemote(ctx, "nope", ""); !isStatus(err, http.StatusNotFound) {
		t.Errorf("browse unknown server: %v", err)
	}

	// 실행 중인 프로세스 목록: 커널 스레드 · 상태 확인용 ps 는 없다.
	if rows, err := monitor.RunningProcesses(ctx, "local"); err != nil || len(rows) == 0 {
		t.Errorf("running processes = %d, %v", len(rows), err)
	} else {
		for _, row := range rows {
			if !isUserProcess(row) {
				t.Errorf("unexpected row %+v", row)
			}
		}
	}

	leftovers := func() {
		t.Helper()
		if matches, _ := filepath.Glob(filepath.Join(home, ".*.update.*")); len(matches) > 0 {
			t.Errorf("temporary folders left: %v", matches)
		}
	}
	update := func(target, kind string, data []byte, size int64) (*UpdateResult, error) {
		t.Helper()
		defer leftovers()
		return monitor.UpdateRemote(ctx, "local", target, kind, bytes.NewReader(data), size)
	}
	read := func(name string) string {
		data, _ := os.ReadFile(name)
		return string(data)
	}

	// 폴더 → 폴더: 기존 폴더는 app.bak.<시각> 으로 남고, 실행 권한은 같은 경로의 기존 파일을 따른다.
	folder := buildTar(t,
		tarEntry{name: "app", dir: true},
		tarEntry{name: "app/run.sh", content: "new"},
		tarEntry{name: "app/new.txt", content: "added"},
		tarEntry{name: "app/sub", dir: true},
		tarEntry{name: "app/sub/b.txt", content: "b"},
	)
	result, err := update(app, UpdateDir, folder, int64(len(folder)))
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != app || !strings.HasPrefix(result.Backup, app+".bak.") || result.Files != 3 || result.Bytes != 9 {
		t.Errorf("update result = %+v", result)
	}
	if read(filepath.Join(app, "run.sh")) != "new" || read(filepath.Join(app, "sub", "b.txt")) != "b" || read(filepath.Join(result.Backup, "lib", "a.txt")) != "old" {
		t.Error("folder not replaced")
	}
	if _, err := os.Stat(filepath.Join(app, "lib")); !os.IsNotExist(err) {
		t.Error("old files must not remain in the new folder")
	}
	if info, _ := os.Stat(filepath.Join(app, "run.sh")); info.Mode()&0o100 == 0 {
		t.Errorf("run.sh mode = %v, want executable like before", info.Mode())
	}
	if info, _ := os.Stat(filepath.Join(app, "new.txt")); info.Mode()&0o111 != 0 {
		t.Errorf("new.txt mode = %v", info.Mode())
	}

	// 파일 → 파일
	file := buildTar(t, tarEntry{name: "app.jar", content: "new jar"})
	if result, err := update(jar, UpdateFile, file, -1); err != nil || read(jar) != "new jar" || read(result.Backup) != "old jar" {
		t.Errorf("file update = %+v, %v", result, err)
	}

	// 종류가 다르면(파일 자리에 폴더) 바꾸지 않는다.
	jarFolder := buildTar(t, tarEntry{name: "app.jar", dir: true}, tarEntry{name: "app.jar/x", content: "x"})
	if _, err := update(jar, UpdateDir, jarFolder, -1); err == nil || !strings.Contains(err.Error(), "폴더가 아닙니다") || read(jar) != "new jar" {
		t.Errorf("dir onto file: %v", err)
	}
	if _, err := update(app, UpdateFile, buildTar(t, tarEntry{name: "app", content: "x"}), -1); err == nil || !strings.Contains(err.Error(), "파일이 아닙니다") {
		t.Errorf("file onto dir: %v", err)
	}

	// 대상 이름 밖의 항목, '..', 링크 등은 거부한다.
	for name, data := range map[string][]byte{
		"other name": buildTar(t, tarEntry{name: "app", dir: true}, tarEntry{name: "other/x", content: "x"}),
		"dot dot":    buildTar(t, tarEntry{name: "app", dir: true}, tarEntry{name: "app/../x", content: "x"}),
		"no root":    buildTar(t, tarEntry{name: "app/x", content: "x"}),
	} {
		if _, err := update(app, UpdateDir, data, -1); !isStatus(err, http.StatusBadRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if read(filepath.Join(app, "run.sh")) != "new" {
		t.Error("rejected upload changed the folder")
	}

	// 끝까지 받지 못하면(길이가 다르면) 바꾸지 않는다.
	if _, err := update(app, UpdateDir, folder, int64(len(folder))+512); err == nil {
		t.Error("short body must fail")
	}
	if backups, _ := filepath.Glob(app + ".bak.*"); len(backups) != 1 {
		t.Errorf("backups = %v", backups)
	}

	// 홈 밖은 바꿀 수 없다.
	os.WriteFile(filepath.Join(outside, "x"), []byte("x"), 0o644)
	if _, err := update(filepath.Join(outside, "x"), UpdateFile, buildTar(t, tarEntry{name: "x", content: "y"}), -1); err == nil || !strings.Contains(err.Error(), "홈 폴더 밖") {
		t.Errorf("update outside home: %v", err)
	}
}

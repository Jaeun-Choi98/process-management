package processes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallOverSSH(t *testing.T) {
	host, port := startSSHServer(t, "pw")
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	os.WriteFile(filepath.Join(home, "existing"), []byte("x"), 0o644)

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
	ctx := context.Background()

	leftovers := func() {
		t.Helper()
		if matches, _ := filepath.Glob(filepath.Join(home, ".*.update.*")); len(matches) > 0 {
			t.Errorf("temporary folders left: %v", matches)
		}
	}
	install := func(req InstallRequest, data []byte) (*InstallResult, error) {
		t.Helper()
		defer leftovers()
		return monitor.InstallRemote(ctx, "local", req, bytes.NewReader(data), int64(len(data)))
	}
	pkg := func(name string) []byte {
		return buildTar(t,
			tarEntry{name: name, dir: true},
			tarEntry{name: name + "/bin", dir: true},
			tarEntry{name: name + "/bin/server", content: "#!/bin/sh\nsleep 1\n"},
			tarEntry{name: name + "/tools.sh", content: "echo"},
			tarEntry{name: name + "/conf.ini", content: "[a]"},
		)
	}
	executable := func(name string) bool {
		info, err := os.Stat(name)
		return err == nil && info.Mode()&0o100 != 0
	}

	// 새 폴더에 설치: *.sh 와 시작 파일(확장자 없는 실행 파일)에 실행 권한, 프로세스 등록 (기본 폴더 기준 상대 경로)
	app := filepath.Join(home, "myapp")
	result, err := install(InstallRequest{Target: app, StartFile: "bin/server", Label: "내 앱"}, pkg("myapp"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != app || result.ProcessID != "myapp" || result.Files != 3 {
		t.Errorf("result = %+v", result)
	}
	if !executable(filepath.Join(app, "bin", "server")) || !executable(filepath.Join(app, "tools.sh")) || executable(filepath.Join(app, "conf.ini")) {
		t.Error("executable bits not set as expected")
	}
	config, _ := store.Load()
	if len(config.Processes) != 1 || config.Processes[0].StartScript != "myapp/bin/server" || config.Processes[0].Label != "내 앱" || config.Processes[0].Server != "local" {
		t.Errorf("registered = %+v", config.Processes)
	}

	// 이미 있으면 설치하지 않는다. (등록도 하지 않는다)
	for _, target := range []string{app, filepath.Join(home, "existing")} {
		if _, err := install(InstallRequest{Target: target}, pkg(filepath.Base(target))); err == nil || !strings.Contains(err.Error(), "이미 있습니다") {
			t.Errorf("install onto %s: %v", target, err)
		}
	}
	// 올리기 전에 이름 · 시작 파일 경로를 확인한다.
	for _, req := range []InstallRequest{{Target: home + "/bad name"}, {Target: home + "/.hidden"}, {Target: home + "/ok", StartFile: "../x"}} {
		if _, err := install(req, pkg("ok")); !isStatus(err, http.StatusBadRequest) {
			t.Errorf("%+v: %v", req, err)
		}
	}
	// 시작 파일이 올린 폴더에 없으면 설치하지 않는다.
	if _, err := install(InstallRequest{Target: home + "/other", StartFile: "nope.sh"}, pkg("other")); err == nil || !strings.Contains(err.Error(), "시작 파일") {
		t.Errorf("missing start file: %v", err)
	}
	if _, err := os.Stat(home + "/other"); !os.IsNotExist(err) {
		t.Error("failed install must not leave the folder")
	}
	// 같은 이름의 프로세스가 있으면 id 를 바꿔 등록한다. 시작 파일 없이도 등록한다.
	if result, err := install(InstallRequest{Target: home + "/sub-MyApp"}, pkg("sub-MyApp")); err != nil || result.ProcessID != "sub-myapp" {
		t.Errorf("second install = %+v, %v", result, err)
	}
	os.MkdirAll(home+"/x", 0o755)
	if result, err := install(InstallRequest{Target: home + "/x/myapp"}, pkg("myapp")); err != nil || result.ProcessID != "myapp-2" {
		t.Errorf("duplicate id install = %+v, %v", result, err)
	}
	if config, _ := store.Load(); len(config.Processes) != 3 {
		t.Errorf("processes = %d", len(config.Processes))
	}
}

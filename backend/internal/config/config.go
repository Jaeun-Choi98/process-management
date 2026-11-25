package config

import (
	"fmt"
	"strings"

	"github.com/go-ini/ini"
)

const (
	TEST    = 0
	PRODUCT = 1
)

// 화면 로그인 계정. 비밀번호는 sha256 값(16진수 64자)만 둔다. 바꾸려면 여기를 고치고 다시 빌드한다.
//
//	Linux : printf '%s' '비밀번호' | sha256sum
//	macOS : printf '%s' '비밀번호' | shasum -a 256
//	( echo 는 끝에 줄바꿈이 붙어 값이 달라지므로 printf 를 쓴다 )
const (
	AuthUser           = "admin"
	AuthPasswordSHA256 = ""
)

type Configuration struct {
	// http-rest
	RestIp   string
	RestPort string
	Cors     []string
	TLS      int
	SSLCERT  string
	SSLKEY   string

	// spa
	SpaDir   string
	SpaIndex string

	// 화면에서 수정한 그룹 · 서버 · 프로세스 설정 ( SSH 비밀번호 포함, 0600 ). 재배포 시 build 를 교체해도 유지되도록 build 밖에 둔다.
	ProcessesFile string
	// 접속한 서버의 SSH 호스트 키 기록 ( 처음 접속할 때 기록, 이후 다르면 접속 거부 )
	KnownHostsFile string

	// 설치 · 업데이트용 압축 파일을 잠시 받아 두는 폴더와 최대 크기(바이트)
	UploadDir      string
	UploadMaxBytes int64

	// 화면 로그인 토큰 만료 시간(분). 계정은 위의 AuthUser · AuthPasswordSHA256 상수.
	AuthSessionMinutes int

	// log
	LogLevel string

	// deploy env
	DeployMode int
}

func NewConfiguration() (*Configuration, error) {
	cfgFile, err := ini.Load("env.ini")
	if err != nil {
		return nil, err
	}

	rest := cfgFile.Section("REST")
	config := &Configuration{
		RestIp:   rest.Key("IP").MustString(""),
		RestPort: rest.Key("PORT").MustString("8080"),
		TLS:      rest.Key("TLS").MustInt(0),
		SSLCERT:  rest.Key("SSLCERT").MustString(""),
		SSLKEY:   rest.Key("SSLKEY").MustString(""),
		SpaDir:   cfgFile.Section("SPA").Key("DIR").MustString("build"),
		SpaIndex: cfgFile.Section("SPA").Key("INDEX").MustString("index.html"),

		ProcessesFile:      cfgFile.Section("PROCESSES").Key("FILE").MustString("./data/processes.json"),
		KnownHostsFile:     cfgFile.Section("PROCESSES").Key("KNOWN_HOSTS").MustString("./data/known_hosts"),
		UploadDir:          cfgFile.Section("UPLOAD").Key("DIR").MustString("./data/upload"),
		UploadMaxBytes:     cfgFile.Section("UPLOAD").Key("MAX_MB").MustInt64(4096) << 20,
		AuthSessionMinutes: cfgFile.Section("AUTH").Key("SESSION_MIN").MustInt(60),
		LogLevel:           cfgFile.Section("LOG").Key("LEVEL").MustString("INFO"),

		DeployMode: cfgFile.Section("DEPLOY_SATE").Key("MODE").MustInt(TEST),
	}

	for _, origin := range strings.Split(rest.Key("CORS").MustString(""), ",") {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		if origin == "*" {
			config.Cors = append(config.Cors, origin)
			continue
		}
		config.Cors = append(config.Cors, fmt.Sprintf("http://%s", origin))
		if config.TLS > 0 {
			config.Cors = append(config.Cors, fmt.Sprintf("https://%s", origin))
		}
	}

	return config, nil
}

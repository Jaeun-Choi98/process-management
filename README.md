# Process Management

여러 서버에 SSH 로 접속해 등록한 프로세스의 실행 상태(PID · CPU · MEM · 실행 시간)를 확인하고,
등록한 로그 파일을 보고, INI 설정 파일을 읽고 고치고, 시작 · 중지 · 재시작하는 관리 화면입니다. 꺼진 프로세스를 자동으로 다시 시작할 수도 있습니다. 서버는 사용자가 만든 그룹(의왕, 광명 ...)으로
묶어서 보여주고, 그룹마다 주소가 나뉩니다. (`/all` 전체, `/uw` 의왕만)

```
브라우저 ──HTTP──▶ Go 서버 (/api) ──SSH (비밀번호)──▶ 각 서버: ps / ls / tail / cat (INI) / kill / 시작 스크립트
                    └ 자동 재시작 감시 (watchIntervalMs 마다)
```

## 설계 원칙

- **정해진 명령만 실행**: 설정에는 명령을 적지 않고, 프로세스를 찾을 조건(`matchName` · `matchArgs` 또는 `matchRegex`)과 로그 · INI 파일 패턴 ·
  시작 스크립트 경로만 적습니다. Go 서버가 `ps`, `ls`, `tail`, `kill -TERM <PID>`, `nohup <스크립트>` 만 만들어
  실행하며, 설정의 경로는 셸 특수문자가 없는 절대 경로만 허용합니다.
- **파일은 시작 스크립트 폴더 기준으로**: 로그 · INI 화면에는 폴더를 둘러보는 기능이 없고, 정해진 패턴에 맞는 파일만 보입니다.
  패턴을 비워 두면 로그는 시작 스크립트 폴더의 한 단계 위 폴더부터 6단계까지의 `*.log` (예: `bin/start.sh` 옆의 `logs/` 도 찾음, 위 폴더가 `/home` 처럼 너무 얕으면 시작 스크립트 폴더부터 5단계), INI 는 시작 스크립트 폴더 아래 3단계까지의 `*.ini` 를 찾습니다.
  (`/opt/shelter/start.sh` → `/opt/shelter/*.log`, `/opt/shelter/*/*.log` … `/opt/shelter/*/*/*/*/*.log`)
  다른 곳을 보려면 패턴(`logFiles` · `iniFiles`)을 적습니다. 상대 경로(`logs/*.log`)는 시작 스크립트 폴더 기준, 절대 경로는 그대로입니다.
  읽기 · 쓰기 요청이 오면 Go 서버가 경로가 패턴에 맞는지 다시 확인합니다. `* ?` 는 `/` 를 넘지 않고(`**` 금지), 앞의 두 폴더는
  와일드카드 없이 적어야 해서 `/home/*`, `/*/app.log` 처럼 넓은 곳을 훑는 패턴은 저장되지 않습니다. 시작 스크립트가 `/opt/start.sh`
  처럼 얕은 곳에 있으면 자동으로 찾지 않습니다. (패턴에 맞는 심볼릭 링크가 가리키는 곳은 막지 않습니다)
- **INI 는 안전하게 덮어쓰기**: INI 파일 패턴에 맞는 파일만 열고 고칩니다. 저장할 때는 읽었던 내용의 hash 를
  같이 보내 그 사이 다른 곳에서 바뀌었으면 거부하고, 받은 내용을 임시 파일에 다 쓴 뒤 크기를 확인하고 나서야 원본을 바꿉니다.
  이전 내용은 `<파일>.bak` 에 남기고, 원본은 내용만 바꿔 권한 · 소유자를 유지합니다. UTF-8 이 아닌 파일은 열지 않습니다.
- **실행 판단 조건은 하나**: 상태 표시(PID · STOPPED), 중지 대상, 자동 재시작이 모두 같은 조건으로 프로세스를 찾습니다.
  그래서 화면에는 실행 중인데 자동 재시작이 또 띄우거나, 다른 프로세스를 중지하는 일이 없습니다. 조건은 모니터링 화면의
  `조건` 창에서 정하며, 실행 파일 이름(`matchName`, 정확히 일치)과 명령줄에 들어 있어야 할 문자열(`matchArgs`)로 적는 것을
  권장합니다. 둘 중 하나만 적어도 되고, 둘 다 적으면 한 프로세스가 둘 다 만족해야 합니다. 이것으로 구분할 수 없을 때만
  정규식(`matchRegex`)을 씁니다.
- **자동 재시작**: `조건` 창에서 프로세스별로 켜고 끕니다. 조건에 맞는 프로세스가 `ps` 에 없으면 시작
  스크립트를 실행하고, 재시작 대기(`restartDelayMs`, 기본 30초) 뒤에도 없으면 횟수 제한 없이 다시 실행합니다. 화면에서 **중지**하면 다시
  시작할 때까지 자동 재시작을 멈춥니다. (이 '멈춤' 과 재시작 횟수는 Go 서버 메모리에만 있어서, Go 서버를 다시 켜면
  풀립니다. 오래 꺼 둘 프로세스는 설정에서 자동 재시작을 끄세요)
- **비밀번호는 쓰기 전용**: 설정에서 입력한 SSH 비밀번호는 Go 서버의 설정 파일(0600)에만 저장되고,
  조회 API 는 `hasPassword` 만 돌려줍니다. 비워 두고 저장하면 기존 비밀번호를 유지하되, host · port · user 가
  바뀌면 다시 입력해야 합니다. (저장된 비밀번호를 다른 서버로 보낼 수 없도록)
- **호스트 키 확인**: 처음 접속한 서버의 호스트 키를 `known_hosts` 에 기록하고, 이후 키가 다르면 접속하지
  않습니다. 서버를 재설치했다면 해당 줄을 지우세요.
- **서버 부하 제한**: 상태 확인은 서버당 `ps` 한 번으로 그 서버의 모든 프로세스를 판단하고, 여러 사람이 보고
  있어도 서버마다 2초에 한 번만 실행합니다. SSH 연결은 열어 두고 재사용합니다.
- **경로 라우팅**: `/all` 은 전체 그룹, `/<그룹 slug>` 는 그 그룹만 보여주고 상태도 그 그룹만 확인합니다.
  그룹 주소에서는 상단 탭에도 그 그룹만 보이고, 설정도 그 그룹의 서버 · 프로세스만 편집합니다.
  (그룹 · 확인 주기 · 가져오기/내보내기는 `/all` 에서만. 다른 그룹의 설정은 손대지 않고 그대로 저장합니다)
  주소를 나눠 보여줄 뿐 접근 제한은 아닙니다. (`/all` 로 들어가면 전체가 보입니다)
- **로그 내보내기**: 로그 화면에서 지금 보이는 줄만(마지막 N줄 · 걸러진 줄) 브라우저에서 바로 파일로 만들거나,
  파일 전체를 Go 서버에서 받습니다. 전체 파일은 메모리에 모으지 않고 SSH 출력을 그대로 흘려보냅니다.

- **적는 것을 줄이기**: 서버 기본 폴더는 비워 두면 `/home/<user>` (root 는 `/root`) 입니다. 로그 폴더 · INI 파일도 `찾아보기` 로 고릅니다.
  시작 스크립트는 기본 폴더 기준 파일 이름만 적거나, 설정 창의 `찾아보기` 로 서버에서
  고릅니다. (찾아보기는 저장된 서버만, SSH 계정의 홈 폴더 아래만) 실행 판단 조건은 `조건` 창의 `실행 중인 프로세스에서 고르기` 로
  지금 떠 있는 프로세스를 고르면 실행 이름과 명령줄 문자열(실행하는 .jar · .py · .sh 등)이 채워지고 바로 몇 개가 맞는지 확인합니다.
  기본 폴더를 바꾸면 파일 이름만 적은 시작 스크립트의 경로가 함께 바뀌므로, 저장할 때 바뀌는 경로를 보여주고 확인합니다.
- **프로그램 설치**: 서버 카드의 `프로그램 설치` 로 아무것도 없는 서버에 처음 설치합니다. 내 PC 의 프로그램 폴더를 열면
  설치 위치(기본 폴더/폴더 이름), 시작 파일(`start*.sh` → `*.sh` → 확장자 없는 실행 파일 순으로 미리 고름), 표시 이름이 채워집니다.
  설치하면 폴더를 올리고(이미 있으면 설치하지 않음) `*.sh` 와 시작 파일에 실행 권한을 준 뒤 프로세스로 등록합니다.
  `설치 후 바로 시작` 이면 시작 전후의 `ps` 를 비교해 새로 뜬 프로세스로 실행 판단 조건을 자동으로 정합니다. (여러 개면 고르게 합니다)
  설정 파일을 먼저 고쳐야 하면 이것을 끄고, 나중에 카드의 `시작` 을 누르면 그때 같은 방식으로 조건을 정합니다.
  (실행 판단 조건이 없는 프로세스의 `시작` 은 모두 이렇게 동작합니다) Java 같은 실행 환경 설치는 하지 않습니다.
- **압축 파일로 설치 · 업데이트**: 설치 · 업데이트 창에서 폴더 대신 압축 파일(`.zip` `.tar.gz` `.tgz` `.tar`)을 열 수 있습니다.
  열면 바로 Go 서버에 압축된 그대로 올려 두고(`env.ini` `[UPLOAD]`, 최대 `MAX_MB`, 1시간 뒤 · 사용 후 · 창을 닫으면 지움) 목록을 보여줍니다.
  설치 · 업데이트할 때 Go 서버가 압축을 풀며 항목을 확인해 서버로 보냅니다. 맨 위가 폴더 하나뿐이면(`myapp-1.2/...`) 벗겨서 넣고,
  압축 파일의 실행 권한과 압축 파일 안을 가리키는 심볼릭 링크는 그대로 둡니다. macOS 가 넣는 `__MACOSX/` · `._*` · `.DS_Store` 는 뺍니다.
  압축 파일은 폴더로만 씁니다. (업데이트에서 파일 하나를 바꿀 때는 그 파일을 직접 고릅니다)
- **서버로 보낼 때 압축**: 업데이트 · 설치 내용은 Go 서버가 gzip 으로 압축해 SSH 로 보내고, 서버가 `tar -xzf` 로 풉니다. (서버에 `gzip` 필요)
- **프로그램 업데이트**: 서버 카드의 `프로그램 업데이트` 를 누르면 왼쪽에 서버(SSH 계정의 홈 폴더 아래만, 기본 폴더에서 시작.
  위의 프로세스 이름을 누르면 그 시작 파일이 있는 폴더로 바로 갑니다), 오른쪽에 내 PC 에서
  연 폴더 · 파일이 보입니다. 양쪽에서 하나씩 고르고 `프로그램 업데이트` 를 누르면 서버의 대상은 `<이름>.bak.<YYYYMMDD-HHmmss>` 로
  남고 내 PC 의 내용이 같은 이름으로 올라갑니다. 폴더는 폴더로, 파일은 파일로만 바꿀 수 있습니다. 다 받은 뒤에만 바꾸므로
  중간에 끊겨도 기존 대상은 그대로입니다. 빈 폴더는 올라가지 않고, 실행 권한은 같은 경로의 기존 파일을 따릅니다.
  창 아래 `업데이트 후 재시작` 에서 고른 프로세스는 업데이트가 성공한 뒤 차례로 재시작합니다. (시작 스크립트가 바꾼 폴더 안에 있거나,
  바꾼 파일과 같은 폴더에 있는 프로세스가 처음에 체크됩니다. 실행 판단 조건과 시작 스크립트가 있어야 재시작할 수 있습니다)
  업데이트가 실패하면 재시작하지 않습니다.

> ⚠ 로그인이 없습니다. 화면에 접속할 수 있는 사람은 누구나 설정을 바꾸고 프로세스를 중지할 수 있으므로
> 내부망에서만 여세요. 상태를 바꾸는 요청은 `Content-Type: application/json` 을 요구해 다른 사이트를 통한
> 요청(CSRF)만 막습니다. (프로그램 업데이트는 `application/x-tar`) 제어 요청은 요청한 IP 와 함께 서버 로그에 남습니다.

## 설정 (`processes.json`)

설정 화면에서 편집하며, Go 서버의 `env.ini` `[PROCESSES] FILE` (기본 `./data/processes.json`)에 저장됩니다.

```json
{
  "pollIntervalMs": 5000,
  "watchIntervalMs": 10000,
  "restartDelayMs": 30000,
  "groups": [{ "slug": "uw", "name": "의왕" }],
  "servers": [
    { "id": "uw-app1", "group": "uw", "label": "의왕 APP 1", "host": "10.0.0.11", "port": 22, "user": "ops", "password": "..." }
  ],
  "processes": [
    {
      "id": "shelter",
      "server": "uw-app1",
      "label": "Shelter",
      "matchName": "java",
      "matchArgs": "/opt/shelter/shelter.jar",
      "logFiles": ["logs/*.log", "/var/log/shelter/*.log"],
      "startScript": "/opt/shelter/start.sh",
      "autoRestart": true
    }
  ]
}
```

| 필드 | 필수 | 설명 |
|---|---|---|
| `pollIntervalMs` | | 상태 확인 주기(ms). 1000 이상, 생략 시 5000 |
| `watchIntervalMs` | | 자동 재시작 확인 주기(ms). 1000 이상, 생략 시 10000 |
| `restartDelayMs` | | 자동 · 화면에서 시작한 뒤 프로세스가 뜨기를 기다리는 시간(ms). 이 시간 안에는 다시 자동 시작하지 않습니다. 1000 이상, 생략 시 30000 |
| `groups[].slug` | ✓ | 주소 경로(`/uw`). 영문 소문자 · 숫자 · `-`. `all` `api` `config` `static` `log-config` 제외 |
| `groups[].name` | ✓ | 탭 · 제목에 보일 이름 |
| `servers[].id` / `group` | ✓ | 고유 id / 그룹 slug |
| `servers[].host` / `port` / `user` / `password` | ✓ (port 제외) | SSH 접속 정보. port 생략 시 22 |
| `servers[].label` | | 표시 이름 (생략 시 id) |
| `servers[].baseDir` | | 기본 폴더 (예: `/home/ops/app`). 비우면 `/home/<user>` (root 는 `/root`). 시작 스크립트를 파일 이름(`start.sh`) · 상대 경로(`bin/start.sh`)로 적으면 이 폴더 기준. 찾아보기 · 프로그램 업데이트 창도 여기서 시작합니다 (홈 폴더 아래일 때) |
| `processes[].id` / `server` | ✓ | 고유 id / 서버 id |
| `processes[].matchName` | | 실행 판단 조건: 실행 파일 이름. `ps` 명령줄 첫 단어의 파일 이름과 정확히 같아야 합니다 (`/usr/bin/java ...` → `java`, `#!/bin/sh` 스크립트 → `sh`). 비우면 보지 않습니다 |
| `processes[].matchArgs` | | 실행 판단 조건: 명령줄에 들어 있어야 하는 문자열 (정규식 아님). 비우면 보지 않습니다. 이것만 쓸 때는 `vi` · `tail` 처럼 같은 경로를 연 다른 프로그램도 맞으므로, 그 프로세스만 가진 문자열로 적으세요 |
| `processes[].matchRegex` | | 실행 판단 조건: 명령줄 전체에 대한 정규식(Go RE2). `matchName` · `matchArgs` 와 함께 쓸 수 없습니다. 다른 프로세스와 겹치지 않게 구체적으로 (`worker\.sh` 는 `com.apple.mdworker.shared` 에도 맞습니다) |
| `processes[].logFiles` | | 로그 파일 패턴 목록 (선택, 최대 10개). 비우면 시작 스크립트 폴더의 한 단계 위 폴더부터 6단계까지의 `*.log` (예: `bin/start.sh` 옆의 `logs/` 도 찾음, 위 폴더가 `/home` 처럼 너무 얕으면 시작 스크립트 폴더부터 5단계). 상대 경로는 시작 스크립트 폴더 기준. 설정 창에서는 `찾아보기` 로 폴더를 고르거나 쉼표로 나눠 적습니다 |
| `processes[].iniFiles` | | INI 파일 패턴 목록 (선택, 최대 10개, `.ini` 로 끝나야 함). 비우면 시작 스크립트 폴더 아래 3단계까지의 `*.ini` |
| `processes[].startScript` | | 시작 · 재시작 때 인자 없이 실행할 스크립트. `/` 로 시작하면 그대로, 아니면 서버 `baseDir` 기준 (`..` 불가). 설정 창의 `찾아보기` 로 서버에서 고를 수 있습니다. 스크립트가 있는 폴더에서 `nohup` 으로 실행하고 출력은 버리므로, 로그는 스크립트가 직접 남겨야 합니다 |
| `processes[].label` | | 표시 이름 (생략 시 id) |
| `processes[].autoRestart` | | `true` 면 조건에 맞는 프로세스가 없을 때 시작 스크립트를 자동 실행합니다. `startScript` 와 실행 판단 조건 필요 |

- 실행 판단 조건(`matchName` · `matchArgs` · `matchRegex`)과 `autoRestart` 는 설정 창이 아니라 모니터링 화면의 `조건` 창에서 바꿉니다.
  설정 창에서 프로세스를 추가하면 조건이 없는 상태(`조건 없음`)로 보이며, 그때는 상태를 확인하지 않고 중지 · 재시작할 수 없습니다.

- 배열 순서가 화면 표시 순서입니다.
- 저장할 때 설정 창을 연 시점의 `version`(파일 내용의 hash)을 같이 보내, 그 사이 `조건` 창 · 다른 사용자 · 직접 수정으로
  파일이 바뀌었으면 덮어쓰지 않고 거부합니다(409). 설정 창은 열 때마다 최신 설정을 다시 읽습니다.
- 규칙은 프론트(`frontend/src/utils/schema.ts`)와 Go 서버(`backend/internal/service/process-service/config.go`)가 같은 내용으로 검사합니다.
- 중지는 실행 판단 조건으로 찾은 PID 에 SIGTERM 을 보내고 10초 기다립니다. 그 안에 종료되지 않으면 실패로 알려주며 SIGKILL 은 보내지 않습니다.
- 예전 설정은 읽을 때 새 형식으로 바뀌고, 다음 저장 때 새 형식으로 저장됩니다. (가져오기도 같습니다)
  - `logFile` · `logDir` · `configDir` → `logFiles` · `iniFiles` 패턴
  - `match` · `watchName` · `watchArgs` → 실행 판단 조건 하나. 감시 조건(`watchName` · `watchArgs`)이 있으면 그것을, 없으면 `match` 를 `matchRegex` 로 씁니다.
    두 조건이 달랐던 프로세스는 상태 표시가 감시 조건 기준으로 바뀌므로 화면에서 한 번 확인하세요.
  `logFile: /a/app.log` → `[/a/app.log]`, `logDir: /a/logs` → `[/a/logs/*, /a/logs/*/*]`, `configDir: /a/conf` → `[/a/conf/*.ini, /a/conf/*/*.ini]`

### 사이트 안내문 (config/README.md, config/API.md)

`public/config/README.md` 는 화면 위에 보이는 소개 · 사용 방법이고(처음엔 펼침), `public/config/API.md` 는
그 아래의 API 명세입니다(처음엔 접힘). 파일을 고치고 새로고침하면 바로 반영되고(빌드한 경우
`build/config/README.md`), 파일을 지우면 패널도 사라집니다. 제목, 목록, 코드, 표, 인용, `**굵게**`,
`[링크](https://...)` 정도의 마크다운만 지원하며, HTML 태그는 글자로 그대로 보입니다.
**API 를 바꾸면 `API.md` 도 같이 고쳐야 합니다.**

## 실행

SSH 접속은 Go 서버만 할 수 있으므로 개발 중에도 Go 서버를 같이 띄웁니다.

```bash
# 1) Go 서버 (:8088)
cd backend/cmd/server
cp example.env.ini env.ini   # 처음 한 번
go run .

# 2) 화면 (:3000, /api 는 :8088 로 전달)
cd frontend
npm install
npm start
```

Go 서버 주소가 다르면 `API_PROXY_TARGET=http://localhost:9000 npm start` 처럼 지정합니다.
`frontend/src/setupProxy.js` 를 수정하면 `npm start` 를 다시 시작해야 합니다.

### 배포

```bash
cd frontend && npm run build && cd ..
rsync -a --delete frontend/build/ backend/cmd/server/build/
cd backend/cmd/server && go build -o main . && ./main
```

`data/`(설정 · known_hosts)는 `build/` 밖에 있으므로 재배포해도 유지됩니다. git 에는 올리지 않습니다.

## 폴더 구조

```
CLAUDE.md                    개발 규칙
backend/                     Go 서버 (아래)
frontend/                    React 화면 (아래)

frontend/src/
  api/client.ts              API 호출 공통 함수 (fetch 래퍼, 에러 문구 처리)
  types/config.ts            설정(processes.json) · 상태 응답 타입
  utils/schema.ts            설정 검사 (화면 · 가져오기 · 불러오기 공통)
  styles/
    tokens.css               색상 등 디자인 토큰 (CSS 변수)
    global.css               공통 스타일 (버튼 · 입력칸 · 상태 점 · 레이아웃)
  hooks/
    useProcessConfig.ts      설정 조회/저장(/api/config)
    useGroupStatus.ts        그룹 상태 폴링(/api/status)
  components/
    Header/                  그룹 탭 (/all: 전체 + 모든 그룹, /uw: 그 그룹만)
    StatusSidebar/           그룹 · 서버 상태 사이드바 (클릭 시 카드로 이동)
    ServerCard/              서버 1대: 접속 상태 + 프로세스 표 + 자동 재시작 상태 + 로그 · INI · 조건 · 시작 · 중지 버튼
    LogViewer/               패턴에 맞는 로그 · INI 파일 목록 → 로그 보기 (줄 수, 필터, 자동 새로고침) 또는 INI 편집
    IniEditor/               INI 파일 편집 · 저장 (CRLF 유지, 동시 수정 감지)
    WatchEditor/             '조건' 창: 실행 판단 조건 · 자동 재시작 변경, 지금 맞는 프로세스 확인
    ConfigEditor/            그룹 · 서버 · 프로세스 편집 / 가져오기 / 내보내기 (그룹 주소에서는 그 그룹만)
    MarkdownPanel/           config/README.md · API.md 를 접이식 패널로 표시
  pages/Dashboard.tsx        그룹 → 서버 → 프로세스 화면 구성, 검색, 사이드바 이동
  App.tsx                    경로 라우팅 (/ → /all)
  setupProxy.js              npm start 에서 /api 를 Go 서버로 전달

backend/
  cmd/server/
    main.go                  진입점 (TEST: 한 번 실행, PRODUCT: 비정상 종료 시 재시작)
    example.env.ini          설정 예시. env.ini 로 복사해 사용 (env.ini 는 git 에 올리지 않음)
    build/                   화면 빌드 결과 (frontend 에서 npm run build 후 복사)
  internal/
    app/                     시작 · 종료 순서
    config/                  env.ini 로더
    container/               설정 저장소 · 모니터 · REST 서버 조립
    service/process-service/
      config.go              설정 타입 · 검사 · 저장 (비밀번호 유지 규칙)
      remote.go              SSH 연결 재사용 · 명령 실행 · known_hosts 확인
      monitor.go             ps 로 상태 확인, kill · 시작 스크립트로 제어
      files.go               패턴에 맞는 로그 · INI 파일 목록(ls), 로그 tail, INI 읽기 · 쓰기, 경로 확인
      match.go               실행 판단 조건 (상태 · 중지 · 자동 재시작 공통), 예전 match · watch 설정 변환
      watchdog.go            자동 재시작, 저장 전 조건 확인
    transport/http-rest/
      rest.go                HTTP 서버 (미들웨어 · SPA 핸들러 연결, 시작 · 종료)
      controller/            /api 경로 등록과 핸들러
      middleware/            CORS · 요청 로그 · SPA
      request/               요청 본문 타입
```

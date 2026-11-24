/**
 * 설정 파일(processes.json)의 구조. Go 서버의 internal/service/process-service/config.go 와 맞춘다.
 *
 *   그룹(groups) ─┬─ 서버(servers, group = 그룹 slug) ─┬─ 프로세스(processes, server = 서버 id)
 *
 * {
 *   "pollIntervalMs": 5000,
 *   "groups":    [{ "slug": "uw", "name": "의왕" }],
 *   "servers":   [{ "id": "uw-app1", "group": "uw", "label": "의왕 APP 1", "host": "10.0.0.11", "port": 22, "user": "ops", "hasPassword": true }],
 *   "processes": [{ "id": "shelter", "server": "uw-app1", "label": "Shelter", "matchName": "java", "matchArgs": "/opt/shelter/shelter.jar",
 *                   "logFiles": ["/opt/shelter/logs/*.log"], "iniFiles": ["/opt/shelter/conf/*.ini"], "startScript": "/opt/shelter/start.sh",
 *                   "autoRestart": true }]
 * }
 */
export interface ProcessConfig {
  /**
   * 조회 · 저장 전용. 서버 설정 파일 내용의 hash. 저장할 때 읽었던 값을 그대로 보내고,
   * 그 사이 다른 곳(감시 창 · 다른 사용자)에서 바뀌었으면 서버가 409 로 거부한다. 내보내기 파일에는 넣지 않는다.
   */
  version?: string;
  /** 상태 확인 주기(ms). 없으면 5000 */
  pollIntervalMs?: number;
  /** 자동 재시작 확인 주기(ms). 없으면 10000 */
  watchIntervalMs?: number;
  /** 시작한 뒤 프로세스가 뜨기를 기다리는 시간(ms). 이 시간 안에는 다시 자동 시작하지 않는다. 없으면 30000 */
  restartDelayMs?: number;
  /** 배열 순서가 화면 표시 순서 */
  groups: GroupEntry[];
  servers: ServerEntry[];
  processes: ProcessEntry[];
}

export interface GroupEntry {
  /** URL 경로 (/uw). 영문 소문자 · 숫자 · '-' */
  slug: string;
  /** 화면 표시 이름 (의왕) */
  name: string;
}

export interface ServerEntry {
  id: string;
  /** 그룹 slug */
  group: string;
  /** 화면 표시 이름 (없으면 id) */
  label?: string;
  host: string;
  /** 없으면 22 */
  port?: number;
  user: string;
  /** 쓰기 전용. 저장할 때만 보내고, 비워 두면 서버가 기존 비밀번호를 유지한다. */
  password?: string;
  /** 조회 전용. 서버에 비밀번호가 저장되어 있으면 true */
  hasPassword?: boolean;
  /**
   * 기본 폴더 (예: /home/ops/app). 프로세스의 시작 스크립트를 파일 이름(start.sh)이나 상대 경로(bin/start.sh)로
   * 적으면 이 폴더 기준이다. 프로그램 업데이트 창 · 시작 스크립트 찾아보기도 여기서 시작한다.
   */
  baseDir?: string;
}

export interface ProcessEntry {
  id: string;
  /** 서버 id */
  server: string;
  /** 화면 표시 이름 (없으면 id) */
  label?: string;
  /**
   * 실행 판단 조건. ps 에서 이 조건에 맞는 프로세스를 이 항목의 실행 인스턴스로 본다.
   * 상태 표시 · 중지 · 자동 재시작이 모두 이 조건 하나를 쓴다. 설정 창이 아니라 모니터링 화면의 '조건' 창에서 바꾼다.
   *   이름 · 문자열 방식: matchName · matchArgs 중 하나 이상 (둘 다 적으면 한 프로세스가 둘 다 만족)
   *   정규식 방식:       matchRegex (이름 · 문자열과 함께 쓸 수 없다)
   * 모두 비어 있으면 상태를 확인하지 않는다. (설정에서 막 추가한 프로세스)
   */
  /** 실행 파일 이름. ps 명령줄 첫 단어의 파일 이름과 정확히 같아야 한다. (예: java) */
  matchName?: string;
  /** 명령줄에 들어 있어야 하는 문자열. 정규식이 아니다. (예: /opt/app/app.jar) */
  matchArgs?: string;
  /** 명령줄 전체에 대한 정규식 (예: java .*app\.jar) */
  matchRegex?: string;
  /** 로그 파일 패턴 (예: /opt/app/logs/*.log). 로그 버튼에는 이 패턴에 맞는 파일만 보이고, tail 로 본다. */
  logFiles?: string[];
  /** INI 파일 패턴 (예: /opt/app/conf/*.ini). INI 버튼에는 이 패턴에 맞는 파일만 보이고, 읽고 고친다. */
  iniFiles?: string[];
  /** 시작 · 재시작 때 실행할 스크립트의 절대 경로 */
  startScript?: string;
  /**
   * 자동 재시작. 실행 판단 조건에 맞는 프로세스가 ps 에 없으면 Go 서버가 시작 스크립트를 실행한다. (조건 · startScript 필요)
   * 실행 판단 조건과 함께 모니터링 화면의 '조건' 창에서 바꾼다. (PUT /api/processes/:id/watch)
   */
  autoRestart?: boolean;
}

/** GET /api/status 응답의 서버 하나 */
export interface ServerStatus {
  id: string;
  /** SSH 접속 · ps 실행에 실패한 이유 */
  error?: string;
  checkedAt: string;
  processes: { id: string; instances: ProcessInstance[]; watch?: WatchStatus }[];
}

/** 자동 재시작을 켠 프로세스의 자동 재시작 상태. Go 서버 메모리에만 있다. (실행 여부는 instances 로 본다) */
export interface WatchStatus {
  /** 화면에서 중지해 자동 재시작이 멈춘 상태. 시작 · 재시작하면 다시 켜진다. */
  paused?: boolean;
  /** Go 서버가 켜진 뒤 자동으로 시작한 횟수 */
  restarts: number;
  lastRestartAt?: string;
  lastError?: string;
}

/** GET /api/processes/:id/ini 응답. hash 는 저장할 때 baseHash 로 돌려보내 그 사이 바뀌었는지 확인한다. */
export interface IniFile {
  content: string;
  hash: string;
}

/** GET /api/processes/:id/files 응답 */
export interface FileList {
  /** 등록한 패턴 */
  patterns: string[];
  /** 패턴에 맞는 파일, 최근 수정 순 */
  entries: FileEntry[];
  /** 항목이 너무 많아 오래된 것을 잘랐는지 */
  truncated?: boolean;
}

export interface FileEntry {
  /** 절대 경로 */
  path: string;
  type: 'file' | 'link';
  size: number;
  /** ls 가 보여주는 수정 시각 ("Nov 19 11:15") */
  modified: string;
  /** 심볼릭 링크가 가리키는 경로 */
  target?: string;
}

/** GET /api/servers/:id/browse 응답: 서버의 폴더 하나 (프로그램 업데이트) */
export interface RemoteDir {
  /** SSH 계정의 홈 폴더 (실제 경로). 이 위로는 올라갈 수 없다. */
  home: string;
  /** 지금 폴더 (실제 경로) */
  path: string;
  /** 폴더 먼저, 이름 순. 심볼릭 링크는 가리키는 대상의 종류(dir · file)로 온다. */
  entries: RemoteEntry[];
}

export interface RemoteEntry {
  /** 절대 경로 */
  path: string;
  /** link 는 가리키는 대상이 없는 링크, other 는 장치 파일 등 */
  type: 'dir' | 'file' | 'link' | 'other';
  size: number;
  /** ls 가 보여주는 수정 시각 ("Nov 19 11:15") */
  modified: string;
}

/** POST /api/servers/:id/update 응답 */
export interface UpdateResult {
  /** 바뀐 대상 */
  path: string;
  /** 이전 내용을 옮겨 둔 경로 (<대상>.bak.YYYYMMDD-HHmmss) */
  backup: string;
  files: number;
  bytes: number;
}

export interface ProcessInstance {
  pid: number;
  cpu: number;
  mem: number;
  /** 실행 시간 ([[dd-]hh:]mm:ss) */
  elapsed: string;
  command: string;
}

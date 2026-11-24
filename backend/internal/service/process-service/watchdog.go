package processes

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Jaeun-Choi98/modules/logger"
)

// 자동 재시작(autoRestart) 감시
//
//	확인 주기(watchIntervalMs)마다 자동 재시작을 켠 프로세스의 서버에서 ps 를 실행해(캐시 공유)
//	실행 판단 조건(match.go)에 맞는 프로세스를 찾는다. 화면의 상태 표시와 같은 조건이다.
//	없으면 시작 스크립트를 실행하고, 그 뒤 재시작 대기(restartDelayMs) 동안은 프로세스가 뜨기를 기다린다.
//	계속 뜨지 않으면 재시작 대기마다 횟수 제한 없이 다시 시작한다. 두 값은 설정 파일에서 매번 읽으므로 저장하면 바로 반영된다.
const (
	defaultWatchInterval = 10 * time.Second
	defaultRestartDelay  = 30 * time.Second
)

// watchInterval 은 자동 재시작 확인 주기다. (설정에 없으면 기본값)
func (c *Config) watchInterval() time.Duration {
	if c.WatchIntervalMs > 0 {
		return time.Duration(c.WatchIntervalMs) * time.Millisecond
	}
	return defaultWatchInterval
}

// restartDelay 는 시작한 뒤 다시 자동 시작하기 전까지 기다리는 시간이다. (설정에 없으면 기본값)
func (c *Config) restartDelay() time.Duration {
	if c.RestartDelayMs > 0 {
		return time.Duration(c.RestartDelayMs) * time.Millisecond
	}
	return defaultRestartDelay
}

type WatchStatus struct {
	// 화면에서 중지해 자동 재시작이 멈춘 상태. 시작 · 재시작하면 다시 켜진다.
	Paused bool `json:"paused,omitempty"`
	// Go 서버가 켜진 뒤 자동으로 시작한 횟수
	Restarts int `json:"restarts"`
	// 마지막으로 자동 시작한 시각
	LastRestartAt *time.Time `json:"lastRestartAt,omitempty"`
	// 마지막 자동 시작 · 상태 확인의 오류 (성공하면 비운다)
	LastError string `json:"lastError,omitempty"`
}

// watchState 는 Go 서버 메모리에만 둔다. Go 서버를 다시 켜면 '중지로 멈춤' 도 풀린다.
type watchState struct {
	paused bool
	// 마지막 시작 시각 (자동 · 화면 모두). 이후 재시작 대기 동안은 자동으로 시작하지 않는다.
	lastAttempt   time.Time
	restarts      int
	lastRestartAt time.Time
	lastError     string
}

// watch 는 m.mu 를 잡은 상태에서 호출한다.
func (m *Monitor) watch(processID string) *watchState {
	state := m.watches[processID]
	if state == nil {
		state = &watchState{}
		m.watches[processID] = state
	}
	return state
}

// StartWatchdog 은 자동 재시작 감시를 시작한다. Close 하면 멈춘다.
func (m *Monitor) StartWatchdog() {
	ctx, cancel := context.WithCancel(context.Background())
	m.stopWatchdog = cancel
	go func() {
		// 확인 주기가 설정에서 바뀔 수 있으므로 매번 설정에서 읽어 다음 확인까지 기다린다.
		interval := defaultWatchInterval
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
				interval = m.watchOnce(ctx)
			}
		}
	}()
}

// watchOnce 는 자동 재시작을 켠 모든 프로세스를 서버마다 동시에 한 번 확인하고, 다음 확인까지의 간격을 돌려준다.
func (m *Monitor) watchOnce(ctx context.Context) time.Duration {
	config, err := m.store.Load()
	if err != nil {
		logger.Errorf("[WATCHDOG] failed to load processes config, err: %v", err)
		return defaultWatchInterval
	}
	delay := config.restartDelay()
	servers := map[string]Server{}
	for _, s := range config.Servers {
		servers[s.ID] = s
	}
	watched := map[string][]Process{}
	for _, p := range config.Processes {
		if p.AutoRestart && p.hasCondition() && p.StartScript != "" {
			p.StartScript = servers[p.Server].scriptPath(p.StartScript)
			watched[p.Server] = append(watched[p.Server], p)
		}
	}

	var wg sync.WaitGroup
	for serverID, list := range watched {
		server, ok := servers[serverID]
		if !ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			snapshot := m.ps(ctx, server, false)
			for _, p := range list {
				m.watchProcess(ctx, server, p, snapshot, delay)
			}
		}()
	}
	wg.Wait()
	return config.watchInterval()
}

func (m *Monitor) watchProcess(ctx context.Context, server Server, process Process, snapshot psSnapshot, delay time.Duration) {
	m.mu.Lock()
	state := m.watch(process.ID)
	if snapshot.err != nil {
		state.lastError = "상태 확인 실패: " + snapshot.err.Error()
		m.mu.Unlock()
		return
	}
	if len(findInstances(process, snapshot.rows)) > 0 {
		state.lastError = ""
		m.mu.Unlock()
		return
	}
	// 화면에서 중지했거나, 방금 시작해 뜨기를 기다리는 중이거나, 화면에서 시작 · 중지가 진행 중이면 건너뛴다.
	if state.paused || time.Since(state.lastAttempt) < delay || m.busy[process.ID] {
		m.mu.Unlock()
		return
	}
	m.busy[process.ID] = true
	state.lastAttempt = time.Now()
	m.mu.Unlock()

	err := m.runStart(ctx, server, process)

	m.mu.Lock()
	delete(m.busy, process.ID)
	state.restarts++
	state.lastRestartAt = time.Now()
	state.lastError = ""
	if err != nil {
		state.lastError = err.Error()
	}
	restarts := state.restarts
	m.mu.Unlock()

	if err != nil {
		logger.Errorf("[WATCHDOG] %s: 조건에 맞는 프로세스가 없어 시작했지만 실패 (%d번째), err: %v", process.ID, restarts, err)
	} else {
		logger.Warnf("[WATCHDOG] %s: 조건에 맞는 프로세스가 없어 시작 스크립트를 실행했습니다 (%d번째)", process.ID, restarts)
	}
}

// watchStatus 는 상태 응답에 넣을 자동 재시작 상태다. 자동 재시작을 켜지 않았으면 nil.
func (m *Monitor) watchStatus(process Process) *WatchStatus {
	if !process.AutoRestart {
		return nil
	}
	status := &WatchStatus{}

	m.mu.Lock()
	defer m.mu.Unlock()
	if state := m.watches[process.ID]; state != nil {
		status.Paused = state.paused
		status.Restarts = state.restarts
		status.LastError = state.lastError
		if !state.lastRestartAt.IsZero() {
			at := state.lastRestartAt
			status.LastRestartAt = &at
		}
	}
	return status
}

// WatchTest 는 저장하기 전의 실행 판단 조건(name · args 또는 regex)으로 지금 서버의 ps 에서 찾은 프로세스를 돌려준다.
// 모니터링 화면의 '조건' 창에서 저장 전에 맞는 프로세스를 확인하는 데 쓴다.
func (m *Monitor) WatchTest(ctx context.Context, processID, name, args, regex string) ([]Instance, error) {
	_, server, err := m.find(processID)
	if err != nil {
		return nil, err
	}
	process := Process{MatchName: strings.TrimSpace(name), MatchArgs: strings.TrimSpace(args), MatchRegex: strings.TrimSpace(regex)}
	switch {
	case !process.hasCondition():
		return nil, &UserError{http.StatusBadRequest, "실행 이름 · 명령줄 문자열이나 정규식을 적어야 합니다"}
	case process.MatchRegex != "" && (process.MatchName != "" || process.MatchArgs != ""):
		return nil, &UserError{http.StatusBadRequest, "정규식은 실행 이름 · 명령줄 문자열과 함께 쓸 수 없습니다"}
	case process.MatchRegex != "":
		if _, err := regexp.Compile(process.MatchRegex); err != nil {
			return nil, &UserError{http.StatusBadRequest, "올바른 정규식이 아닙니다: " + err.Error()}
		}
	}
	snapshot := m.ps(ctx, server, false)
	if snapshot.err != nil {
		return nil, snapshot.err
	}
	return findInstances(process, snapshot.rows), nil
}

// RunningProcesses 는 서버에서 지금 실행 중인 프로세스 목록이다. (조건 창에서 고르면 실행 판단 조건을 채운다)
// PID 1 · 커널 스레드 · 상태 확인용 ps 는 뺀다.
func (m *Monitor) RunningProcesses(ctx context.Context, serverID string) ([]Instance, error) {
	server, err := m.findServer(serverID)
	if err != nil {
		return nil, err
	}
	snapshot := m.ps(ctx, server, false)
	if snapshot.err != nil {
		return nil, snapshot.err
	}
	rows := []Instance{}
	for _, row := range snapshot.rows {
		if isUserProcess(row) {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

package processes

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
)

// 실행 판단 조건 (Process.MatchName · MatchArgs · MatchRegex)
//
//	상태 표시(PID · CPU · MEM · STOPPED), 중지(kill 대상), 자동 재시작(없으면 시작)이 모두 이 조건 하나로 프로세스를 찾는다.
//	이름 · 문자열 방식: 명령줄 첫 단어의 파일 이름이 matchName 과 같고, 명령줄에 matchArgs 가 들어 있는 프로세스. 비워 둔 쪽은 보지 않는다.
//	정규식 방식:       명령줄 전체가 matchRegex 에 맞는 프로세스.
//	상태 확인용으로 실행한 ps 자체와 PID 1 은 어느 방식이든 제외한다.

// hasCondition 은 실행 판단 조건이 하나라도 있는지다.
func (p Process) hasCondition() bool {
	return p.MatchName != "" || p.MatchArgs != "" || p.MatchRegex != ""
}

// isUserProcess 는 PID 1 · 상태 확인용 ps 자체 · 커널 스레드([kworker/0:1] 등)가 아닌 프로세스인지다.
func isUserProcess(row Instance) bool {
	command := strings.TrimSpace(row.Command)
	return row.PID > 1 && command != "" && !strings.HasPrefix(command, "[") && !strings.Contains(command, psArgs)
}

// findInstances 는 ps 결과에서 실행 판단 조건에 맞는 프로세스를 고른다. 조건이 없거나 정규식이 잘못되었으면 빈 목록이다.
func findInstances(p Process, rows []Instance) []Instance {
	instances := []Instance{}
	if !p.hasCondition() {
		return instances
	}
	var pattern *regexp.Regexp
	if p.MatchRegex != "" {
		var err error
		if pattern, err = regexp.Compile(p.MatchRegex); err != nil {
			return instances
		}
	}
	for _, row := range rows {
		if !isUserProcess(row) {
			continue
		}
		fields := strings.Fields(row.Command)
		var ok bool
		if pattern != nil {
			ok = pattern.MatchString(row.Command)
		} else {
			ok = (p.MatchName == "" || path.Base(fields[0]) == p.MatchName) && strings.Contains(row.Command, p.MatchArgs)
		}
		if ok {
			instances = append(instances, row)
		}
	}
	return instances
}

// migrateMatch 는 예전 설정(화면 표시용 match 정규식 + 자동 재시작용 watchName · watchArgs)을 조건 하나로 바꿔 읽는다.
// 다음에 저장하면 새 필드로 저장된다.
//
//	watchName · watchArgs 가 있으면 → matchName · matchArgs (자동 재시작이 보던 조건을 그대로 쓴다)
//	없으면 match                    → matchRegex
func migrateMatch(data []byte, c *Config) {
	var legacy struct {
		Processes []struct {
			Match     string `json:"match"`
			WatchName string `json:"watchName"`
			WatchArgs string `json:"watchArgs"`
		} `json:"processes"`
	}
	if json.Unmarshal(data, &legacy) != nil || len(legacy.Processes) != len(c.Processes) {
		return
	}
	for i, old := range legacy.Processes {
		p := &c.Processes[i]
		if p.hasCondition() {
			continue
		}
		if old.WatchName != "" || old.WatchArgs != "" {
			p.MatchName, p.MatchArgs = old.WatchName, old.WatchArgs
		} else {
			p.MatchRegex = old.Match
		}
	}
}

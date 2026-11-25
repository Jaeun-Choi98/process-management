package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// 같은 IP 에서 연달아 이만큼 틀리면 lockDuration 동안 로그인을 막는다.
	maxFailures  = 5
	lockDuration = time.Minute
	// 전체 로그 파일 내려받기는 <a href> 로 받아 헤더를 실을 수 없어서, 잠깐 쓰는 1회용 표를 주소에 붙인다.
	ticketTTL = 30 * time.Second
)

// Error 는 화면에 보여줄 로그인 오류다.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

type failure struct {
	count int
	until time.Time
}

// Auth 는 계정 하나(config.AuthUser · AuthPasswordSHA256)로 로그인하고, 로그인한 브라우저 탭마다 토큰을 준다.
// 토큰은 메모리에만 두므로 Go 서버를 다시 시작하면 모두 다시 로그인해야 한다.
type Auth struct {
	user string
	// 비밀번호의 sha256 (16진수 소문자). 비어 있거나 형식이 틀리면 로그인할 수 없다.
	hash string
	// 이 시간 동안 요청이 없으면 토큰이 만료된다.
	idle time.Duration

	mu       sync.Mutex
	sessions map[string]time.Time // 토큰 → 마지막 사용 시각
	tickets  map[string]time.Time // 1회용 표 → 만료 시각
	failures map[string]*failure  // IP → 연속 실패
	now      func() time.Time
}

func New(user, hash string, idle time.Duration) *Auth {
	return &Auth{
		user:     user,
		hash:     strings.ToLower(strings.TrimSpace(hash)),
		idle:     idle,
		sessions: map[string]time.Time{},
		tickets:  map[string]time.Time{},
		failures: map[string]*failure{},
		now:      time.Now,
	}
}

// Configured 는 env.ini 에 아이디와 sha256 비밀번호가 제대로 들어 있는지 알려준다.
func (a *Auth) Configured() bool {
	if a.user == "" || len(a.hash) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(a.hash)
	return err == nil
}

// Login 은 아이디 · 비밀번호가 맞으면 새 토큰을 돌려준다.
func (a *Auth) Login(user, password, ip string) (string, error) {
	if !a.Configured() {
		return "", &Error{http.StatusServiceUnavailable, "로그인 계정이 설정되지 않았습니다. 관리자에게 문의하세요"}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.prune(now)

	f := a.failures[ip]
	if f != nil && now.Before(f.until) {
		return "", &Error{http.StatusTooManyRequests, fmt.Sprintf("로그인을 여러 번 실패했습니다. %d초 뒤에 다시 시도하세요", int(f.until.Sub(now).Seconds())+1)}
	}

	sum := sha256.Sum256([]byte(password))
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(a.user)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(a.hash)) == 1
	if !userOK || !passOK {
		if f == nil || !f.until.IsZero() {
			f = &failure{}
			a.failures[ip] = f
		}
		f.count++
		if f.count >= maxFailures {
			f.until = now.Add(lockDuration)
		}
		return "", &Error{http.StatusUnauthorized, "아이디 또는 비밀번호가 맞지 않습니다"}
	}

	delete(a.failures, ip)
	token := randomToken()
	a.sessions[token] = now
	return token, nil
}

// Check 는 토큰이 유효한지 확인하고, 유효하면 마지막 사용 시각을 늘린다.
func (a *Auth) Check(token string) bool {
	if token == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	last, ok := a.sessions[token]
	if !ok {
		return false
	}
	if now.Sub(last) > a.idle {
		delete(a.sessions, token)
		return false
	}
	a.sessions[token] = now
	return true
}

func (a *Auth) Logout(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
}

// Ticket 은 로그인한 토큰으로 1회용 표를 만든다. (Check 를 통과한 요청에서만 부른다)
func (a *Auth) Ticket() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	ticket := randomToken()
	a.tickets[ticket] = a.now().Add(ticketTTL)
	return ticket
}

// UseTicket 은 표가 유효하면 지우고 true 를 돌려준다. 한 번만 쓸 수 있다.
func (a *Auth) UseTicket(ticket string) bool {
	if ticket == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	until, ok := a.tickets[ticket]
	delete(a.tickets, ticket)
	return ok && a.now().Before(until)
}

// prune 은 만료된 토큰 · 표 · 실패 기록을 지운다. 로그인할 때마다 부르므로 따로 도는 작업이 필요 없다.
func (a *Auth) prune(now time.Time) {
	for token, last := range a.sessions {
		if now.Sub(last) > a.idle {
			delete(a.sessions, token)
		}
	}
	for ticket, until := range a.tickets {
		if !now.Before(until) {
			delete(a.tickets, ticket)
		}
	}
	for ip, f := range a.failures {
		if !f.until.IsZero() && !now.Before(f.until) {
			delete(a.failures, ip)
		}
	}
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 는 실패하지 않는다. (Go 1.24 부터 실패 시 프로그램을 멈춘다)
		panic(err)
	}
	return hex.EncodeToString(b)
}

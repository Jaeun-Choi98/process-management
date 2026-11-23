package rest

import (
	"context"
	"net"
	"net/http"
	"pjt/internal/config"
	processes "pjt/internal/service/process-service"
	"pjt/internal/transport/http-rest/controller"
	"pjt/internal/transport/http-rest/middleware"
	"time"

	"github.com/Jaeun-Choi98/modules/logger"
	"github.com/gin-gonic/gin"
)

type RestServer struct {
	server   *http.Server
	listener net.Listener
	monitor  *processes.Monitor
	useTLS   bool
	cert     string
	key      string
}

// New 는 라우터에 미들웨어 · API(controller) · SPA 핸들러를 붙여 서버를 만든다.
// monitor 는 서버를 종료할 때 SSH 연결과 자동 재시작 감시를 함께 멈추려고 받는다.
func New(cfg *config.Configuration, ctl *controller.Controller, monitor *processes.Monitor) *RestServer {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery(), middleware.LogMiddleware(), middleware.NewCORSMiddleware(cfg.Cors, true))

	ctl.RoutePath(router)
	router.NoRoute(middleware.SpaHandler("/api", cfg.SpaDir, cfg.SpaIndex))

	return &RestServer{
		server: &http.Server{
			Addr:        cfg.RestIp + ":" + cfg.RestPort,
			Handler:     router,
			ReadTimeout: 5 * time.Second,
		},
		monitor: monitor,
		useTLS:  cfg.TLS == 1,
		cert:    cfg.SSLCERT,
		key:     cfg.SSLKEY,
	}
}

// Listen 은 포트를 바인딩한다. 포트 사용 중 같은 바인딩 실패를 Serve 전에 동기적으로 알 수 있다.
func (s *RestServer) Listen() error {
	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return err
	}
	s.listener = ln
	return nil
}

// Serve 는 Listen 으로 바인딩한 포트에서 요청을 처리하며 Shutdown 될 때까지 블록된다.
// Shutdown 으로 정상 종료되면 http.ErrServerClosed 를 돌려준다.
func (s *RestServer) Serve() error {
	logger.Infof("[REST] http rest server goroutine start")

	if s.useTLS {
		return s.server.ServeTLS(s.listener, s.cert, s.key)
	}
	return s.server.Serve(s.listener)
}

func (s *RestServer) Shutdown(ctx context.Context) error {
	err := s.server.Shutdown(ctx)
	s.monitor.Close()
	// Serve 에 들어가기 전에 종료된 경우 http.Server 가 리스너를 모르므로 직접 닫는다. ( 이미 닫힌 경우의 에러는 무시 )
	if s.listener != nil {
		_ = s.listener.Close()
	}
	logger.Infoln("[REST] http rest server goroutine is terminated")
	return err
}

package container

import (
	"pjt/internal/config"
	processes "pjt/internal/service/process-service"
	rest "pjt/internal/transport/http-rest"
	"pjt/internal/transport/http-rest/controller"
)

var myContainer *Container

type Container struct {
	restServer *rest.RestServer
}

func NewContainer(cfg *config.Configuration) {

	if myContainer != nil {
		return
	}

	n := &Container{}

	// 그룹 · 서버 · 프로세스 설정 파일과, SSH 로 상태 확인 · 제어하는 모니터
	store := processes.NewStore(cfg.ProcessesFile)
	monitor := processes.NewMonitor(store, cfg.KnownHostsFile)
	// 자동 재시작을 켠 프로세스를 감시한다. ( 서버 종료 시 RestServer.Shutdown 에서 멈춘다 )
	monitor.StartWatchdog()

	// 설치 · 업데이트용 압축 파일 임시 보관 ( 전에 남은 파일은 지운다 )
	uploads := processes.NewUploads(cfg.UploadDir, cfg.UploadMaxBytes)

	n.restServer = rest.New(cfg, controller.NewController(store, monitor, uploads), monitor)

	myContainer = n
}

func GetRestServer() *rest.RestServer {
	return myContainer.restServer
}

func DiscardContainer() {
	myContainer = nil
}

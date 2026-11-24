package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"

	processes "pjt/internal/service/process-service"
	"pjt/internal/transport/http-rest/request"

	"github.com/Jaeun-Choi98/modules/logger"
	"github.com/gin-gonic/gin"
)

// Controller 는 REST API 경로를 등록하고 요청을 서비스(process-service)로 넘긴다.
type Controller struct {
	store   *processes.Store
	monitor *processes.Monitor
}

func NewController(store *processes.Store, monitor *processes.Monitor) *Controller {
	return &Controller{store: store, monitor: monitor}
}

// RoutePath 는 API 를 등록한다. API 는 /api 아래에 두고, 매칭되지 않은 경로는 rest 의 NoRoute(SPA 핸들러)가 처리한다.
func (ctl *Controller) RoutePath(router *gin.Engine) {
	store, monitor := ctl.store, ctl.monitor

	api := router.Group("/api")
	api.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// 로그 레벨 조회 및 실행 중 변경 ( 재기동 시 env.ini 값으로 복귀 )
	// ex) curl 'host/log-config?level=debug'
	router.GET("/log-config", handleLogConfig)

	// 그룹 · 서버 · 프로세스 설정, SSH 로 확인한 프로세스 상태 · 로그, 프로세스 제어
	api.GET("/config", handleGetConfig(store))
	api.PUT("/config", requireJSON, handlePutConfig(store, monitor))
	api.GET("/status", handleStatus(monitor))
	api.GET("/processes/:id/files", handleFiles(monitor))
	api.GET("/processes/:id/log", handleLog(monitor))
	api.GET("/processes/:id/log/download", handleDownloadLog(monitor))
	api.GET("/processes/:id/ini", handleReadINI(monitor))
	api.PUT("/processes/:id/ini", requireJSON, handleWriteINI(monitor))
	api.PUT("/processes/:id/watch", requireJSON, handleUpdateWatch(store, monitor))
	api.GET("/processes/:id/watch-test", handleWatchTest(monitor))
	api.POST("/processes/:id/:action", requireJSON, handleControl(monitor))

	// 프로그램 업데이트: 서버의 폴더 · 파일 둘러보기와 바꾸기
	api.GET("/servers/:id/browse", handleBrowse(monitor))
	api.GET("/servers/:id/ps", handleRunningProcesses(monitor))
	api.POST("/servers/:id/update", requireTar, handleUpdate(monitor))
	api.POST("/servers/:id/install", requireTar, handleInstall(monitor))
}

// handleLogConfig 는 현재 로그 레벨을 돌려준다. 쿼리를 주면 레벨을 바꾼 뒤 돌려준다.
//
//	GET /log-config                조회
//	GET /log-config?level=debug    레벨 변경 (debug/info/warn/error)
func handleLogConfig(c *gin.Context) {
	if levelStr, ok := c.GetQuery("level"); ok {
		level, err := logger.ParseLevel(levelStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		logger.SetLevel(level)
		logger.Warnf("[REST] log level changed: %s, remote: %s", level, c.ClientIP())
	}
	c.JSON(http.StatusOK, gin.H{"level": logger.GetLevel().String()})
}

// 설정 저장 요청 본문 최대 크기
const maxConfigBodyBytes = 1 << 20

// requireJSON 은 상태를 바꾸는 요청(PUT · POST)에 Content-Type: application/json 을 요구한다.
// 다른 사이트의 <form> 은 이 헤더로 요청을 보낼 수 없으므로, 다른 사이트를 통해 몰래 프로세스를 중지시키는 요청(CSRF)을 막는다.
func requireJSON(c *gin.Context) {
	if c.ContentType() != "application/json" {
		c.AbortWithStatusJSON(http.StatusUnsupportedMediaType, gin.H{"error": "Content-Type: application/json 이 필요합니다"})
		return
	}
	c.Next()
}

// requireTar 는 업데이트 요청에 Content-Type: application/x-tar 를 요구한다.
// requireJSON 과 같은 이유(다른 사이트의 <form> 은 이 헤더로 보낼 수 없다)로 CSRF 를 막는다.
func requireTar(c *gin.Context) {
	if c.ContentType() != "application/x-tar" {
		c.AbortWithStatusJSON(http.StatusUnsupportedMediaType, gin.H{"error": "Content-Type: application/x-tar 가 필요합니다"})
		return
	}
	c.Next()
}

// respondError 는 화면에 보여줄 오류(UserError)는 그 상태 코드로, SSH 실패 등은 502 로 응답한다.
func respondError(c *gin.Context, err error) {
	var userErr *processes.UserError
	if errors.As(err, &userErr) {
		c.JSON(userErr.Status, gin.H{"error": userErr.Message})
		return
	}
	c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
}

// GET /api/config: 설정 조회 ( 비밀번호 대신 hasPassword )
func handleGetConfig(store *processes.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		config, err := store.Load()
		if err != nil {
			logger.Errorf("[REST] failed to load processes config, err: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "설정 파일을 읽지 못했습니다"})
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, processes.Public(config))
	}
}

// PUT /api/config: 설정 전체 교체
func handlePutConfig(store *processes.Store, monitor *processes.Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxConfigBodyBytes))
		if err != nil {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "요청 본문이 너무 큽니다"})
			return
		}

		config, err := store.Save(body)
		var userErr *processes.UserError
		switch {
		case errors.Is(err, processes.ErrInvalid):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		case errors.As(err, &userErr): // 409: 설정 창을 연 뒤 다른 곳에서 바뀜
			respondError(c, err)
			return
		case err != nil:
			logger.Errorf("[REST] failed to save processes config, err: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "설정 파일을 저장하지 못했습니다"})
			return
		}

		monitor.Reset()
		logger.Warnf("[REST] processes config saved, remote: %s", c.ClientIP())
		c.JSON(http.StatusOK, processes.Public(config))
	}
}

// GET /api/status?group=uw : 그룹(all 이면 전체)의 서버별 프로세스 상태
func handleStatus(monitor *processes.Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		servers, err := monitor.GroupStatus(c.Request.Context(), c.DefaultQuery("group", "all"))
		if err != nil {
			respondError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"servers": servers})
	}
}

// GET /api/processes/:id/files?root=log|ini : 로그 · INI 파일 패턴에 맞는 파일 목록 ( 최근 수정 순 )
func handleFiles(monitor *processes.Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		list, err := monitor.Files(c.Request.Context(), c.Param("id"), c.DefaultQuery("root", processes.RootLog))
		if err != nil {
			respondError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, list)
	}
}

// GET /api/processes/:id/log?path=/opt/app/logs/app.log&lines=200 : 로그 파일 패턴에 맞는 파일의 끝부분
func handleLog(monitor *processes.Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		lines, _ := strconv.Atoi(c.DefaultQuery("lines", "200"))
		output, err := monitor.Log(c.Request.Context(), c.Param("id"), c.Query("path"), lines)
		if err != nil {
			respondError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"output": output})
	}
}

// GET /api/processes/:id/log/download?path=/opt/app/logs/app.log : 로그 파일 전체 내려받기 ( 첨부 파일로 응답 )
func handleDownloadLog(monitor *processes.Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, file := c.Param("id"), c.Query("path")
		started := false
		err := monitor.DownloadLog(c.Request.Context(), id, file, func(size int64) io.Writer {
			started = true
			c.Header("Content-Type", "application/octet-stream")
			c.Header("Content-Disposition", attachmentName(path.Base(file)))
			c.Header("Content-Length", strconv.FormatInt(size, 10))
			c.Header("Cache-Control", "no-store")
			c.Status(http.StatusOK)
			return c.Writer
		})
		switch {
		case err != nil && !started:
			respondError(c, err)
		case err != nil:
			// 이미 보내기 시작해서 상태 코드를 바꿀 수 없다. (브라우저에는 내려받기 실패로 보인다)
			logger.Warnf("[REST] log download interrupted: %s %s, remote: %s, err: %v", id, file, c.ClientIP(), err)
		default:
			logger.Infof("[REST] log downloaded: %s %s, remote: %s", id, file, c.ClientIP())
		}
	}
}

// attachmentName 은 Content-Disposition 값이다. 영문 외 글자 · 공백이 있는 이름은 filename* 로 함께 보낸다.
func attachmentName(name string) string {
	fallback := []rune(name)
	for i, r := range fallback {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_') {
			fallback[i] = '_'
		}
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, string(fallback), url.PathEscape(name))
}

// GET /api/processes/:id/ini?path=/opt/app/conf/app.ini : INI 파일 패턴에 맞는 파일 내용과 hash
func handleReadINI(monitor *processes.Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		file, err := monitor.ReadINI(c.Request.Context(), c.Param("id"), c.Query("path"))
		if err != nil {
			respondError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, file)
	}
}

// PUT /api/processes/:id/ini?path=/opt/app/conf/app.ini {"content": "...", "baseHash": "..."} : *.ini 파일 저장 ( 누가 했는지 로그에 남긴다 )
func handleWriteINI(monitor *processes.Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body request.WriteINI
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "본문 형식이 올바르지 않습니다: " + err.Error()})
			return
		}
		id, path := c.Param("id"), c.Query("path")
		file, err := monitor.WriteINI(c.Request.Context(), id, path, body.Content, body.BaseHash)
		if err != nil {
			logger.Warnf("[REST] ini write failed: %s %s, remote: %s, err: %v", id, path, c.ClientIP(), err)
			respondError(c, err)
			return
		}
		logger.Warnf("[REST] ini written: %s %s (%d bytes), remote: %s", id, path, len(body.Content), c.ClientIP())
		c.JSON(http.StatusOK, file)
	}
}

// PUT /api/processes/:id/watch {"autoRestart": true, "matchName": "java", "matchArgs": "...", "matchRegex": ""} : 모니터링 화면에서 실행 판단 조건 · 자동 재시작 변경
// 응답은 GET /api/config 와 같은 형식의 전체 설정이다.
func handleUpdateWatch(store *processes.Store, monitor *processes.Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		var update processes.WatchUpdate
		if err := c.ShouldBindJSON(&update); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "본문 형식이 올바르지 않습니다: " + err.Error()})
			return
		}
		id := c.Param("id")
		config, err := store.UpdateWatch(id, update)
		if err != nil {
			respondError(c, err)
			return
		}
		logger.Warnf("[REST] watch updated: %s %+v, remote: %s", id, update, c.ClientIP())
		c.JSON(http.StatusOK, processes.Public(config))
	}
}

// GET /api/processes/:id/watch-test?name=java&args=app.jar | ?regex=... : 실행 판단 조건에 지금 맞는 프로세스 ( 저장 전 확인용 )
func handleWatchTest(monitor *processes.Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		instances, err := monitor.WatchTest(c.Request.Context(), c.Param("id"), c.Query("name"), c.Query("args"), c.Query("regex"))
		if err != nil {
			respondError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"instances": instances})
	}
}

// POST /api/processes/:id/start|stop|restart : 프로세스 제어 ( 누가 했는지 로그에 남긴다 )
func handleControl(monitor *processes.Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, action := c.Param("id"), c.Param("action")
		logger.Warnf("[REST] process %s requested: %s, remote: %s", action, id, c.ClientIP())

		message, err := monitor.Control(c.Request.Context(), id, action)
		if err != nil {
			logger.Warnf("[REST] process %s failed: %s, err: %v", action, id, err)
			respondError(c, err)
			return
		}
		logger.Infof("[REST] process %s done: %s (%s)", action, id, message)
		c.JSON(http.StatusOK, gin.H{"message": message})
	}
}

// GET /api/servers/:id/browse?path=/home/ops/app : 서버의 폴더 목록 ( path 가 없으면 SSH 계정의 홈, 홈 밖은 거부 )
func handleBrowse(monitor *processes.Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		dir, err := monitor.BrowseRemote(c.Request.Context(), c.Param("id"), c.Query("path"))
		if err != nil {
			respondError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, dir)
	}
}

// POST /api/servers/:id/update?path=/home/ops/app&kind=dir|file ( 본문: tar ) : 서버의 폴더 · 파일을 올린 내용으로 바꾼다.
// 기존 대상은 '<이름>.bak.<YYYYMMDD-HHmmss>' 로 남긴다. ( 누가 했는지 로그에 남긴다 )
func handleUpdate(monitor *processes.Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, target, kind := c.Param("id"), c.Query("path"), c.Query("kind")
		logger.Warnf("[REST] program update requested: %s %s (%s, %d bytes), remote: %s", id, target, kind, c.Request.ContentLength, c.ClientIP())

		result, err := monitor.UpdateRemote(c.Request.Context(), id, target, kind, c.Request.Body, c.Request.ContentLength)
		if err != nil {
			logger.Warnf("[REST] program update failed: %s %s, err: %v", id, target, err)
			// 다 받기 전에 실패해도 응답이 브라우저에 닿도록 남은 본문을 마저 읽는다.
			io.Copy(io.Discard, c.Request.Body)
			respondError(c, err)
			return
		}
		logger.Warnf("[REST] program updated: %s %s -> backup %s (%d files, %d bytes)", id, result.Path, result.Backup, result.Files, result.Bytes)
		c.JSON(http.StatusOK, result)
	}
}

// GET /api/servers/:id/ps : 서버에서 지금 실행 중인 프로세스 ( 조건 창에서 골라 실행 판단 조건을 채운다 )
func handleRunningProcesses(monitor *processes.Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := monitor.RunningProcesses(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"processes": rows})
	}
}

// POST /api/servers/:id/install?path=/home/ops/app&start=bin/start.sh&label=앱 ( 본문: tar ) : 새 폴더에 프로그램을 설치하고 프로세스로 등록한다.
// 대상이 이미 있으면 설치하지 않는다. *.sh 와 시작 파일에 실행 권한을 준다. ( 누가 했는지 로그에 남긴다 )
func handleInstall(monitor *processes.Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		req := processes.InstallRequest{Target: c.Query("path"), StartFile: c.Query("start"), Label: c.Query("label")}
		logger.Warnf("[REST] program install requested: %s %s (start %s, %d bytes), remote: %s", id, req.Target, req.StartFile, c.Request.ContentLength, c.ClientIP())

		result, err := monitor.InstallRemote(c.Request.Context(), id, req, c.Request.Body, c.Request.ContentLength)
		if err != nil {
			logger.Warnf("[REST] program install failed: %s %s, err: %v", id, req.Target, err)
			// 다 받기 전에 실패해도 응답이 브라우저에 닿도록 남은 본문을 마저 읽는다.
			io.Copy(io.Discard, c.Request.Body)
			respondError(c, err)
			return
		}
		logger.Warnf("[REST] program installed: %s %s -> process %s (%d files, %d bytes)", id, result.Path, result.ProcessID, result.Files, result.Bytes)
		c.JSON(http.StatusOK, result)
	}
}

package request

// InstallUpload 는 받아 둔 압축 파일로 설치하는 요청이다.
type InstallUpload struct {
	// 받아 둔 압축 파일 id ( POST /api/uploads 응답 )
	Upload string `json:"upload" binding:"required"`
	// 설치할 폴더 ( 절대 경로, 아직 없어야 한다 )
	Path string `json:"path" binding:"required"`
	// 설치 폴더 안의 시작 파일 ( 상대 경로, 선택 )
	Start string `json:"start"`
	// 프로세스 표시 이름 ( 선택 )
	Label string `json:"label"`
	// 압축 파일 안의 기준 폴더 ( 맨 위 폴더를 벗긴 경로, 예: app. 비우면 압축 파일 전체 )
	Sub string `json:"sub"`
}

// UpdateUpload 는 받아 둔 압축 파일로 서버의 폴더를 바꾸는 요청이다.
type UpdateUpload struct {
	Upload string `json:"upload" binding:"required"`
	// 바꿀 폴더 ( 절대 경로 )
	Path string `json:"path" binding:"required"`
	// 압축 파일 안의 기준 폴더 ( 맨 위 폴더를 벗긴 경로, 예: app. 비우면 압축 파일 전체 )
	Sub string `json:"sub"`
}

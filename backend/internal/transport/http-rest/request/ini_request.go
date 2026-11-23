package request

// WriteINI 는 PUT /api/processes/:id/ini 의 본문이다.
// BaseHash 는 읽을 때 받은 hash 로, 그 사이 파일이 바뀌었으면 저장을 거부한다.
type WriteINI struct {
	Content  string `json:"content"`
	BaseHash string `json:"baseHash"`
}

package request

// Login 은 POST /api/login 본문이다.
type Login struct {
	User     string `json:"user" binding:"required"`
	Password string `json:"password" binding:"required"`
}

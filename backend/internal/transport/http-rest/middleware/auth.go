package middleware

import (
	"net/http"
	"strings"

	auth "pjt/internal/service/auth-service"

	"github.com/gin-gonic/gin"
)

// TokenKey 는 확인한 토큰을 gin.Context 에 넣어 두는 키다. (로그아웃에서 쓴다)
const TokenKey = "authToken"

// RequireAuth 는 Authorization: Bearer <토큰> 이 유효한 요청만 통과시킨다.
// 토큰은 브라우저 탭마다 따로(sessionStorage) 두므로 새 탭 · 새 브라우저는 다시 로그인해야 한다.
func RequireAuth(a *auth.Auth) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || !a.Check(token) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "로그인이 필요합니다"})
			return
		}
		c.Set(TokenKey, token)
		c.Next()
	}
}

// RequireTicket 은 ?ticket=<1회용 표> 가 유효한 요청만 통과시킨다. 헤더를 실을 수 없는 내려받기 링크용이다.
func RequireTicket(a *auth.Auth) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !a.UseTicket(c.Query("ticket")) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "로그인이 필요합니다. 화면을 새로고침한 뒤 다시 내려받으세요"})
			return
		}
		c.Next()
	}
}

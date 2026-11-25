package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	auth "pjt/internal/service/auth-service"

	"github.com/gin-gonic/gin"
)

func TestRequireAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sum := sha256.Sum256([]byte("pw"))
	a := auth.New("admin", hex.EncodeToString(sum[:]), time.Hour)
	token, err := a.Login("admin", "pw", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	ok := func(c *gin.Context) { c.String(http.StatusOK, c.GetString(TokenKey)) }
	router.GET("/a", RequireAuth(a), ok)
	router.GET("/d", RequireTicket(a), ok)

	get := func(url, header string) int {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	if get("/a", "") != http.StatusUnauthorized || get("/a", "Bearer x") != http.StatusUnauthorized || get("/a", token) != http.StatusUnauthorized {
		t.Error("missing or bad token must be rejected")
	}
	if get("/a", "Bearer "+token) != http.StatusOK {
		t.Error("valid token rejected")
	}
	ticket := a.Ticket()
	if get("/d?ticket="+ticket, "") != http.StatusOK || get("/d?ticket="+ticket, "") != http.StatusUnauthorized || get("/d", "Bearer "+token) != http.StatusUnauthorized {
		t.Error("ticket must be single use and required")
	}
}

package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/chy/chat2db/server/internal/auth"
	"github.com/chy/chat2db/server/internal/db"
	"github.com/chy/chat2db/server/internal/model"
	"github.com/gin-gonic/gin"
)

const (
	CtxUserID = "userID"
	CtxUser   = "user"
)

// AuthRequired validates the JWT from Authorization: Bearer <token>
// and loads the current user into the context.
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if h == "" {
			abortAuth(c, "missing Authorization header")
			return
		}
		parts := strings.SplitN(h, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			abortAuth(c, "invalid Authorization header")
			return
		}
		claims, err := auth.ParseToken(parts[1])
		if err != nil {
			abortAuth(c, "invalid token")
			return
		}
		var u model.User
		if err := db.Meta().First(&u, claims.UserID).Error; err != nil {
			abortAuth(c, "user not found")
			return
		}
		c.Set(CtxUserID, u.ID)
		c.Set(CtxUser, &u)
		c.Next()
	}
}

// abortAuth 统一返回 401 + request_id，避免在四个分支重复样板代码。
func abortAuth(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error":      msg,
		"request_id": GetRequestID(c),
	})
}

// CurrentUserID returns the current user's ID or 0.
func CurrentUserID(c *gin.Context) uint {
	v, ok := c.Get(CtxUserID)
	if !ok {
		return 0
	}
	id, _ := v.(uint)
	return id
}

// CurrentUser returns the current *model.User or error if none.
func CurrentUser(c *gin.Context) (*model.User, error) {
	v, ok := c.Get(CtxUser)
	if !ok {
		return nil, errors.New("no user in context")
	}
	u, ok := v.(*model.User)
	if !ok {
		return nil, errors.New("invalid user in context")
	}
	return u, nil
}

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
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing Authorization header"})
			return
		}
		parts := strings.SplitN(h, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid Authorization header"})
			return
		}
		claims, err := auth.ParseToken(parts[1])
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		var u model.User
		if err := db.Meta().First(&u, claims.UserID).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "user not found"})
			return
		}
		c.Set(CtxUserID, u.ID)
		c.Set(CtxUser, &u)
		c.Next()
	}
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

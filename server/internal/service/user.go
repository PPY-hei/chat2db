package service

import (
	"errors"
	"strings"

	"github.com/chy/chat2db/server/internal/config"
	cryptopkg "github.com/chy/chat2db/server/internal/crypto"
	"github.com/chy/chat2db/server/internal/db"
	"github.com/chy/chat2db/server/internal/model"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// Register creates a new user.
func Register(email, name, password string) (*model.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" || name == "" {
		return nil, errors.New("email, name and password are required")
	}
	if len(password) < 6 {
		return nil, errors.New("password must be at least 6 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	u := &model.User{Email: email, Name: name, PasswordHash: string(hash)}
	if err := db.Meta().Create(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

// Login verifies the credentials and returns the user.
func Login(email, password string) (*model.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var u model.User
	if err := db.Meta().Where("email = ?", email).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("invalid email or password")
		}
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, errors.New("invalid email or password")
	}
	return &u, nil
}

// UpdateLLM updates a user's LLM endpoint/model/api key. Empty apiKey keeps the previous one.
func UpdateLLM(userID uint, endpoint, modelName, apiKey string) error {
	updates := map[string]any{
		"llm_endpoint": endpoint,
		"llm_model":    modelName,
	}
	if apiKey != "" {
		enc, err := cryptopkg.EncryptString(apiKey, config.Get().CredentialKey)
		if err != nil {
			return err
		}
		updates["llm_api_key_enc"] = enc
	}
	return db.Meta().Model(&model.User{}).Where("id = ?", userID).Updates(updates).Error
}

// GetLLMAPIKey decrypts the API key for the given user.
func GetLLMAPIKey(u *model.User) (string, error) {
	if u.LLMAPIKeyEnc == "" {
		return "", nil
	}
	return cryptopkg.DecryptString(u.LLMAPIKeyEnc, config.Get().CredentialKey)
}

// FindSharedLLMOwner 查找用户可用的"共享 LLM 配置来源"。
// 规则：遍历用户所在的所有组，找一个 share_llm=true 且 Owner 的 LLM 配置完整的组 Owner。
// 若找不到则返回 nil, nil。
func FindSharedLLMOwner(userID uint) (*model.User, error) {
	var owner model.User
	// 通过 JOIN groups + group_members 找出"用户所在组"并且"组开启了 share_llm"
	// 的 Owner，再要求 Owner 的 LLM 配置齐全。
	err := db.Meta().
		Table("users AS u").
		Select("u.*").
		Joins("JOIN groups g ON g.owner_id = u.id").
		Joins("JOIN group_members gm ON gm.group_id = g.id").
		Where("gm.user_id = ? AND g.share_llm = ? AND u.llm_endpoint <> '' AND u.llm_model <> '' AND u.llm_api_key_enc <> ''", userID, true).
		Order("g.id").
		Limit(1).
		First(&owner).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &owner, nil
}

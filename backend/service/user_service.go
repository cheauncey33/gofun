package service

import (
	"gofun/container"
	"gofun/models"
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type TokenPair struct {
	AccessToken            string `json:"access_token"`
	RefreshToken           string `json:"refresh_token"`
	Token                  string `json:"token"`
	AccessTokenExpireSecs  int    `json:"access_token_expire_secs"`
	RefreshTokenExpireSecs int    `json:"refresh_token_expire_secs"`
}

func generateToken(secret []byte, userID int64, expireSecs int, tokenType string) (string, error) {
	claims := struct {
		UserID    int64  `json:"user_id"`
		TokenType string `json:"token_type"`
		jwt.RegisteredClaims
	}{
		UserID:    userID,
		TokenType: tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(expireSecs) * time.Second)),
			Issuer:    "zyh",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

type UserService struct {
	db                *gorm.DB
	rdb               *redis.Client
	jwtSecret         []byte
	jwtExpire         int
	refreshExpireSecs int
}

func NewUserService(c *container.Container, jwtExpireSecs, refreshExpireSecs int) *UserService {
	return &UserService{
		db:                c.DB,
		rdb:               c.RDB,
		jwtSecret:         c.JWTSecret,
		jwtExpire:         jwtExpireSecs,
		refreshExpireSecs: refreshExpireSecs,
	}
}

func (s *UserService) Register(username, password string, dormID int64) error {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	user := models.User{
		Username:     username,
		Password:     string(hashedPassword),
		Balance:      100.0,
		BalanceCents: 100000,
		DormID:       dormID,
	}
	return s.db.Create(&user).Error
}

func (s *UserService) Login(username, password string) (*TokenPair, error) {
	var user models.User
	if err := s.db.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, fmt.Errorf("用户不存在")
	}
	err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password))
	if err != nil {
		return nil, fmt.Errorf("密码错误")
	}

	now := time.Now()
	s.db.Model(&user).Update("last_login_at", &now)

	return s.issueTokenPair(user.ID)
}

func (s *UserService) Refresh(refreshToken string) (*TokenPair, error) {
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh token不能为空")
	}
	claims, err := parseServiceToken(s.jwtSecret, refreshToken)
	if err != nil || claims.TokenType != "refresh" {
		return nil, fmt.Errorf("refresh token无效或已过期")
	}

	ctx := context.Background()
	userID, err := s.rdb.Get(ctx, refreshTokenKey(refreshToken)).Int64()
	if err != nil {
		return nil, fmt.Errorf("refresh token无效或已过期")
	}
	if userID != claims.UserID {
		return nil, fmt.Errorf("refresh token无效或已过期")
	}

	s.rdb.Del(ctx, refreshTokenKey(refreshToken))
	return s.issueTokenPair(userID)
}

func (s *UserService) Logout(refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	ctx := context.Background()
	userID, _ := s.rdb.Get(ctx, refreshTokenKey(refreshToken)).Int64()
	s.rdb.Del(ctx, refreshTokenKey(refreshToken))
	if userID > 0 {
		storedToken, _ := s.rdb.Get(ctx, userRefreshTokenKey(userID)).Result()
		if storedToken == refreshToken {
			s.rdb.Del(ctx, userRefreshTokenKey(userID))
		}
	}
	return nil
}

func (s *UserService) issueTokenPair(userID int64) (*TokenPair, error) {
	accessToken, err := generateToken(s.jwtSecret, userID, s.jwtExpire, "access")
	if err != nil {
		return nil, err
	}

	refreshToken, err := generateToken(s.jwtSecret, userID, s.refreshExpireSecs, "refresh")
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if oldToken, err := s.rdb.Get(ctx, userRefreshTokenKey(userID)).Result(); err == nil && oldToken != "" {
		s.rdb.Del(ctx, refreshTokenKey(oldToken))
	}
	refreshTTL := time.Duration(s.refreshExpireSecs) * time.Second
	if err := s.rdb.Set(ctx, refreshTokenKey(refreshToken), userID, refreshTTL).Err(); err != nil {
		return nil, fmt.Errorf("保存refresh token失败")
	}
	if err := s.rdb.Set(ctx, userRefreshTokenKey(userID), refreshToken, refreshTTL).Err(); err != nil {
		return nil, fmt.Errorf("保存refresh token失败")
	}

	return &TokenPair{
		AccessToken:            accessToken,
		RefreshToken:           refreshToken,
		Token:                  accessToken,
		AccessTokenExpireSecs:  s.jwtExpire,
		RefreshTokenExpireSecs: s.refreshExpireSecs,
	}, nil
}

func refreshTokenKey(refreshToken string) string {
	return "auth:refresh_token:" + refreshToken
}

func userRefreshTokenKey(userID int64) string {
	return fmt.Sprintf("auth:user_refresh_token:%d", userID)
}

func parseServiceToken(secret []byte, tokenString string) (*struct {
	UserID    int64  `json:"user_id"`
	TokenType string `json:"token_type"`
	jwt.RegisteredClaims
}, error) {
	claims := &struct {
		UserID    int64  `json:"user_id"`
		TokenType string `json:"token_type"`
		jwt.RegisteredClaims
	}{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, jwt.ErrSignatureInvalid
	}
	return claims, nil
}

func (s *UserService) GetUserInfo(userID int64) (models.User, error) {
	var user models.User
	err := s.db.Select("id", "username", "balance", "balance_cents", "dorm_id", "phone", "avatar_url", "role", "last_login_at", "create_time").
		Where("id = ?", userID).First(&user).Error
	return user, err
}

type UpdateUserInfoReq struct {
	Phone     string `json:"phone" binding:"omitempty,phone"`
	AvatarURL string `json:"avatar_url"`
}

func (s *UserService) UpdateUserInfo(userID int64, req UpdateUserInfoReq) error {
	updates := map[string]interface{}{}
	if req.Phone != "" {
		var exist models.User
		if err := s.db.Where("phone = ? AND id != ?", req.Phone, userID).First(&exist).Error; err == nil {
			return fmt.Errorf("手机号已被使用")
		}
		updates["phone"] = req.Phone
	}
	if req.AvatarURL != "" {
		updates["avatar_url"] = req.AvatarURL
	}
	if len(updates) == 0 {
		return nil
	}
	return s.db.Model(&models.User{}).Where("id = ?", userID).Updates(updates).Error
}

type ChangePasswordReq struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,password"`
}

func (s *UserService) ChangePassword(userID int64, req ChangePasswordReq) error {
	var user models.User
	if err := s.db.Where("id = ?", userID).First(&user).Error; err != nil {
		return fmt.Errorf("用户不存在")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.OldPassword)); err != nil {
		return fmt.Errorf("原密码错误")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 12)
	if err != nil {
		return err
	}

	if err := s.db.Model(&user).Update("password", string(hashedPassword)).Error; err != nil {
		return err
	}

	ctx := context.Background()
	if oldToken, err := s.rdb.Get(ctx, userRefreshTokenKey(userID)).Result(); err == nil && oldToken != "" {
		s.rdb.Del(ctx, refreshTokenKey(oldToken))
	}
	s.rdb.Del(ctx, userRefreshTokenKey(userID))
	return nil
}

package traderservices

import (
	"fmt"
	"fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/storage"
	tokenservice "fx-app-api/internal/utils/services"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type AuthService struct {
	traderRepo storage.TraderStorage
}

type AuthResult struct {
	Token  string
	Trader *entity.Trader
}

func NewAuthService(traderRepo storage.TraderStorage) *AuthService {
	return &AuthService{traderRepo: traderRepo}
}

func (s *AuthService) AuthenticateTrader(email, password string) (*AuthResult, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, fmt.Errorf("email is required")
	}
	if strings.TrimSpace(password) == "" {
		return nil, fmt.Errorf("password is required")
	}

	trader, err := s.traderRepo.GetTraderByEmail(email)
	if err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	if !trader.IsActive {
		return nil, fmt.Errorf("trader account is inactive")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(trader.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	token, err := tokenservice.CreateToken(displayName(trader), trader.Email, []string{trader.Roles}, []string{trader.Status})
	if err != nil {
		return nil, fmt.Errorf("failed to create token: %w", err)
	}

	return &AuthResult{Token: token, Trader: trader}, nil
}

func HashPassword(password string) (string, error) {
	if strings.TrimSpace(password) == "" {
		return "", fmt.Errorf("password is required")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}

	return string(hash), nil
}

func displayName(trader *entity.Trader) string {
	switch {
	case strings.TrimSpace(trader.Name) != "":
		return trader.Name
	case strings.TrimSpace(trader.FirstName+" "+trader.LastName) != "":
		return strings.TrimSpace(trader.FirstName + " " + trader.LastName)
	default:
		return trader.Email
	}
}

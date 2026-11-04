package traderservices

import (
	"errors"
	"fmt"
	"fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/storage"
	tokenservice "fx-app-api/internal/utils/services"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// ErrAccountPending : inscription pas encore acceptée par un superviseur du bureau.
var ErrAccountPending = errors.New("account pending approval")

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
	if err := bcrypt.CompareHashAndPassword([]byte(trader.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	if !trader.IsActive {
		// On ne le dit qu'au titulaire du mot de passe : un inconnu reçoit le message habituel.
		if strings.EqualFold(trader.Status, "PENDING") {
			return nil, ErrAccountPending
		}
		return nil, fmt.Errorf("invalid credentials")
	}

	token, err := s.IssueToken(trader)
	if err != nil {
		return nil, err
	}

	return &AuthResult{Token: token, Trader: trader}, nil
}

// IssueToken délivre un jeton de session lié au mot de passe actuel du compte.
func (s *AuthService) IssueToken(trader *entity.Trader) (string, error) {
	token, err := tokenservice.CreateSessionToken(displayName(trader), trader.Email, []string{trader.Roles}, []string{trader.Status}, trader.PasswordHash)
	if err != nil {
		return "", fmt.Errorf("failed to create token: %w", err)
	}
	return token, nil
}

func HashPassword(password string) (string, error) {
	if strings.TrimSpace(password) == "" {
		return "", fmt.Errorf("password is required")
	}
	if len([]byte(password)) < 12 {
		return "", fmt.Errorf("password must be at least 12 bytes")
	}
	if len([]byte(password)) > 72 {
		return "", fmt.Errorf("password must not exceed 72 bytes")
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

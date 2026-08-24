package services

import (
	"crypto/rsa"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultPrivateKeyPath = "jwt/private.pem"
	defaultPublicKeyPath  = "jwt/public.pem"
)

// Claims defines the structure of the data (claims) contained in the JWT.
type Claims struct {
	Name   string   `json:"name"`
	Email  string   `json:"email"`
	Roles  []string `json:"roles"`
	Status []string `json:"status"`
	jwt.RegisteredClaims
}

func loadKeys() (*rsa.PrivateKey, *rsa.PublicKey, error) {
	privBytes, err := os.ReadFile(keyPath("JWT_PRIVATE_KEY_PATH", defaultPrivateKeyPath))
	if err != nil {
		return nil, nil, err
	}
	privKey, err := jwt.ParseRSAPrivateKeyFromPEM(privBytes)
	if err != nil {
		return nil, nil, err
	}

	pubBytes, err := os.ReadFile(keyPath("JWT_PUBLIC_KEY_PATH", defaultPublicKeyPath))
	if err != nil {
		return nil, nil, err
	}
	pubKey, err := jwt.ParseRSAPublicKeyFromPEM(pubBytes)
	if err != nil {
		return nil, nil, err
	}

	return privKey, pubKey, nil
}

func CreateToken(name string, email string, roles []string, status []string) (string, error) {
	privKey, _, err := loadKeys()
	if err != nil {
		return "", err
	}

	claims := &Claims{
		Name:   name,
		Email:  email,
		Roles:  roles,
		Status: status,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour * 24)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(privKey)
	if err != nil {
		return "", err
	}

	return tokenString, nil
}

func VerifyToken(tokenString string) error {
	_, pubKey, err := loadKeys()
	if err != nil {
		return err
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodRS256 {
			return nil, fmt.Errorf("unexpected signing method: %s", token.Header["alg"])
		}

		return pubKey, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}))

	if err != nil {
		return err
	}

	if !token.Valid {
		return fmt.Errorf("invalid token")
	}

	return nil
}

func ParseToken(tokenString string) (*Claims, error) {
	_, pubKey, err := loadKeys()
	if err != nil {
		return nil, err
	}

	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodRS256 {
			return nil, fmt.Errorf("unexpected signing method: %s", token.Header["alg"])
		}
		return pubKey, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}))

	if err != nil {
		return nil, err
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	return claims, nil
}

func keyPath(envName, defaultPath string) string {
	if path := os.Getenv(envName); path != "" {
		return path
	}

	return filepath.Clean(defaultPath)
}

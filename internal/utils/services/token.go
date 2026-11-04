package services

import (
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultPrivateKeyPath = "jwt/private.pem"
	defaultPublicKeyPath  = "jwt/public.pem"
	envPrivateKey         = "JWT_PRIVATE_KEY"
	envPublicKey          = "JWT_PUBLIC_KEY"
)

// Claims defines the structure of the data in the JWT.
type Claims struct {
	Name   string   `json:"name"`
	Email  string   `json:"email"`
	Roles  []string `json:"roles"`
	Status []string `json:"status"`
	// Empreinte du mot de passe au moment de la connexion : un changement de mot de passe
	// invalide les sessions ouvertes avant.
	PasswordFingerprint string `json:"pwd,omitempty"`
	jwt.RegisteredClaims
}

// PasswordFingerprint : empreinte courte du hachage du mot de passe (jamais le mot de passe).
func PasswordFingerprint(passwordHash string) string {
	sum := sha256.Sum256([]byte("fx-session:" + passwordHash))
	return hex.EncodeToString(sum[:8])
}

// loadKeys loads RSA keys from multiple sources in order of priority:
// 1. Environment variables (JWT_PRIVATE_KEY, JWT_PUBLIC_KEY)
// 2. Files specified by environment variables (JWT_PRIVATE_KEY_PATH, JWT_PUBLIC_KEY_PATH)
// 3. Default file locations
func loadKeys() (*rsa.PrivateKey, *rsa.PublicKey, error) {
	// Try loading from environment variables first (base64 encoded)
	if privKey, pubKey, err := loadKeysFromEnv(); err == nil {
		return privKey, pubKey, nil
	}

	// Try loading from file paths specified in environment variables
	if privKey, pubKey, err := loadKeysFromFilePaths(); err == nil {
		return privKey, pubKey, nil
	}

	// Finally, try default file locations
	return loadKeysFromDefaultFiles()
}

func loadKeysFromEnv() (*rsa.PrivateKey, *rsa.PublicKey, error) {
	// Check if keys are provided as base64 encoded strings in environment variables
	privKeyB64 := strings.TrimSpace(os.Getenv(envPrivateKey))
	pubKeyB64 := strings.TrimSpace(os.Getenv(envPublicKey))

	if privKeyB64 == "" || pubKeyB64 == "" {
		return nil, nil, fmt.Errorf("JWT keys not found in environment variables")
	}

	// Decode base64 strings
	privBytes, err := base64.StdEncoding.DecodeString(privKeyB64)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode private key from base64: %w", err)
	}

	pubBytes, err := base64.StdEncoding.DecodeString(pubKeyB64)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode public key from base64: %w", err)
	}

	// Parse RSA keys
	privKey, err := jwt.ParseRSAPrivateKeyFromPEM(privBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse private key from PEM: %w", err)
	}

	pubKey, err := jwt.ParseRSAPublicKeyFromPEM(pubBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse public key from PEM: %w", err)
	}

	return privKey, pubKey, nil
}

func loadKeysFromFilePaths() (*rsa.PrivateKey, *rsa.PublicKey, error) {
	privPath := keyPath("JWT_PRIVATE_KEY_PATH", "")
	pubPath := keyPath("JWT_PUBLIC_KEY_PATH", "")

	if privPath == "" || pubPath == "" {
		return nil, nil, fmt.Errorf("JWT key paths not specified in environment")
	}

	privBytes, err := os.ReadFile(privPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read private key file %s: %w", privPath, err)
	}
	privKey, err := jwt.ParseRSAPrivateKeyFromPEM(privBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse private key from file %s: %w", privPath, err)
	}

	pubBytes, err := os.ReadFile(pubPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read public key file %s: %w", pubPath, err)
	}
	pubKey, err := jwt.ParseRSAPublicKeyFromPEM(pubBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse public key from file %s: %w", pubPath, err)
	}

	return privKey, pubKey, nil
}

func loadKeysFromDefaultFiles() (*rsa.PrivateKey, *rsa.PublicKey, error) {
	privBytes, err := os.ReadFile(defaultPrivateKeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read default private key file %s: %w", defaultPrivateKeyPath, err)
	}
	privKey, err := jwt.ParseRSAPrivateKeyFromPEM(privBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse private key from default file %s: %w", defaultPrivateKeyPath, err)
	}

	pubBytes, err := os.ReadFile(defaultPublicKeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read default public key file %s: %w", defaultPublicKeyPath, err)
	}
	pubKey, err := jwt.ParseRSAPublicKeyFromPEM(pubBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse public key from default file %s: %w", defaultPublicKeyPath, err)
	}

	return privKey, pubKey, nil
}

var (
	keyManager     *KeyManager
	keyManagerOnce sync.Once
	keyManagerErr  error
)

// getKeyManager retourne le gestionnaire de clés singleton
func getKeyManager() (*KeyManager, error) {
	keyManagerOnce.Do(func() {
		keyManager, keyManagerErr = NewKeyManager()
	})
	return keyManager, keyManagerErr
}

func CreateToken(name string, email string, roles []string, status []string) (string, error) {
	return CreateSessionToken(name, email, roles, status, "")
}

// CreateSessionToken : comme CreateToken, mais lié au mot de passe actuel du compte.
func CreateSessionToken(name string, email string, roles []string, status []string, passwordHash string) (string, error) {
	km, err := getKeyManager()
	if err != nil {
		return "", fmt.Errorf("failed to get key manager: %w", err)
	}

	claims := &Claims{
		Name:   name,
		Email:  email,
		Roles:  roles,
		Status: status,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour * 24)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	if passwordHash != "" {
		claims.PasswordFingerprint = PasswordFingerprint(passwordHash)
	}

	return km.CreateTokenWithRotation(claims)
}

func VerifyToken(tokenString string) error {
	km, err := getKeyManager()
	if err != nil {
		return fmt.Errorf("failed to get key manager: %w", err)
	}

	return km.VerifyTokenWithRotation(tokenString)
}

func ParseToken(tokenString string) (*Claims, error) {
	km, err := getKeyManager()
	if err != nil {
		return nil, fmt.Errorf("failed to get key manager: %w", err)
	}

	km.mu.RLock()
	currentPublicKey := km.currentPublicKey
	previousPublicKey := km.previousPublicKey
	km.mu.RUnlock()

	claims := &Claims{}
	var token *jwt.Token

	// Essayer d'abord avec la clé actuelle
	token, err = jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodRS256 {
			return nil, fmt.Errorf("unexpected signing method: %s", token.Header["alg"])
		}
		return currentPublicKey, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}))

	// Si échec avec la clé actuelle, essayer avec la clé précédente
	if (err != nil || !token.Valid) && previousPublicKey != nil {
		claims = &Claims{}
		token, err = jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			if token.Method != jwt.SigningMethodRS256 {
				return nil, fmt.Errorf("unexpected signing method: %s", token.Header["alg"])
			}
			return previousPublicKey, nil
		}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}))
	}

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
		return filepath.Clean(path)
	}

	if defaultPath == "" {
		return ""
	}

	return filepath.Clean(defaultPath)
}

// CheckJWTKeys verifies if JWT keys are properly configured and logs warnings
func CheckJWTKeys() error {
	_, _, err := loadKeys()
	if err != nil {
		return fmt.Errorf("JWT keys not properly configured: %w", err)
	}

	// Check if using default file locations (less secure)
	if os.Getenv(envPrivateKey) == "" && os.Getenv(envPublicKey) == "" {
		if os.Getenv("JWT_PRIVATE_KEY_PATH") == "" && os.Getenv("JWT_PUBLIC_KEY_PATH") == "" {
			fmt.Println("⚠️  WARNING: Using default JWT key file locations. For production, use environment variables or secrets manager.")
			fmt.Println("   Recommended: Set JWT_PRIVATE_KEY and JWT_PUBLIC_KEY environment variables (base64 encoded)")
		}
	}

	return nil
}

package services

import (
	"encoding/base64"
	"os"
	"testing"
	"time"
)

func TestLoadKeysFromEnv(t *testing.T) {
	// Sauvegarder les variables d'environnement existantes
	origPrivateKey := os.Getenv("JWT_PRIVATE_KEY")
	origPublicKey := os.Getenv("JWT_PUBLIC_KEY")
	defer func() {
		os.Setenv("JWT_PRIVATE_KEY", origPrivateKey)
		os.Setenv("JWT_PUBLIC_KEY", origPublicKey)
	}()

	// Créer une paire de clés de test
	privKeyPEM := `-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7VKDWlCgWvxTp
2bG6y8Qv6mK2hN3kY4QhU9f8Y0jL6vG5zQZ7Xj8HnMlVQq5K7a8wW8vN6yF5b7t
-----END PRIVATE KEY-----`

	pubKeyPEM := `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAu1Sg1pQoFr8U6dmxusvE
L+pitoTd5GOEIVPX/GNIy+rxuc0Ge14/B5zJVUKuSu2vMFvLzesheW+7
-----END PUBLIC KEY-----`

	// Encoder en base64
	privKeyB64 := base64.StdEncoding.EncodeToString([]byte(privKeyPEM))
	pubKeyB64 := base64.StdEncoding.EncodeToString([]byte(pubKeyPEM))

	// Définir les variables d'environnement
	os.Setenv("JWT_PRIVATE_KEY", privKeyB64)
	os.Setenv("JWT_PUBLIC_KEY", pubKeyB64)

	// Tester le chargement
	_, _, err := loadKeys()
	if err == nil {
		t.Error("Expected error for invalid keys, got nil")
	}
}

func TestCreateAndVerifyToken(t *testing.T) {
	// Ce test nécessite des clés JWT valides
	// Il est exécuté uniquement si les clés sont configurées
	if os.Getenv("JWT_PRIVATE_KEY") == "" && os.Getenv("JWT_PUBLIC_KEY") == "" {
		t.Skip("JWT keys not configured in environment")
	}

	// Créer un token
	token, err := CreateToken(
		"Test User",
		"test@example.com",
		[]string{"admin"},
		[]string{"active"},
	)

	if err != nil {
		t.Fatalf("Failed to create token: %v", err)
	}

	if token == "" {
		t.Error("Created token is empty")
	}

	// Vérifier le token
	err = VerifyToken(token)
	if err != nil {
		t.Errorf("Failed to verify token: %v", err)
	}

	// Parser le token
	claims, err := ParseToken(token)
	if err != nil {
		t.Errorf("Failed to parse token: %v", err)
	}

	if claims.Name != "Test User" {
		t.Errorf("Expected name 'Test User', got '%s'", claims.Name)
	}

	if claims.Email != "test@example.com" {
		t.Errorf("Expected email 'test@example.com', got '%s'", claims.Email)
	}
}

func TestCheckJWTKeys(t *testing.T) {
	// Tester la fonction de vérification
	err := CheckJWTKeys()

	// Sans clés configurées ni fichiers de développement, la configuration est invalide.
	if err == nil {
		t.Fatal("expected an error when JWT keys are unavailable")
	}
}

func TestTokenExpiration(t *testing.T) {
	// Ce test nécessite des clés JWT valides
	if os.Getenv("JWT_PRIVATE_KEY") == "" && os.Getenv("JWT_PUBLIC_KEY") == "" {
		t.Skip("JWT keys not configured in environment")
	}

	// Note: Le token est configuré pour expirer dans 24 heures dans CreateToken
	// Nous testons simplement que le token est créé avec une date d'expiration
	token, err := CreateToken(
		"Test User",
		"test@example.com",
		[]string{"user"},
		[]string{"active"},
	)

	if err != nil {
		t.Fatalf("Failed to create token: %v", err)
	}

	claims, err := ParseToken(token)
	if err != nil {
		t.Fatalf("Failed to parse token: %v", err)
	}

	if claims.ExpiresAt == nil {
		t.Error("Token should have expiration time")
	}

	// Vérifier que l'expiration est dans le futur
	if claims.ExpiresAt.Time.Before(time.Now()) {
		t.Error("Token expiration should be in the future")
	}
}

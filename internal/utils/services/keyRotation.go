package services

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// KeyManager gère la rotation des clés JWT
type KeyManager struct {
	mu sync.RWMutex

	// Clés actuelles
	currentPrivateKey *rsa.PrivateKey
	currentPublicKey  *rsa.PublicKey

	// Clés précédentes (pour la transition)
	previousPrivateKey *rsa.PrivateKey
	previousPublicKey  *rsa.PublicKey

	// Configuration
	rotationInterval time.Duration
	keyLifetime      time.Duration
	lastRotation     time.Time

	// Callbacks
	onRotation func() error
}

// KeyRotationConfig configuration pour la rotation des clés
type KeyRotationConfig struct {
	// RotationInterval intervalle entre les rotations (par défaut: 90 jours)
	RotationInterval time.Duration

	// KeyLifetime durée de vie maximale d'une clé (par défaut: 180 jours)
	KeyLifetime time.Duration

	// OnRotation callback appelé lors de la rotation
	OnRotation func() error
}

// DefaultKeyRotationConfig configuration par défaut
var DefaultKeyRotationConfig = KeyRotationConfig{
	RotationInterval: 90 * 24 * time.Hour,  // 90 jours
	KeyLifetime:      180 * 24 * time.Hour, // 180 jours
	OnRotation:       nil,
}

// NewKeyManager crée un nouveau gestionnaire de clés
func NewKeyManager(config ...KeyRotationConfig) (*KeyManager, error) {
	var cfg KeyRotationConfig
	if len(config) > 0 {
		cfg = config[0]
	} else {
		cfg = DefaultKeyRotationConfig
	}

	// S'assurer que les intervalles sont valides
	if cfg.RotationInterval <= 0 {
		cfg.RotationInterval = DefaultKeyRotationConfig.RotationInterval
	}
	if cfg.KeyLifetime <= 0 {
		cfg.KeyLifetime = DefaultKeyRotationConfig.KeyLifetime
	}
	if cfg.KeyLifetime < cfg.RotationInterval {
		return nil, fmt.Errorf("KeyLifetime must be greater than RotationInterval")
	}

	// Charger les clés initiales
	privateKey, publicKey, err := loadKeys()
	if err != nil {
		return nil, fmt.Errorf("failed to load initial keys: %w", err)
	}

	km := &KeyManager{
		currentPrivateKey: privateKey,
		currentPublicKey:  publicKey,
		rotationInterval:  cfg.RotationInterval,
		keyLifetime:       cfg.KeyLifetime,
		lastRotation:      time.Now(),
		onRotation:        cfg.OnRotation,
	}

	return km, nil
}

// GetCurrentPrivateKey retourne la clé privée actuelle
func (km *KeyManager) GetCurrentPrivateKey() *rsa.PrivateKey {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return km.currentPrivateKey
}

// GetCurrentPublicKey retourne la clé publique actuelle
func (km *KeyManager) GetCurrentPublicKey() *rsa.PublicKey {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return km.currentPublicKey
}

// GetPreviousPublicKey retourne la clé publique précédente
func (km *KeyManager) GetPreviousPublicKey() *rsa.PublicKey {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return km.previousPublicKey
}

// RotateKeys effectue une rotation des clés
func (km *KeyManager) RotateKeys() error {
	km.mu.Lock()
	defer km.mu.Unlock()

	// Sauvegarder les clés actuelles comme précédentes
	km.previousPrivateKey = km.currentPrivateKey
	km.previousPublicKey = km.currentPublicKey

	// Générer de nouvelles clés
	newPrivateKey, err := rsa.GenerateKey(nil, 4096)
	if err != nil {
		return fmt.Errorf("failed to generate new private key: %w", err)
	}

	km.currentPrivateKey = newPrivateKey
	km.currentPublicKey = &newPrivateKey.PublicKey
	km.lastRotation = time.Now()

	// Appeler le callback si défini
	if km.onRotation != nil {
		if err := km.onRotation(); err != nil {
			// Ne pas échouer la rotation si le callback échoue
			fmt.Printf("WARNING: onRotation callback failed: %v\n", err)
		}
	}

	return nil
}

// ShouldRotate vérifie si une rotation est nécessaire
func (km *KeyManager) ShouldRotate() bool {
	km.mu.RLock()
	defer km.mu.RUnlock()

	return time.Since(km.lastRotation) >= km.rotationInterval
}

// TimeSinceLastRotation retourne le temps écoulé depuis la dernière rotation
func (km *KeyManager) TimeSinceLastRotation() time.Duration {
	km.mu.RLock()
	defer km.mu.RUnlock()

	return time.Since(km.lastRotation)
}

// VerifyTokenWithRotation vérifie un token avec support de rotation
func (km *KeyManager) VerifyTokenWithRotation(tokenString string) error {
	km.mu.RLock()
	currentPublicKey := km.currentPublicKey
	previousPublicKey := km.previousPublicKey
	km.mu.RUnlock()

	// Essayer d'abord avec la clé actuelle
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodRS256 {
			return nil, fmt.Errorf("unexpected signing method: %s", token.Header["alg"])
		}
		return currentPublicKey, nil
	})

	if err == nil && token.Valid {
		return nil
	}

	// Si échec avec la clé actuelle, essayer avec la clé précédente
	if previousPublicKey != nil {
		token, err = jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if token.Method != jwt.SigningMethodRS256 {
				return nil, fmt.Errorf("unexpected signing method: %s", token.Header["alg"])
			}
			return previousPublicKey, nil
		})

		if err == nil && token.Valid {
			// Token signé avec l'ancienne clé - toujours valide pendant la période de transition
			return nil
		}
	}

	return fmt.Errorf("invalid token: %w", err)
}

// CreateTokenWithRotation crée un token avec la clé actuelle
func (km *KeyManager) CreateTokenWithRotation(claims *Claims) (string, error) {
	km.mu.RLock()
	privateKey := km.currentPrivateKey
	km.mu.RUnlock()

	if privateKey == nil {
		return "", fmt.Errorf("no private key available")
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(privateKey)
}

// StartAutoRotation démarre la rotation automatique des clés
func (km *KeyManager) StartAutoRotation() chan struct{} {
	stopChan := make(chan struct{})

	go func() {
		ticker := time.NewTicker(24 * time.Hour) // Vérifier chaque jour
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if km.ShouldRotate() {
					fmt.Println("🔑 Automatically rotating JWT keys...")
					if err := km.RotateKeys(); err != nil {
						fmt.Printf("ERROR: Failed to auto-rotate keys: %v\n", err)
					} else {
						fmt.Println("✅ JWT keys rotated successfully")
					}
				}
			case <-stopChan:
				return
			}
		}
	}()

	return stopChan
}

// ExportCurrentKeys exporte les clés actuelles (pour sauvegarde)
func (km *KeyManager) ExportCurrentKeys() (privateKeyPEM []byte, publicKeyPEM []byte, err error) {
	km.mu.RLock()
	defer km.mu.RUnlock()

	if km.currentPrivateKey == nil {
		return nil, nil, fmt.Errorf("no current private key")
	}

	// Exporter la clé privée au format PEM
	privateKeyPEM = ExportPrivateKeyToPEM(km.currentPrivateKey)

	// Exporter la clé publique au format PEM
	publicKeyPEM = ExportPublicKeyToPEM(km.currentPublicKey)

	return privateKeyPEM, publicKeyPEM, nil
}

// ImportKeys importe de nouvelles clés
func (km *KeyManager) ImportKeys(privateKeyPEM, publicKeyPEM []byte) error {
	km.mu.Lock()
	defer km.mu.Unlock()

	// Parser les clés
	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM(privateKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}

	publicKey, err := jwt.ParseRSAPublicKeyFromPEM(publicKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to parse public key: %w", err)
	}

	// Vérifier que les clés correspondent
	if !privateKey.PublicKey.Equal(publicKey) {
		return fmt.Errorf("private and public keys do not match")
	}

	// Sauvegarder les clés actuelles comme précédentes
	km.previousPrivateKey = km.currentPrivateKey
	km.previousPublicKey = km.currentPublicKey

	// Définir les nouvelles clés comme actuelles
	km.currentPrivateKey = privateKey
	km.currentPublicKey = publicKey
	km.lastRotation = time.Now()

	return nil
}

// Status retourne l'état actuel du gestionnaire de clés
func (km *KeyManager) Status() map[string]interface{} {
	km.mu.RLock()
	defer km.mu.RUnlock()

	status := map[string]interface{}{
		"last_rotation":       km.lastRotation.Format(time.RFC3339),
		"time_since_rotation": time.Since(km.lastRotation).String(),
		"rotation_interval":   km.rotationInterval.String(),
		"key_lifetime":        km.keyLifetime.String(),
		"has_previous_key":    km.previousPublicKey != nil,
		"should_rotate":       time.Since(km.lastRotation) >= km.rotationInterval,
		"rotation_due_in":     (km.rotationInterval - time.Since(km.lastRotation)).String(),
	}

	return status
}

// ExportPrivateKeyToPEM exporte une clé privée RSA au format PEM
func ExportPrivateKeyToPEM(privateKey *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
}

// ExportPublicKeyToPEM exporte une clé publique RSA au format PEM
func ExportPublicKeyToPEM(publicKey *rsa.PublicKey) []byte {
	encoded, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded})
}

// SaveKeysToFile sauvegarde les clés actuelles dans des fichiers
func (km *KeyManager) SaveKeysToFile(privateKeyPath, publicKeyPath string) error {
	privateKeyPEM, publicKeyPEM, err := km.ExportCurrentKeys()
	if err != nil {
		return err
	}

	// Sauvegarder la clé privée
	if err := os.WriteFile(privateKeyPath, privateKeyPEM, 0600); err != nil {
		return fmt.Errorf("failed to save private key: %w", err)
	}

	// Sauvegarder la clé publique
	if err := os.WriteFile(publicKeyPath, publicKeyPEM, 0644); err != nil {
		// Nettoyer la clé privée en cas d'erreur
		os.Remove(privateKeyPath)
		return fmt.Errorf("failed to save public key: %w", err)
	}

	return nil
}

// LoadKeysFromFile charge des clés depuis des fichiers
func (km *KeyManager) LoadKeysFromFile(privateKeyPath, publicKeyPath string) error {
	privateKeyPEM, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return fmt.Errorf("failed to read private key file: %w", err)
	}

	publicKeyPEM, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return fmt.Errorf("failed to read public key file: %w", err)
	}

	return km.ImportKeys(privateKeyPEM, publicKeyPEM)
}

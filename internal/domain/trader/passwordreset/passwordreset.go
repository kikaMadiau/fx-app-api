// Package passwordreset gère « Mot de passe oublié » : un lien à usage unique, valable
// 30 minutes, envoyé par e-mail. Seule l'empreinte SHA-256 du jeton est conservée en base.
package passwordreset

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"fx-app-api/internal/storage"
)

// Validity : durée de vie d'un lien de réinitialisation.
const Validity = 30 * time.Minute

// ErrInvalidToken : lien inconnu, expiré ou déjà utilisé.
var ErrInvalidToken = errors.New("reset link is invalid or has expired")

type Repository struct {
	store *storage.PostgresStore
}

func NewRepository(store *storage.PostgresStore) *Repository { return &Repository{store: store} }

func (r *Repository) Init() error {
	_, err := r.store.DB().Exec(`
		CREATE TABLE IF NOT EXISTS password_reset_tokens (
			id BIGSERIAL PRIMARY KEY,
			trader_id INT NOT NULL REFERENCES traders(id),
			token_hash CHAR(64) NOT NULL UNIQUE,
			expires_at TIMESTAMPTZ NOT NULL,
			used_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS password_reset_tokens_trader_idx ON password_reset_tokens(trader_id);`)
	if err != nil {
		return fmt.Errorf("init password reset tokens: %w", err)
	}
	return nil
}

// Create invalide les liens encore ouverts du compte et en crée un nouveau ; renvoie le jeton en clair.
func (r *Repository) Create(traderID int, now time.Time) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate reset token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	tx, err := r.store.DB().Begin()
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE password_reset_tokens SET used_at = $2 WHERE trader_id = $1 AND used_at IS NULL`, traderID, now); err != nil {
		return "", fmt.Errorf("revoke previous reset tokens: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO password_reset_tokens (trader_id, token_hash, expires_at, created_at) VALUES ($1, $2, $3, $4)`,
		traderID, hash(token), now.Add(Validity), now); err != nil {
		return "", fmt.Errorf("save reset token: %w", err)
	}
	return token, tx.Commit()
}

// Consume vérifie le jeton, enregistre le nouveau mot de passe (déjà haché) et ferme le lien,
// dans une seule transaction SQL. Renvoie l'identifiant du compte.
func (r *Repository) Consume(token, passwordHash string, now time.Time) (int, error) {
	tx, err := r.store.DB().Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	var traderID int
	err = tx.QueryRow(`
		SELECT p.id, p.trader_id FROM password_reset_tokens p
		JOIN traders t ON t.id = p.trader_id
		WHERE p.token_hash = $1 AND p.used_at IS NULL AND p.expires_at > $2
		  AND t.deleted_at IS NULL AND t.is_active
		FOR UPDATE OF p`, hash(token), now).Scan(&id, &traderID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrInvalidToken
	}
	if err != nil {
		return 0, fmt.Errorf("check reset token: %w", err)
	}
	if _, err := tx.Exec(`UPDATE traders SET password_hash = $1, must_change_password = FALSE, updated_at = $3 WHERE id = $2`, passwordHash, traderID, now); err != nil {
		return 0, fmt.Errorf("update password: %w", err)
	}
	if _, err := tx.Exec(`UPDATE password_reset_tokens SET used_at = $2 WHERE trader_id = $1 AND used_at IS NULL`, traderID, now); err != nil {
		return 0, fmt.Errorf("close reset token: %w", err)
	}
	return traderID, tx.Commit()
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

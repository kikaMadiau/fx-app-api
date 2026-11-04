// Package iddocument conserve les photos de la pièce d'identité des clients (recto et verso),
// prises au guichet lors d'une opération ou plus tard pour régulariser un dossier.
package iddocument

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"fx-app-api/internal/storage"
)

// Faces acceptées pour une pièce d'identité.
const (
	SideFront = "front"
	SideBack  = "back"
)

var ErrNoDocument = errors.New("no identity document photo")

func ValidSide(side string) bool { return side == SideFront || side == SideBack }

// Status indique quelles faces de la pièce ont été photographiées.
type Status struct {
	Front *Face `json:"front"`
	Back  *Face `json:"back"`
}

type Face struct {
	UploadedAt time.Time `json:"uploaded_at"`
	UploadedBy int       `json:"uploaded_by"`
	SizeBytes  int       `json:"size_bytes"`
}

type Repository struct{ store *storage.PostgresStore }

func NewRepository(store *storage.PostgresStore) *Repository { return &Repository{store: store} }

func (r *Repository) Init() error {
	_, err := r.store.DB().Exec(`
		CREATE TABLE IF NOT EXISTS customer_id_documents (
			customer_id INT NOT NULL REFERENCES customers(id),
			side VARCHAR(10) NOT NULL CHECK (side IN ('front', 'back')),
			image BYTEA NOT NULL,
			mime VARCHAR(30) NOT NULL,
			uploaded_by INT REFERENCES traders(id),
			uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (customer_id, side)
		);`)
	if err != nil {
		return fmt.Errorf("create customer_id_documents: %w", err)
	}
	return nil
}

// Save enregistre (ou remplace) une face de la pièce d'identité du client.
func (r *Repository) Save(customerID int, side string, data []byte, mime string, uploadedBy int) error {
	_, err := r.store.DB().Exec(`
		INSERT INTO customer_id_documents (customer_id, side, image, mime, uploaded_by, uploaded_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (customer_id, side) DO UPDATE
		SET image = EXCLUDED.image, mime = EXCLUDED.mime, uploaded_by = EXCLUDED.uploaded_by, uploaded_at = NOW()`,
		customerID, side, data, mime, uploadedBy)
	if err != nil {
		return fmt.Errorf("save identity document photo: %w", err)
	}
	return nil
}

func (r *Repository) Image(customerID int, side string) ([]byte, string, error) {
	var data []byte
	var mime string
	err := r.store.DB().QueryRow(`SELECT image, mime FROM customer_id_documents WHERE customer_id = $1 AND side = $2`, customerID, side).Scan(&data, &mime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNoDocument
	}
	if err != nil {
		return nil, "", fmt.Errorf("get identity document photo: %w", err)
	}
	return data, mime, nil
}

func (r *Repository) Status(customerID int) (*Status, error) {
	rows, err := r.store.DB().Query(`SELECT side, uploaded_at, COALESCE(uploaded_by, 0), octet_length(image) FROM customer_id_documents WHERE customer_id = $1`, customerID)
	if err != nil {
		return nil, fmt.Errorf("get identity document status: %w", err)
	}
	defer rows.Close()
	status := &Status{}
	for rows.Next() {
		var side string
		face := &Face{}
		if err := rows.Scan(&side, &face.UploadedAt, &face.UploadedBy, &face.SizeBytes); err != nil {
			return nil, err
		}
		if side == SideFront {
			status.Front = face
		} else {
			status.Back = face
		}
	}
	return status, rows.Err()
}

func (r *Repository) Delete(customerID int, side string) error {
	res, err := r.store.DB().Exec(`DELETE FROM customer_id_documents WHERE customer_id = $1 AND side = $2`, customerID, side)
	if err != nil {
		return fmt.Errorf("delete identity document photo: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoDocument
	}
	return nil
}

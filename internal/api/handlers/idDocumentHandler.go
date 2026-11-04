package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"fx-app-api/internal/domain/customer/iddocument"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	"fx-app-api/internal/storage"
)

// Une photo de pièce prise au téléphone est réduite côté interface (≈ 1600 px) ;
// la limite laisse de la marge pour une image envoyée telle quelle.
const maxIDDocumentBytes = 4 << 20 // 4 Mio

func (h *Handler) SetIDDocumentRepository(repository *iddocument.Repository) {
	h.idDocumentRepo = repository
}

// idDocumentTarget valide le client et la face (recto / verso) indiqués dans l'URL.
func (h *Handler) idDocumentTarget(r *http.Request, withSide bool) (int, string, error) {
	if h.idDocumentRepo == nil {
		return 0, "", &HTTPError{StatusCode: http.StatusInternalServerError, Message: "identity document repository is unavailable"}
	}
	customerID, err := pathID(r)
	if err != nil {
		return 0, "", badRequest("invalid customer id", err)
	}
	side := ""
	if withSide {
		side = r.PathValue("side")
		if !iddocument.ValidSide(side) {
			return 0, "", badRequest("side must be front or back", nil)
		}
	}
	if _, err := h.customerStore.GetCustomer(customerID); err != nil {
		if errors.Is(err, storage.ErrCustomerIDNotFound) {
			return 0, "", notFound("customer not found")
		}
		return 0, "", fmt.Errorf("failed to check customer: %w", err)
	}
	return customerID, side, nil
}

// HandleGetIDDocumentStatus : GET /customers/{id}/id-document — faces déjà photographiées.
func (h *Handler) HandleGetIDDocumentStatus(w http.ResponseWriter, r *http.Request) error {
	customerID, _, err := h.idDocumentTarget(r, false)
	if err != nil {
		return err
	}
	status, err := h.idDocumentRepo.Status(customerID)
	if err != nil {
		return err
	}
	return WriteJson(w, http.StatusOK, status)
}

// HandleUploadIDDocument : PUT /customers/{id}/id-document/{side}, multipart, champ « photo ».
func (h *Handler) HandleUploadIDDocument(w http.ResponseWriter, r *http.Request) error {
	customerID, side, err := h.idDocumentTarget(r, true)
	if err != nil {
		return err
	}
	user := traderservices.GetUser(r)
	if user == nil {
		return &HTTPError{StatusCode: http.StatusUnauthorized, Message: "unauthenticated"}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxIDDocumentBytes+64<<10)
	if err := r.ParseMultipartForm(maxIDDocumentBytes); err != nil {
		return badRequest("photo must be sent as multipart/form-data and weigh at most 4 MB", err)
	}
	file, _, err := r.FormFile("photo")
	if err != nil {
		return badRequest("photo file is required (field \"photo\")", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxIDDocumentBytes+1))
	if err != nil {
		return badRequest("could not read photo", err)
	}
	if len(data) == 0 || len(data) > maxIDDocumentBytes {
		return badRequest("photo must weigh at most 4 MB", nil)
	}
	mime := http.DetectContentType(data)
	if !allowedPhotoTypes[mime] {
		return badRequest("photo must be a JPEG, PNG or WebP image", nil)
	}
	before, _ := h.idDocumentRepo.Status(customerID)
	if err := h.idDocumentRepo.Save(customerID, side, data, mime, user.Id); err != nil {
		return err
	}
	status, err := h.idDocumentRepo.Status(customerID)
	if err != nil {
		return err
	}
	if err := h.recordAudit(r, "customer.id_document."+side, "customer", customerID, before, status); err != nil {
		return fmt.Errorf("failed to audit identity document upload: %w", err)
	}
	return WriteJson(w, http.StatusOK, status)
}

// HandleGetIDDocument : GET /customers/{id}/id-document/{side} — l'image elle-même.
func (h *Handler) HandleGetIDDocument(w http.ResponseWriter, r *http.Request) error {
	customerID, side, err := h.idDocumentTarget(r, true)
	if err != nil {
		return err
	}
	data, mime, err := h.idDocumentRepo.Image(customerID, side)
	if errors.Is(err, iddocument.ErrNoDocument) {
		return notFound("no identity document photo")
	}
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, err = io.Copy(w, bytes.NewReader(data))
	return err
}

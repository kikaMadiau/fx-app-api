package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	//"time"

	storeentity "fx-app-api/internal/domain/store/entity"
	storerepository "fx-app-api/internal/domain/store/repository"
	"fx-app-api/internal/domain/trader/entity"
	"fx-app-api/internal/domain/trader/permissions"
	traderservices "fx-app-api/internal/domain/trader/traderservices"
	"github.com/lib/pq"
)

type createStoreRequest struct {
	Name             string  `json:"name"`
	BCCLicenseNumber string  `json:"bcc_license_number"`
	LicenseIssuedAt  *string `json:"license_issued_at"`
	LicenseExpiresAt *string `json:"license_expires_at"`
	Supervisor       struct {
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Email     string `json:"email"`
		Phone     string `json:"phone"`
	} `json:"supervisor"`
}
type addMemberRequest struct {
	FirstName string   `json:"first_name"`
	LastName  string   `json:"last_name"`
	Email     string   `json:"email"`
	Phone     string   `json:"phone"`
	Roles     []string `json:"roles"`
}
type updateMemberRequest struct {
	Roles    []string `json:"roles"`
	IsActive *bool    `json:"is_active"`
}
type updateStoreRequest struct {
	Name             *string `json:"name"`
	BCCLicenseNumber *string `json:"bcc_license_number"`
	LicenseIssuedAt  *string `json:"license_issued_at"`
	LicenseExpiresAt *string `json:"license_expires_at"`
	Status           *string `json:"status"`
}

func (h *Handler) HandleCreateStore(w http.ResponseWriter, r *http.Request) error {
	var req createStoreRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Supervisor.Email) == "" {
		return badRequest("store name and supervisor email are required", nil)
	}
	issued, err := parseOptionalTime(req.LicenseIssuedAt)
	if err != nil {
		return badRequest("invalid license_issued_at", err)
	}
	expires, err := parseOptionalTime(req.LicenseExpiresAt)
	if err != nil {
		return badRequest("invalid license_expires_at", err)
	}
	temp, err := temporaryPassword()
	if err != nil {
		return err
	}
	hash, err := traderservices.HashPassword(temp)
	if err != nil {
		return err
	}
	store := &storeentity.Store{Name: strings.TrimSpace(req.Name), BCCLicenseNumber: req.BCCLicenseNumber, LicenseIssuedAt: issued, LicenseExpiresAt: expires, Status: "ACTIVE"}
	member := &entity.Trader{FirstName: req.Supervisor.FirstName, LastName: req.Supervisor.LastName, Email: strings.TrimSpace(req.Supervisor.Email), Phone: req.Supervisor.Phone, PasswordHash: hash}
	actor := traderservices.GetUser(r)
	if actor == nil {
		return &HTTPError{StatusCode: 401, Message: "authentication required"}
	}
	if err := h.storeRepo.CreateWithSupervisor(store, member, actor.Id); err != nil {
		return storeRepositoryError(err)
	}
	return WriteJson(w, http.StatusCreated, map[string]any{"store": store, "supervisor": map[string]any{"id": member.Id, "first_name": member.FirstName, "last_name": member.LastName, "email": member.Email, "roles": []string{"SUPERVISOR"}}, "temporary_password": temp})
}

func (h *Handler) HandleListStores(w http.ResponseWriter, r *http.Request) error {
	list, err := h.storeRepo.List()
	if err != nil {
		return fmt.Errorf("list stores: %w", err)
	}
	return WriteJson(w, http.StatusOK, map[string]any{"items": list, "total": len(list)})
}

func (h *Handler) HandleGetStore(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid store id", err)
	}
	user := traderservices.GetUser(r)
	if user == nil {
		return &HTTPError{StatusCode: 401, Message: "authentication required"}
	}
	if !user.IsPlatformAdmin && user.StoreId != id {
		return notFound("store not found")
	}
	s, err := h.storeRepo.Get(id)
	if err != nil {
		return storeRepositoryError(err)
	}
	return WriteJson(w, http.StatusOK, s)
}

func (h *Handler) HandleUpdateStore(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid store id", err)
	}
	var req updateStoreRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	patch := map[string]any{}
	if req.Name != nil {
		patch["name"] = strings.TrimSpace(*req.Name)
	}
	if req.BCCLicenseNumber != nil {
		patch["bcc_license_number"] = *req.BCCLicenseNumber
	}
	if req.Status != nil {
		v := strings.ToUpper(strings.TrimSpace(*req.Status))
		if v != "ACTIVE" && v != "SUSPENDED" && v != "CLOSED" {
			return badRequest("status must be ACTIVE, SUSPENDED or CLOSED", nil)
		}
		patch["status"] = v
	}
	if req.LicenseIssuedAt != nil {
		v, e := parseOptionalTime(req.LicenseIssuedAt)
		if e != nil {
			return badRequest("invalid license_issued_at", e)
		}
		patch["license_issued_at"] = v
	}
	if req.LicenseExpiresAt != nil {
		v, e := parseOptionalTime(req.LicenseExpiresAt)
		if e != nil {
			return badRequest("invalid license_expires_at", e)
		}
		patch["license_expires_at"] = v
	}
	actor := traderservices.GetUser(r)
	updated, err := h.storeRepo.Update(id, actor.Id, patch)
	if err != nil {
		return storeRepositoryError(err)
	}
	return WriteJson(w, http.StatusOK, updated)
}

func (h *Handler) HandleListStoreMembers(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid store id", err)
	}
	user := traderservices.GetUser(r)
	if user == nil || user.StoreId != id {
		return notFound("store not found")
	}
	members, err := h.storeRepo.Members(id)
	if err != nil {
		return fmt.Errorf("list store members: %w", err)
	}
	return WriteJson(w, http.StatusOK, map[string]any{"items": members, "total": len(members)})
}

func (h *Handler) HandleAddStoreMember(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid store id", err)
	}
	actor := traderservices.GetUser(r)
	if actor == nil {
		return &HTTPError{StatusCode: http.StatusUnauthorized, Message: "authentication required"}
	}
	if !actor.IsPlatformAdmin && actor.StoreId != id {
		return notFound("store not found")
	}
	var req addMemberRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if strings.TrimSpace(req.Email) == "" {
		return badRequest("email is required", nil)
	}
	if len(req.Roles) == 0 {
		req.Roles = []string{"TRADER"}
	}
	roles, err := normalizeStoreRoles(req.Roles)
	if err != nil {
		return badRequest("invalid roles", err)
	}
	temp, err := temporaryPassword()
	if err != nil {
		return err
	}
	hash, err := traderservices.HashPassword(temp)
	if err != nil {
		return err
	}
	member := &entity.Trader{FirstName: req.FirstName, LastName: req.LastName, Email: strings.TrimSpace(req.Email), Phone: req.Phone, Roles: strings.Join(roles, ","), PasswordHash: hash}
	created, addErr := h.storeRepo.AddMember(id, actor.Id, member)
	if addErr != nil {
		return storeRepositoryError(addErr)
	}
	response := map[string]any{"id": member.Id, "email": member.Email, "roles": roles, "is_active": true}
	if created {
		response["temporary_password"] = temp
	}
	return WriteJson(w, http.StatusCreated, response)
}

func (h *Handler) HandleUpdateStoreMember(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return badRequest("invalid store id", err)
	}
	traderID, err := positivePathInt(r, "traderId")
	if err != nil {
		return badRequest("invalid trader id", err)
	}
	var req updateMemberRequest
	if err := decodeJSON(r, &req); err != nil {
		return badRequest("invalid request body", err)
	}
	if req.IsActive == nil || len(req.Roles) == 0 {
		return badRequest("roles and is_active are required", nil)
	}
	if err := validateStoreRoles(req.Roles); err != nil {
		return badRequest("invalid roles", err)
	}
	actor := traderservices.GetUser(r)
	if actor.Id == traderID && containsRole(req.Roles, "SUPERVISOR") == false {
		members, e := h.storeRepo.Members(id)
		if e != nil {
			return e
		}
		supervisors := 0
		for _, m := range members {
			if m.IsActive && containsRole(strings.Split(m.Roles, ","), "SUPERVISOR") {
				supervisors++
			}
		}
		if supervisors <= 1 {
			return conflict("cannot remove the last supervisor")
		}
	}
	if err := h.storeRepo.UpdateMember(id, actor.Id, traderID, req.Roles, *req.IsActive); err != nil {
		return storeRepositoryError(err)
	}
	return WriteJson(w, http.StatusOK, map[string]any{"id": traderID, "roles": req.Roles, "is_active": *req.IsActive})
}

func parseOptionalTime(raw *string) (*time.Time, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *raw)
	return &t, err
}
func temporaryPassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func validateStoreRoles(roles []string) error {
	if len(roles) == 0 {
		return fmt.Errorf("at least one role is required")
	}
	for _, role := range roles {
		r, ok := permissions.NormalizeRole(role)
		if !ok || r == permissions.Admin || r == permissions.PlatformAdmin {
			return fmt.Errorf("unsupported store role %q", role)
		}
	}
	return nil
}

func normalizeStoreRoles(roles []string) ([]string, error) {
	if err := validateStoreRoles(roles); err != nil {
		return nil, err
	}
	normalized := make([]string, len(roles))
	for i, role := range roles {
		r, _ := permissions.NormalizeRole(role)
		normalized[i] = string(r)
	}
	return normalized, nil
}
func containsRole(roles []string, role string) bool {
	for _, v := range roles {
		if strings.EqualFold(strings.TrimSpace(v), role) {
			return true
		}
	}
	return false
}
func storeRepositoryError(err error) error {
	switch {
	case errors.Is(err, storerepository.ErrStoreNotFound):
		return notFound("store not found")
	case errors.Is(err, storerepository.ErrMemberInOtherStore):
		return conflict("trader already belongs to another store")
	case errors.Is(err, storerepository.ErrLastSupervisor):
		return conflict("cannot remove the last store supervisor")
	case errors.Is(err, storerepository.ErrMemberAlreadyInStore):
		return conflict("trader already belongs to this store")
	default:
		var pgErr *pq.Error
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return conflict("store license number or email already exists")
		}
		return fmt.Errorf("store operation failed: %w", err)
	}
}
func positivePathInt(r *http.Request, key string) (int, error) {
	raw := r.PathValue(key)
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid positive integer")
	}
	return value, nil
}

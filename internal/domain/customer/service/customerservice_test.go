package service

import (
	"database/sql"
	"errors"
	"fmt"
	"fx-app-api/internal/domain/customer/entity"
	"fx-app-api/internal/storage"
	"testing"
	"time"
)

type fakeCustomerStorage struct {
	customers map[int]*entity.Customer
}

func (s *fakeCustomerStorage) Init() error { return nil }

// BeginTx renvoie une transaction nil : le service écrit alors sans transaction.
func (s *fakeCustomerStorage) BeginTx() (*sql.Tx, error) { return nil, nil }

func (s *fakeCustomerStorage) CreateCustomer(customer *entity.Customer) error {
	customer.ID = len(s.customers) + 1
	s.customers[customer.ID] = cloneCustomer(customer)
	return nil
}

func (s *fakeCustomerStorage) CreateCustomerWithTx(tx *sql.Tx, customer *entity.Customer) error {
	return s.CreateCustomer(customer)
}

func (s *fakeCustomerStorage) ListCustomers(filter storage.CustomerFilter) ([]*entity.CustomerSummary, int, error) {
	return nil, 0, nil
}

func (s *fakeCustomerStorage) UpdateCustomer(customer *entity.Customer) error {
	return s.UpdateCustomerWithTx(nil, customer)
}

func (s *fakeCustomerStorage) UpdateCustomerWithTx(tx *sql.Tx, customer *entity.Customer) error {
	if _, ok := s.customers[customer.ID]; !ok {
		return fmt.Errorf("customer with ID %d not found", customer.ID)
	}
	s.customers[customer.ID] = cloneCustomer(customer)
	return nil
}

func (s *fakeCustomerStorage) GetCustomer(id int) (*entity.Customer, error) {
	customer, ok := s.customers[id]
	if !ok {
		return nil, fmt.Errorf("customer with ID %d not found", id)
	}
	return cloneCustomer(customer), nil
}

func (s *fakeCustomerStorage) GetCustomerByPhone(phone string, storeID int) (*entity.Customer, error) {
	for _, customer := range s.customers {
		if customer.Phone == phone {
			return cloneCustomer(customer), nil
		}
	}
	return nil, fmt.Errorf("%w: %s", storage.ErrCustomerNotFound, phone)
}

type fakeKYCProfileStorage struct {
	profiles map[int]*CustomerKYCProfile
}

func (s *fakeKYCProfileStorage) Init() error { return nil }

func (s *fakeKYCProfileStorage) CreateKYCProfile(profile *CustomerKYCProfile) error {
	profile.ID = len(s.profiles) + 1
	s.profiles[profile.CustomerID] = cloneKYCProfile(profile)
	return nil
}

func (s *fakeKYCProfileStorage) CreateKYCProfileWithTx(tx *sql.Tx, profile *CustomerKYCProfile) error {
	return s.CreateKYCProfile(profile)
}

func (s *fakeKYCProfileStorage) GetKYCProfileByCustomerID(customerID int) (*CustomerKYCProfile, error) {
	profile, ok := s.profiles[customerID]
	if !ok {
		return nil, fmt.Errorf("%w: customer %d", ErrKYCProfileNotFound, customerID)
	}
	return cloneKYCProfile(profile), nil
}

func (s *fakeKYCProfileStorage) UpdateKYCProfile(profile *CustomerKYCProfile) error {
	if _, ok := s.profiles[profile.CustomerID]; !ok {
		return fmt.Errorf("KYC profile for customer %d not found", profile.CustomerID)
	}
	s.profiles[profile.CustomerID] = cloneKYCProfile(profile)
	return nil
}

func TestUpdateKYCProfilePersistsProfileAndReassessesRisk(t *testing.T) {
	customerStore := &fakeCustomerStorage{
		customers: map[int]*entity.Customer{
			1: {ID: 1, FullName: "Ada Lovelace", IDNumber: "ID-1"},
		},
	}
	kycStore := &fakeKYCProfileStorage{
		profiles: map[int]*CustomerKYCProfile{
			1: {
				ID:              10,
				CustomerID:      1,
				LegalNature:     Individual,
				ActivityProfile: Personal,
				KYCStatus:       Unverified,
				CreatedAt:       time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			},
		},
	}
	service := NewCustomerServiceWithKYC(customerStore, kycStore)

	updatedProfile, err := service.UpdateKYCProfile(1, &CustomerKYCProfile{
		LegalNature:         Individual,
		ActivityProfile:     Business,
		KYCStatus:           Verified,
		SourceOfFunds:       "Trading income",
		PurposeOfOperations: "FX operations",
		ExpectedVolume:      50000,
		ExpectedFrequency:   12,
	})
	if err != nil {
		t.Fatalf("UpdateKYCProfile returned error: %v", err)
	}

	if updatedProfile.ID != 10 {
		t.Fatalf("expected profile ID to be preserved, got %d", updatedProfile.ID)
	}
	if updatedProfile.CustomerID != 1 {
		t.Fatalf("expected profile customer ID to be 1, got %d", updatedProfile.CustomerID)
	}
	if updatedProfile.CreatedAt.IsZero() {
		t.Fatal("expected profile CreatedAt to be preserved")
	}

	persistedProfile := kycStore.profiles[1]
	if persistedProfile.ActivityProfile != Business {
		t.Fatalf("expected persisted activity profile %q, got %q", Business, persistedProfile.ActivityProfile)
	}

	persistedCustomer := customerStore.customers[1]
	if persistedCustomer.RiskScore != 20 {
		t.Fatalf("expected customer risk score 20, got %.2f", persistedCustomer.RiskScore)
	}
	if persistedCustomer.RiskLevel != "LOW" {
		t.Fatalf("expected customer risk level LOW, got %q", persistedCustomer.RiskLevel)
	}
}

func TestUpdateKYCProfileRejectsMismatchedCustomerID(t *testing.T) {
	customerStore := &fakeCustomerStorage{
		customers: map[int]*entity.Customer{
			1: {ID: 1, FullName: "Ada Lovelace", IDNumber: "ID-1"},
		},
	}
	kycStore := &fakeKYCProfileStorage{
		profiles: map[int]*CustomerKYCProfile{
			1: {ID: 10, CustomerID: 1, LegalNature: Individual, ActivityProfile: Personal, KYCStatus: Unverified},
		},
	}
	service := NewCustomerServiceWithKYC(customerStore, kycStore)

	_, err := service.UpdateKYCProfile(1, &CustomerKYCProfile{
		CustomerID:      2,
		LegalNature:     Individual,
		ActivityProfile: Personal,
		KYCStatus:       Verified,
	})
	if err == nil {
		t.Fatal("expected mismatched customer_id error")
	}
}

func TestTriggerRiskReassessmentRequiresKYCRepository(t *testing.T) {
	service := NewCustomerService(&fakeCustomerStorage{customers: map[int]*entity.Customer{}})

	_, err := service.TriggerRiskReassessment(1)
	if err == nil {
		t.Fatal("expected missing KYC repository error")
	}
}

func TestCreateCustomerWithKYCCreatesCustomerProfileAndInitialRisk(t *testing.T) {
	customerStore := &fakeCustomerStorage{customers: map[int]*entity.Customer{}}
	kycStore := &fakeKYCProfileStorage{profiles: map[int]*CustomerKYCProfile{}}
	service := NewCustomerServiceWithKYC(customerStore, kycStore)

	created, isNew, err := service.CreateCustomerWithKYC(
		&entity.Customer{FullName: "Ada Lovelace", IDNumber: "ID-1", Phone: "+243810000001"},
		&CustomerKYCProfile{
			LegalNature:     Individual,
			ActivityProfile: Business,
			KYCStatus:       Verified, // ignoré : le statut initial est toujours UNVERIFIED
		},
	)
	if err != nil {
		t.Fatalf("CreateCustomerWithKYC returned error: %v", err)
	}
	if !isNew || created.ID == 0 {
		t.Fatal("expected a new customer to be created with an ID")
	}

	profile := kycStore.profiles[created.ID]
	if profile == nil {
		t.Fatal("expected the KYC profile to be created for the new customer")
	}
	if profile.KYCStatus != Unverified {
		t.Fatalf("expected initial KYC status %q, got %q", Unverified, profile.KYCStatus)
	}

	stored := customerStore.customers[created.ID]
	if stored.RiskScore != 20 || stored.RiskLevel != "LOW" {
		t.Fatalf("expected persisted risk 20/LOW, got %.2f/%q", stored.RiskScore, stored.RiskLevel)
	}
}

func TestCreateCustomerWithKYCRejectsInvalidProfileBeforeWriting(t *testing.T) {
	cases := map[string]*CustomerKYCProfile{
		"nil profile":           nil,
		"unknown legal nature":  {LegalNature: "COMPANY", ActivityProfile: Business},
		"unknown activity":      {LegalNature: Individual, ActivityProfile: "HOBBY"},
		"unknown kyc status":    {LegalNature: Individual, ActivityProfile: Personal, KYCStatus: "MAYBE"},
		"legal entity personal": {LegalNature: LegalEntity, ActivityProfile: Personal},
		"negative volume":       {LegalNature: Individual, ActivityProfile: Personal, ExpectedVolume: -1},
		"negative frequency":    {LegalNature: Individual, ActivityProfile: Personal, ExpectedFrequency: -1},
	}

	for name, profile := range cases {
		t.Run(name, func(t *testing.T) {
			customerStore := &fakeCustomerStorage{customers: map[int]*entity.Customer{}}
			kycStore := &fakeKYCProfileStorage{profiles: map[int]*CustomerKYCProfile{}}
			service := NewCustomerServiceWithKYC(customerStore, kycStore)

			_, _, err := service.CreateCustomerWithKYC(
				&entity.Customer{FullName: "Ada Lovelace", IDNumber: "ID-1"},
				profile,
			)
			if !errors.Is(err, ErrInvalidKYCProfile) {
				t.Fatalf("expected ErrInvalidKYCProfile, got %v", err)
			}
			if len(customerStore.customers) != 0 || len(kycStore.profiles) != 0 {
				t.Fatal("expected nothing to be written when the profile is invalid")
			}
		})
	}
}

func TestCreateCustomerWithKYCReusesExistingCustomerAndProfile(t *testing.T) {
	customerStore := &fakeCustomerStorage{customers: map[int]*entity.Customer{}}
	kycStore := &fakeKYCProfileStorage{profiles: map[int]*CustomerKYCProfile{}}
	service := NewCustomerServiceWithKYC(customerStore, kycStore)

	first, isNew, err := service.CreateCustomerWithKYC(
		&entity.Customer{FullName: "Ada Lovelace", IDNumber: "ID-1", Phone: "+243810000001"},
		&CustomerKYCProfile{LegalNature: Individual, ActivityProfile: Personal},
	)
	if err != nil || !isNew {
		t.Fatalf("expected the first call to create the customer, got isNew=%v err=%v", isNew, err)
	}
	kycStore.profiles[first.ID].KYCStatus = Verified // simule une vérification déjà faite

	second, isNew, err := service.CreateCustomerWithKYC(
		&entity.Customer{FullName: "Ada L.", IDNumber: "ID-2", Phone: "+243810000001"},
		&CustomerKYCProfile{LegalNature: Individual, ActivityProfile: Business},
	)
	if err != nil {
		t.Fatalf("expected the existing customer to be reused, got error: %v", err)
	}
	if isNew || second.ID != first.ID {
		t.Fatalf("expected customer %d to be reused, got id=%d isNew=%v", first.ID, second.ID, isNew)
	}
	if len(customerStore.customers) != 1 || len(kycStore.profiles) != 1 {
		t.Fatal("expected no extra customer or profile to be created")
	}
	profile := kycStore.profiles[first.ID]
	if profile.KYCStatus != Verified || profile.ActivityProfile != Personal {
		t.Fatalf("expected the existing KYC profile to be left untouched, got %+v", profile)
	}
	if customerStore.customers[first.ID].FullName != "Ada Lovelace" {
		t.Fatal("expected the existing customer data to be left untouched")
	}
}

func TestCreateCustomerWithKYCCreatesMissingProfileForExistingCustomer(t *testing.T) {
	customerStore := &fakeCustomerStorage{customers: map[int]*entity.Customer{
		1: {ID: 1, FullName: "Ada Lovelace", IDNumber: "ID-1", Phone: "+243810000001"},
	}}
	kycStore := &fakeKYCProfileStorage{profiles: map[int]*CustomerKYCProfile{}}
	service := NewCustomerServiceWithKYC(customerStore, kycStore)

	customer, isNew, err := service.CreateCustomerWithKYC(
		&entity.Customer{FullName: "Ada Lovelace", IDNumber: "ID-1", Phone: "+243810000001"},
		&CustomerKYCProfile{LegalNature: Individual, ActivityProfile: Business, KYCStatus: Verified},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isNew || customer.ID != 1 {
		t.Fatalf("expected existing customer 1 to be reused, got id=%d isNew=%v", customer.ID, isNew)
	}

	profile := kycStore.profiles[1]
	if profile == nil || profile.KYCStatus != Unverified {
		t.Fatalf("expected an UNVERIFIED profile to be created, got %+v", profile)
	}
	if customerStore.customers[1].RiskScore != 20 {
		t.Fatalf("expected risk to be reassessed to 20, got %.2f", customerStore.customers[1].RiskScore)
	}
}

func TestUpdateKYCProfileRejectsUnknownEnumValues(t *testing.T) {
	customerStore := &fakeCustomerStorage{
		customers: map[int]*entity.Customer{
			1: {ID: 1, FullName: "Ada Lovelace", IDNumber: "ID-1"},
		},
	}
	kycStore := &fakeKYCProfileStorage{
		profiles: map[int]*CustomerKYCProfile{
			1: {ID: 10, CustomerID: 1, LegalNature: Individual, ActivityProfile: Personal, KYCStatus: Unverified},
		},
	}
	service := NewCustomerServiceWithKYC(customerStore, kycStore)

	_, err := service.UpdateKYCProfile(1, &CustomerKYCProfile{
		LegalNature:     Individual,
		ActivityProfile: "HOBBY",
	})
	if !errors.Is(err, ErrInvalidKYCProfile) {
		t.Fatalf("expected ErrInvalidKYCProfile, got %v", err)
	}
	if kycStore.profiles[1].ActivityProfile != Personal {
		t.Fatal("expected the stored profile to stay unchanged")
	}
}

func cloneCustomer(customer *entity.Customer) *entity.Customer {
	copy := *customer
	return &copy
}

func cloneKYCProfile(profile *CustomerKYCProfile) *CustomerKYCProfile {
	copy := *profile
	return &copy
}

func (s *fakeCustomerStorage) GetCustomerWithTx(tx *sql.Tx, id int) (*entity.Customer, error) {
	return s.GetCustomer(id)
}

func (s *fakeKYCProfileStorage) GetKYCProfileByCustomerIDWithTx(tx *sql.Tx, customerID int) (*CustomerKYCProfile, error) {
	return s.GetKYCProfileByCustomerID(customerID)
}

func TestUpdateKYCStatusChangesStatusAndVerificationTime(t *testing.T) {
	customerStore := &fakeCustomerStorage{customers: map[int]*entity.Customer{1: {ID: 1, FullName: "KYC Subject", IDNumber: "ID-1"}}}
	kycStore := &fakeKYCProfileStorage{profiles: map[int]*CustomerKYCProfile{1: {
		ID: 10, CustomerID: 1, LegalNature: Individual, ActivityProfile: Personal,
		KYCStatus: Unverified, SourceOfFunds: "Salary", PurposeOfOperations: "Travel",
	}}}
	service := NewCustomerServiceWithKYC(customerStore, kycStore)
	updated, err := service.UpdateKYCStatus(1, Verified)
	if err != nil {
		t.Fatalf("UpdateKYCStatus returned error: %v", err)
	}
	if updated.KYCStatus != Verified {
		t.Fatalf("status = %s", updated.KYCStatus)
	}
	if updated.LastVerificationDate.IsZero() {
		t.Fatal("verification timestamp was not recorded")
	}
	persisted, err := kycStore.GetKYCProfileByCustomerID(1)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.KYCStatus != Verified {
		t.Fatalf("persisted status = %s", persisted.KYCStatus)
	}
}

func TestUpdateKYCStatusRejectsUnknownStatus(t *testing.T) {
	service := NewCustomerServiceWithKYC(&fakeCustomerStorage{customers: map[int]*entity.Customer{}}, &fakeKYCProfileStorage{profiles: map[int]*CustomerKYCProfile{}})
	_, err := service.UpdateKYCStatus(1, KYCStatus("SUPER_VERIFIED"))
	if !errors.Is(err, ErrInvalidKYCProfile) {
		t.Fatalf("error = %v, want ErrInvalidKYCProfile", err)
	}
}

package service

import (
	"fmt"
	"fx-app-api/internal/domain/customer/entity"
	"testing"
	"time"
)

type fakeCustomerStorage struct {
	customers map[int]*entity.Customer
}

func (s *fakeCustomerStorage) Init() error { return nil }

func (s *fakeCustomerStorage) CreateCustomer(customer *entity.Customer) error {
	customer.ID = len(s.customers) + 1
	s.customers[customer.ID] = cloneCustomer(customer)
	return nil
}

func (s *fakeCustomerStorage) UpdateCustomer(customer *entity.Customer) error {
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

type fakeKYCProfileStorage struct {
	profiles map[int]*CustomerKYCProfile
}

func (s *fakeKYCProfileStorage) Init() error { return nil }

func (s *fakeKYCProfileStorage) CreateKYCProfile(profile *CustomerKYCProfile) error {
	profile.ID = len(s.profiles) + 1
	s.profiles[profile.CustomerID] = cloneKYCProfile(profile)
	return nil
}

func (s *fakeKYCProfileStorage) GetKYCProfileByCustomerID(customerID int) (*CustomerKYCProfile, error) {
	profile, ok := s.profiles[customerID]
	if !ok {
		return nil, fmt.Errorf("KYC profile for customer %d not found", customerID)
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

func cloneCustomer(customer *entity.Customer) *entity.Customer {
	copy := *customer
	return &copy
}

func cloneKYCProfile(profile *CustomerKYCProfile) *CustomerKYCProfile {
	copy := *profile
	return &copy
}

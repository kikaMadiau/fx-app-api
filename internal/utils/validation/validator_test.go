package validation

import (
	"testing"
)

// Test structures
type TestCustomer struct {
	FullName   string `validate:"required,name,min=2,max=100"`
	IDNumber   string `validate:"required,idnumber"`
	Phone      string `validate:"required,e164"`
	Email      string `validate:"required,emailformat"`
	Address    string `validate:"max=200"`
	PostalCode string `validate:"max=10"`
	RiskLevel  string `validate:"omitempty,oneof=LOW MEDIUM HIGH"`
}

type TestTransaction struct {
	Amount         float64 `validate:"required,gt=0,lte=10000000"`
	Currency       string  `validate:"required,currency"`
	TargetCurrency string  `validate:"required,currency"`
	Type           string  `validate:"required,oneof=BUY SELL EXCHANGE TRANSFER"`
	CustomerID     int     `validate:"required,gt=0"`
}

type TestTrader struct {
	Email    string `validate:"required,emailformat"`
	Password string `validate:"required,strongpassword,min=8"`
	Name     string `validate:"required,name,min=2,max=100"`
	Role     string `validate:"required,oneof=TRADER COMPLIANCE_OFFICER SUPERVISOR ADMIN"`
}

func TestValidateCustomer(t *testing.T) {
	tests := []struct {
		name     string
		customer TestCustomer
		hasError bool
	}{
		{
			name: "Valid customer",
			customer: TestCustomer{
				FullName:   "Jean Dupont",
				IDNumber:   "AB123456",
				Phone:      "+33612345678",
				Email:      "jean.dupont@example.com",
				Address:    "123 Rue de la Paix, Paris",
				PostalCode: "75001",
				RiskLevel:  "LOW",
			},
			hasError: false,
		},
		{
			name: "Invalid email",
			customer: TestCustomer{
				FullName: "Jean Dupont",
				IDNumber: "AB123456",
				Phone:    "+33612345678",
				Email:    "invalid-email",
				Address:  "123 Rue de la Paix, Paris",
			},
			hasError: true,
		},
		{
			name: "Invalid phone",
			customer: TestCustomer{
				FullName: "Jean Dupont",
				IDNumber: "AB123456",
				Phone:    "not-a-phone",
				Email:    "jean.dupont@example.com",
			},
			hasError: true,
		},
		{
			name: "Missing required fields",
			customer: TestCustomer{
				FullName: "", // Required
				IDNumber: "", // Required
				Phone:    "", // Required
				Email:    "", // Required
			},
			hasError: true,
		},
		{
			name: "Invalid risk level",
			customer: TestCustomer{
				FullName:  "Jean Dupont",
				IDNumber:  "AB123456",
				Phone:     "+33612345678",
				Email:     "jean.dupont@example.com",
				RiskLevel: "INVALID",
			},
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(&tt.customer)

			if tt.hasError && err == nil {
				t.Error("Expected validation error but got none")
			}

			if !tt.hasError && err != nil {
				t.Errorf("Unexpected validation error: %v", err)
			}
		})
	}
}

func TestValidateTransaction(t *testing.T) {
	tests := []struct {
		name        string
		transaction TestTransaction
		hasError    bool
	}{
		{
			name: "Valid transaction",
			transaction: TestTransaction{
				Amount:         1000.50,
				Currency:       "EUR",
				TargetCurrency: "USD",
				Type:           "BUY",
				CustomerID:     1,
			},
			hasError: false,
		},
		{
			name: "Invalid currency",
			transaction: TestTransaction{
				Amount:         1000.50,
				Currency:       "EURO", // 4 lettres
				TargetCurrency: "USD",
				Type:           "BUY",
				CustomerID:     1,
			},
			hasError: true,
		},
		{
			name: "Same currency",
			transaction: TestTransaction{
				Amount:         1000.50,
				Currency:       "EUR",
				TargetCurrency: "EUR", // Même devise
				Type:           "BUY",
				CustomerID:     1,
			},
			hasError: false, // La validation 'different' n'est pas dans le validateur de base
		},
		{
			name: "Zero amount",
			transaction: TestTransaction{
				Amount:         0,
				Currency:       "EUR",
				TargetCurrency: "USD",
				Type:           "BUY",
				CustomerID:     1,
			},
			hasError: true,
		},
		{
			name: "Negative amount",
			transaction: TestTransaction{
				Amount:         -100,
				Currency:       "EUR",
				TargetCurrency: "USD",
				Type:           "BUY",
				CustomerID:     1,
			},
			hasError: true,
		},
		{
			name: "Invalid type",
			transaction: TestTransaction{
				Amount:         1000.50,
				Currency:       "EUR",
				TargetCurrency: "USD",
				Type:           "INVALID",
				CustomerID:     1,
			},
			hasError: true,
		},
		{
			name: "Zero customer ID",
			transaction: TestTransaction{
				Amount:         1000.50,
				Currency:       "EUR",
				TargetCurrency: "USD",
				Type:           "BUY",
				CustomerID:     0,
			},
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(&tt.transaction)

			if tt.hasError && err == nil {
				t.Error("Expected validation error but got none")
			}

			if !tt.hasError && err != nil {
				t.Errorf("Unexpected validation error: %v", err)
			}
		})
	}
}

func TestValidateTrader(t *testing.T) {
	tests := []struct {
		name     string
		trader   TestTrader
		hasError bool
	}{
		{
			name: "Valid trader",
			trader: TestTrader{
				Email:    "trader@example.com",
				Password: "SecureP@ss123",
				Name:     "Trader Name",
				Role:     "TRADER",
			},
			hasError: false,
		},
		{
			name: "Weak password",
			trader: TestTrader{
				Email:    "trader@example.com",
				Password: "weak", // Trop court, pas de majuscule/chiffre/spécial
				Name:     "Trader Name",
				Role:     "TRADER",
			},
			hasError: true,
		},
		{
			name: "Invalid role",
			trader: TestTrader{
				Email:    "trader@example.com",
				Password: "SecureP@ss123",
				Name:     "Trader Name",
				Role:     "INVALID_ROLE",
			},
			hasError: true,
		},
		{
			name: "Invalid name",
			trader: TestTrader{
				Email:    "trader@example.com",
				Password: "SecureP@ss123",
				Name:     "T", // Trop court
				Role:     "TRADER",
			},
			hasError: true,
		},
		{
			name: "Invalid email",
			trader: TestTrader{
				Email:    "not-an-email",
				Password: "SecureP@ss123",
				Name:     "Trader Name",
				Role:     "TRADER",
			},
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(&tt.trader)

			if tt.hasError && err == nil {
				t.Error("Expected validation error but got none")
			}

			if !tt.hasError && err != nil {
				t.Errorf("Unexpected validation error: %v", err)
			}
		})
	}
}

func TestSanitizeInput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Trim spaces",
			input:    "  test  ",
			expected: "test",
		},
		{
			name:     "Escape HTML",
			input:    "<script>alert('xss')</script>",
			expected: "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;",
		},
		{
			name:     "Mixed content",
			input:    "  Hello <b>World</b>  ",
			expected: "Hello &lt;b&gt;World&lt;/b&gt;",
		},
		{
			name:     "Quotes",
			input:    `He said "Hello"`,
			expected: "He said &quot;Hello&quot;",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeInput(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizeInput(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSanitizePhone(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Valid E.164",
			input:    "+33612345678",
			expected: "+33612345678",
		},
		{
			name:     "With spaces and dashes",
			input:    "+33 6 12 34 56 78",
			expected: "+33612345678",
		},
		{
			name:     "Without plus",
			input:    "33612345678",
			expected: "+33612345678",
		},
		{
			name:     "With parentheses",
			input:    "+33 (0)6 12-34-56-78",
			expected: "+330612345678",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizePhone(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizePhone(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestValidateAmount(t *testing.T) {
	tests := []struct {
		name    string
		amount  float64
		wantErr bool
	}{
		{"Valid amount", 1000.50, false},
		{"Zero amount", 0, true},
		{"Negative amount", -100, true},
		{"Very large amount", 20000000, true},
		{"Small positive", 0.01, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAmount(tt.amount)

			if tt.wantErr && err == nil {
				t.Error("Expected error but got none")
			}

			if !tt.wantErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestValidateBusinessID(t *testing.T) {
	tests := []struct {
		name    string
		id      int
		wantErr bool
	}{
		{"Valid ID", 1, false},
		{"Valid large ID", 999999, false},
		{"Zero ID", 0, true},
		{"Negative ID", -1, true},
		{"Too large ID", 2000000000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBusinessID(tt.id)

			if tt.wantErr && err == nil {
				t.Error("Expected error but got none")
			}

			if !tt.wantErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestValidateRequest(t *testing.T) {
	type SimpleRequest struct {
		Name  string `validate:"required,min=2"`
		Email string `validate:"required,emailformat"`
	}

	tests := []struct {
		name     string
		request  interface{}
		expected int // Nombre d'erreurs attendues
	}{
		{
			name: "Valid request",
			request: SimpleRequest{
				Name:  "John Doe",
				Email: "john@example.com",
			},
			expected: 0,
		},
		{
			name: "Invalid request - missing fields",
			request: SimpleRequest{
				Name:  "", // Requis
				Email: "", // Requis
			},
			expected: 2,
		},
		{
			name: "Invalid request - bad email",
			request: SimpleRequest{
				Name:  "John Doe",
				Email: "not-an-email",
			},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := ValidateRequest(tt.request)

			if len(errors) != tt.expected {
				t.Errorf("ValidateRequest() returned %d errors, want %d", len(errors), tt.expected)
				for field, err := range errors {
					t.Logf("  %s: %s", field, err)
				}
			}
		})
	}
}

package storage

import (
	"os"
	"strings"
	"testing"
)

func TestIsProductionEnvironment(t *testing.T) {
	// Sauvegarder l'environnement original
	origEnv := os.Getenv("APP_ENV")
	origDBHost := os.Getenv("DB_HOST")
	origDyno := os.Getenv("DYNO")
	origKService := os.Getenv("K_SERVICE")
	origAWSLambda := os.Getenv("AWS_LAMBDA_FUNCTION_NAME")
	origAzure := os.Getenv("WEBSITE_SITE_NAME")
	origDatabaseURL := os.Getenv("DATABASE_URL")

	defer func() {
		os.Setenv("APP_ENV", origEnv)
		os.Setenv("DB_HOST", origDBHost)
		os.Setenv("DYNO", origDyno)
		os.Setenv("K_SERVICE", origKService)
		os.Setenv("AWS_LAMBDA_FUNCTION_NAME", origAWSLambda)
		os.Setenv("WEBSITE_SITE_NAME", origAzure)
		os.Setenv("DATABASE_URL", origDatabaseURL)
	}()

	tests := []struct {
		name     string
		setup    func()
		expected bool
	}{
		{
			name: "Development environment - localhost",
			setup: func() {
				os.Unsetenv("APP_ENV")
				os.Setenv("DB_HOST", "localhost")
			},
			expected: false,
		},
		{
			name: "Production by APP_ENV",
			setup: func() {
				os.Setenv("APP_ENV", "production")
				os.Setenv("DB_HOST", "localhost")
			},
			expected: true,
		},
		{
			name: "Production by DB_HOST remote",
			setup: func() {
				os.Unsetenv("APP_ENV")
				os.Setenv("DB_HOST", "database.example.com")
			},
			expected: true,
		},
		{
			name: "Heroku environment",
			setup: func() {
				os.Unsetenv("APP_ENV")
				os.Unsetenv("DB_HOST")
				os.Setenv("DYNO", "web.1")
			},
			expected: true,
		},
		{
			name: "Google Cloud Run",
			setup: func() {
				os.Unsetenv("APP_ENV")
				os.Unsetenv("DB_HOST")
				os.Setenv("K_SERVICE", "my-service")
			},
			expected: true,
		},
		{
			name: "AWS Lambda",
			setup: func() {
				os.Unsetenv("APP_ENV")
				os.Unsetenv("DB_HOST")
				os.Setenv("AWS_LAMBDA_FUNCTION_NAME", "my-function")
			},
			expected: true,
		},
		{
			name: "Azure App Service",
			setup: func() {
				os.Unsetenv("APP_ENV")
				os.Unsetenv("DB_HOST")
				os.Setenv("WEBSITE_SITE_NAME", "my-app")
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Nettoyer l'environnement
			os.Unsetenv("APP_ENV")
			os.Unsetenv("DB_HOST")
			os.Unsetenv("DYNO")
			os.Unsetenv("K_SERVICE")
			os.Unsetenv("AWS_LAMBDA_FUNCTION_NAME")
			os.Unsetenv("WEBSITE_SITE_NAME")
			os.Unsetenv("DATABASE_URL")

			// Configurer le test
			tt.setup()

			// Exécuter la fonction
			result := isProductionEnvironment()

			// Vérifier le résultat
			if result != tt.expected {
				t.Errorf("isProductionEnvironment() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestRemoteDatabaseURLIsProductionAndRedacted(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("DB_HOST", "")
	t.Setenv("DATABASE_URL", "postgres://operator:top-secret@db.example.test:5432/fx?sslmode=verify-full")
	if !isProductionEnvironment() {
		t.Fatal("remote DATABASE_URL should be treated as production")
	}
	redacted := redactedConnStr(os.Getenv("DATABASE_URL"))
	if strings.Contains(redacted, "top-secret") || !strings.Contains(redacted, "REDACTED") {
		t.Fatalf("DATABASE_URL was not safely redacted: %s", redacted)
	}
}

func TestCheckDatabaseSecurity(t *testing.T) {
	// Sauvegarder l'environnement original
	origEnv := os.Getenv("APP_ENV")
	origSSLMode := os.Getenv("DB_SSLMODE")
	origDBPassword := os.Getenv("DB_PASSWORD")
	origDBHost := os.Getenv("DB_HOST")

	defer func() {
		os.Setenv("APP_ENV", origEnv)
		os.Setenv("DB_SSLMODE", origSSLMode)
		os.Setenv("DB_PASSWORD", origDBPassword)
		os.Setenv("DB_HOST", origDBHost)
	}()

	tests := []struct {
		name        string
		setup       func()
		expectError bool
		errorMsg    string
	}{
		{
			name: "Development with disable SSL mode",
			setup: func() {
				os.Unsetenv("APP_ENV")
				os.Setenv("DB_SSLMODE", "disable")
				os.Setenv("DB_HOST", "localhost")
			},
			expectError: false, // Pas d'erreur en développement
		},
		{
			name: "Production with disable SSL mode - SHOULD ERROR",
			setup: func() {
				os.Setenv("APP_ENV", "production")
				os.Setenv("DB_SSLMODE", "disable")
			},
			expectError: true,
			errorMsg:    "INSECURE: database SSL mode",
		},
		{
			name: "Production with require SSL mode",
			setup: func() {
				os.Setenv("APP_ENV", "production")
				os.Setenv("DB_SSLMODE", "require")
				os.Setenv("DB_PASSWORD", "secure_password")
			},
			expectError: false,
		},
		{
			name: "Production with verify-full SSL mode",
			setup: func() {
				os.Setenv("APP_ENV", "production")
				os.Setenv("DB_SSLMODE", "verify-full")
				os.Setenv("DB_PASSWORD", "secure_password")
			},
			expectError: false,
		},
		{
			name: "Production with empty password - warning only",
			setup: func() {
				os.Setenv("APP_ENV", "production")
				os.Setenv("DB_SSLMODE", "require")
				os.Setenv("DB_PASSWORD", "")
			},
			expectError: false, // Warning mais pas d'erreur
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Nettoyer l'environnement
			os.Unsetenv("APP_ENV")
			os.Unsetenv("DB_SSLMODE")
			os.Unsetenv("DB_PASSWORD")
			os.Unsetenv("DB_HOST")

			// Configurer le test
			tt.setup()

			// Exécuter la fonction
			err := CheckDatabaseSecurity()

			// Vérifier les erreurs
			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				} else if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Error message doesn't contain expected text. Got: %v, Want: %v", err.Error(), tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestPostgresConnStr(t *testing.T) {
	// Sauvegarder l'environnement original
	origEnv := os.Getenv("APP_ENV")
	origSSLMode := os.Getenv("DB_SSLMODE")
	origDatabaseURL := os.Getenv("DATABASE_URL")

	defer func() {
		os.Setenv("APP_ENV", origEnv)
		os.Setenv("DB_SSLMODE", origSSLMode)
		os.Setenv("DATABASE_URL", origDatabaseURL)
	}()

	tests := []struct {
		name     string
		setup    func()
		expected string
	}{
		{
			name: "Default development settings",
			setup: func() {
				os.Unsetenv("APP_ENV")
				os.Unsetenv("DB_SSLMODE")
				os.Unsetenv("DATABASE_URL")
			},
			expected: "sslmode=prefer", // Nouvelle valeur par défaut
		},
		{
			name: "Production environment default",
			setup: func() {
				os.Setenv("APP_ENV", "production")
				os.Unsetenv("DB_SSLMODE")
				os.Unsetenv("DATABASE_URL")
			},
			expected: "sslmode=require", // Valeur par défaut pour production
		},
		{
			name: "Explicit SSL mode",
			setup: func() {
				os.Unsetenv("APP_ENV")
				os.Setenv("DB_SSLMODE", "verify-full")
				os.Unsetenv("DATABASE_URL")
			},
			expected: "sslmode=verify-full",
		},
		{
			name: "DATABASE_URL takes precedence",
			setup: func() {
				os.Unsetenv("APP_ENV")
				os.Setenv("DB_SSLMODE", "require")
				os.Setenv("DATABASE_URL", "postgres://user:pass@host/db?sslmode=disable")
			},
			expected: "postgres://user:pass@host/db?sslmode=disable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Nettoyer l'environnement
			os.Unsetenv("APP_ENV")
			os.Unsetenv("DB_SSLMODE")
			os.Unsetenv("DATABASE_URL")

			// Configurer le test
			tt.setup()

			// Exécuter la fonction
			result := postgresConnStr()

			// Vérifier le résultat
			if !strings.Contains(result, tt.expected) {
				t.Errorf("postgresConnStr() = %v, want containing %v", result, tt.expected)
			}
		})
	}
}

func TestRedactedConnStr(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Password redaction",
			input:    "host=localhost port=5432 user=postgres password=secret dbname=mydb sslmode=require",
			expected: "password=REDACTED",
		},
		{
			name:     "No password",
			input:    "host=localhost port=5432 user=postgres dbname=mydb sslmode=require",
			expected: "password=REDACTED", // Ne devrait pas apparaître
		},
		{
			name:     "Multiple parameters",
			input:    "host=dbserver.example.com port=5432 user=appuser password=P@ssw0rd!123 dbname=production sslmode=verify-full",
			expected: "password=REDACTED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := redactedConnStr(tt.input)

			if tt.expected == "password=REDACTED" {
				// Vérifier que le mot de passe est masqué
				if strings.Contains(tt.input, "password=") && !strings.Contains(result, "password=REDACTED") {
					t.Errorf("redactedConnStr() didn't redact password. Got: %v", result)
				}
			} else if !strings.Contains(result, tt.expected) {
				t.Errorf("redactedConnStr() = %v, want containing %v", result, tt.expected)
			}
		})
	}
}

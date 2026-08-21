package storage

import (
	"database/sql"
	"fmt"

	"os"
	"os/user"
	"strings"

	"github.com/lib/pq"
	_ "github.com/lib/pq"
)

type PostgresStore struct {
	db *sql.DB
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
}

// DB returns the underlying *sql.DB connection.
func (s *PostgresStore) DB() *sql.DB {
	return s.db
}

func NewPostgresStore() (*PostgresStore, error) {
	connStr := postgresConnStr()
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}

	if err := db.Ping(); err != nil {
		// Si la base de données n'existe pas, on essaie de la créer.
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "3D000" { // 3D000 = invalid_catalog_name
			dbName := getenv("DB_NAME", "forex_trader_kyc_db")
			if err := createDatabase(connStr, dbName); err != nil {
				return nil, fmt.Errorf("failed to create database %s: %w", dbName, err)
			}

			// Reconnexion à la base de données maintenant qu'elle est créée.
			db, err = sql.Open("postgres", connStr)
			if err != nil {
				return nil, fmt.Errorf("re-open postgres connection after create: %w", err)
			}
			if err := db.Ping(); err != nil {
				return nil, fmt.Errorf("ping postgres after create using %s: %w", redactedConnStr(connStr), err)
			}

			// La connexion a réussi après la création.
			return &PostgresStore{db: db}, nil
		}

		// Pour toute autre erreur de ping, on retourne l'erreur.
		return nil, fmt.Errorf("ping postgres using %s: %w", redactedConnStr(connStr), err)
	}

	return &PostgresStore{db: db}, nil
}

func postgresConnStr() string {
	if databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL")); databaseURL != "" {
		return databaseURL
	}

	parts := []string{
		fmt.Sprintf("host=%s", getenv("DB_HOST", "localhost")),
		fmt.Sprintf("port=%s", getenv("DB_PORT", "5432")),
		fmt.Sprintf("user=%s", getenv("DB_USER", defaultDBUser())),
		fmt.Sprintf("dbname=%s", getenv("DB_NAME", "forex_trader_kyc_db")),
		fmt.Sprintf("sslmode=%s", getenv("DB_SSLMODE", "disable")),
	}

	if password := strings.TrimSpace(os.Getenv("DB_PASSWORD")); password != "" {
		parts = append(parts, fmt.Sprintf("password=%s", password))
	}

	return strings.Join(parts, " ")
}

// createDatabase se connecte à la base de données 'postgres' par défaut pour créer la base de données cible.
func createDatabase(connStr, dbName string) error {
	// Créer une chaîne de connexion pour la base de données 'postgres'
	// en remplaçant le nom de la base de données cible.
	maintenanceDBConnStr := strings.Replace(connStr, fmt.Sprintf("dbname=%s", dbName), "dbname=postgres", 1)

	// Connexion à la base de données de maintenance
	mdb, err := sql.Open("postgres", maintenanceDBConnStr)
	if err != nil {
		return fmt.Errorf("failed to open maintenance db connection: %w", err)
	}
	defer mdb.Close()

	if err = mdb.Ping(); err != nil {
		return fmt.Errorf("failed to ping maintenance db: %w", err)
	}

	// Exécution de la commande de création de la base de données.
	// On utilise `pq.QuoteIdentifier` pour se protéger contre les injections SQL sur le nom de la DB.
	_, err = mdb.Exec(fmt.Sprintf("CREATE DATABASE %s", pq.QuoteIdentifier(dbName)))
	if err != nil {
		// Ignorer l'erreur si la base de données existe déjà (peut arriver dans des scénarios de concurrence)
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "42P04" { // 42P04 = duplicate_database
			return nil
		}
		return fmt.Errorf("could not execute create database command: %w", err)
	}
	return nil
}

func defaultDBUser() string {
	if userName := strings.TrimSpace(os.Getenv("USER")); userName != "" {
		return userName
	}

	currentUser, err := user.Current()
	if err == nil && currentUser.Username != "" {
		return currentUser.Username
	}

	return "postgres"
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}

func redactedConnStr(connStr string) string {
	parts := strings.Fields(connStr)
	for i, part := range parts {
		if strings.HasPrefix(part, "password=") {
			parts[i] = "password=REDACTED"
		}
	}

	return strings.Join(parts, " ")
}

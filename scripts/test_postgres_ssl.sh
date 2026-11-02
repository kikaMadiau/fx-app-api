#!/bin/bash
# Script pour tester la configuration SSL PostgreSQL
# Usage: ./scripts/test_postgres_ssl.sh [sslmode]

set -e

SSL_MODE="${1:-prefer}"
ENV_FILE="${2:-.env}"

echo "Test de configuration SSL PostgreSQL"
echo "======================================="
echo "Mode SSL: $SSL_MODE"
echo "Fichier .env: $ENV_FILE"
echo ""

# Charger les variables d'environnement
if [ -f "$ENV_FILE" ]; then
    echo " Chargement des variables depuis $ENV_FILE..."
    export $(grep -v '^#' "$ENV_FILE" | xargs)
else
    echo "Fichier $ENV_FILE non trouvé, utilisation des variables d'environnement actuelles"
fi

# Surcharger DB_SSLMODE si spécifié
if [ "$SSL_MODE" != "prefer" ]; then
    export DB_SSLMODE="$SSL_MODE"
    echo "DB_SSLMODE défini à: $SSL_MODE"
fi

# Afficher la configuration
echo ""
echo "Configuration actuelle:"
echo "-------------------------"
echo "DB_HOST: ${DB_HOST:-localhost}"
echo "DB_PORT: ${DB_PORT:-5432}"
echo "DB_USER: ${DB_USER:-postgres}"
echo "DB_NAME: ${DB_NAME:-fx_drc_db}"
echo "DB_SSLMODE: ${DB_SSLMODE:-prefer}"
echo "DB_PASSWORD: ${DB_PASSWORD:+(défini)}"
echo ""

# Vérifier la sécurité
echo "Vérification de sécurité..."
echo "-----------------------------"

# Vérifier le mode SSL
case "${DB_SSLMODE:-prefer}" in
    "disable"|"allow")
        echo "DANGER: DB_SSLMODE=${DB_SSLMODE} est INSÉCURE!"
        echo "   Les données sont transmises en clair sur le réseau."
        echo "   Recommandation: Utiliser 'prefer', 'require', ou 'verify-full'"
        ;;
    "prefer")
        echo "AVERTISSEMENT: DB_SSLMODE=prefer"
        echo "   Acceptable pour le développement, mais pas optimal pour la production."
        ;;
    "require"|"verify-ca"|"verify-full")
        echo "DB_SSLMODE=${DB_SSLMODE} est SÉCURISÉ"
        echo "   Les connexions sont chiffrées avec SSL/TLS."
        ;;
    *)
        echo "❓ DB_SSLMODE=${DB_SSLMODE} - Mode inconnu"
        ;;
esac

# Vérifier si c'est un environnement de production
echo ""
echo "Détection d'environnement..."
echo "-------------------------------"

IS_PROD="false"
if [ "$APP_ENV" = "production" ] || [ "$APP_ENV" = "prod" ] || [ "$APP_ENV" = "prd" ]; then
    IS_PROD="true"
    echo "Environnement de production détecté (APP_ENV=$APP_ENV)"
elif [ "$DB_HOST" != "localhost" ] && [ "$DB_HOST" != "127.0.0.1" ] && [ ! -z "$DB_HOST" ]; then
    IS_PROD="true"
    echo "Base de données distante détectée (DB_HOST=$DB_HOST)"
else
    echo "Environnement de développement détecté"
fi

# Recommandations spécifiques
echo ""
echo "Recommandations:"
echo "------------------"
if [ "$IS_PROD" = "true" ]; then
    if [ "${DB_SSLMODE:-prefer}" = "disable" ] || [ "${DB_SSLMODE:-prefer}" = "allow" ]; then
        echo "CRITIQUE: Changer DB_SSLMODE en 'require' ou 'verify-full' IMMÉDIATEMENT"
    elif [ "${DB_SSLMODE:-prefer}" = "prefer" ]; then
        echo "Recommandé: Changer DB_SSLMODE en 'require' pour la production"
    else
        echo "Configuration SSL appropriée pour la production"
    fi
    
    if [ -z "$DB_PASSWORD" ]; then
        echo "CRITIQUE: DB_PASSWORD est vide en production!"
    fi
else
    if [ "${DB_SSLMODE:-prefer}" = "disable" ]; then
        echo "Recommandé: Utiliser DB_SSLMODE=prefer pour plus de sécurité"
    fi
fi

# Tester la connexion PostgreSQL
echo ""
echo "Test de connexion PostgreSQL..."
echo "---------------------------------"

# Construire la chaîne de connexion
CONN_STR="host=${DB_HOST:-localhost} port=${DB_PORT:-5432} user=${DB_USER:-postgres} dbname=${DB_NAME:-fx_drc_db} sslmode=${DB_SSLMODE:-prefer}"
if [ ! -z "$DB_PASSWORD" ]; then
    CONN_STR="$CONN_STR password=$DB_PASSWORD"
fi

echo "Chaîne de connexion (sans mot de passe):"
echo "$CONN_STR" | sed 's/password=[^ ]*/password=***/g'
echo ""

# Essayer de se connecter avec psql si disponible
if command -v psql &> /dev/null; then
    echo "Tentative de connexion avec psql..."
    if timeout 5 psql "$CONN_STR" -c "\conninfo" 2>&1; then
        echo "Connexion PostgreSQL réussie!"
        
        # Vérifier les informations SSL
        echo ""
        echo "Informations SSL de la connexion:"
        psql "$CONN_STR" -c "SELECT version();" 2>/dev/null
        echo ""
        psql "$CONN_STR" -c "SHOW ssl;" 2>/dev/null || true
    else
        echo "Échec de la connexion PostgreSQL"
        echo "   Vérifiez:"
        echo "   1. PostgreSQL est en cours d'exécution"
        echo "   2. Les identifiants sont corrects"
        echo "   3. La configuration SSL est compatible"
    fi
else
    echo "psql non disponible, test de connexion impossible"
fi

# Tester avec l'application Go
echo ""
echo "Test avec l'application Go..."
echo "--------------------------------"

cd "$(dirname "$0")/.."

# Compiler et tester
if go build -o /tmp/test_ssl ./cmd 2>&1; then
    echo "Application compilée avec succès"
    
    # Exécuter avec vérification de sécurité
    echo ""
    echo "Démarrage de l'application avec vérification de sécurité..."
    timeout 10 /tmp/test_ssl 2>&1 | grep -A5 -B5 -i "ssl\|security\|warning\|error" || true
else
    echo "Échec de la compilation"
    go build ./cmd 2>&1 | tail -20
fi

# Nettoyage
rm -f /tmp/test_ssl 2>/dev/null || true

echo ""
echo "======================================="
echo "Test terminé"
echo ""
echo "Documentation:"
echo "   - SECURITY.md"
echo "   - .env.example pour les configurations recommandées"
echo ""
echo "Pour changer le mode SSL:"
echo "   export DB_SSLMODE=require  # Pour bash"
echo "   ou éditer votre fichier .env"

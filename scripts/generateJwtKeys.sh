#!/bin/bash
# Script pour générer des clés JWT RSA sécurisées
# Usage: ./scripts/generate_jwt_keys.sh [output_dir]

set -e

OUTPUT_DIR="${1:-./jwt_keys}"
mkdir -p "$OUTPUT_DIR"

echo " Génération de clés JWT RSA 4096 bits sécurisées..."
echo " Répertoire de sortie: $OUTPUT_DIR"

# Générer la clé privée RSA 4096 bits
openssl genrsa -out "$OUTPUT_DIR/private.pem" 4096
echo " Clé privée générée: $OUTPUT_DIR/private.pem"

# Générer la clé publique correspondante
openssl rsa -in "$OUTPUT_DIR/private.pem" -pubout -out "$OUTPUT_DIR/public.pem"
echo " Clé publique générée: $OUTPUT_DIR/public.pem"

# Générer les versions base64 pour les variables d'environnement
PRIVATE_KEY_B64=$(cat "$OUTPUT_DIR/private.pem" | base64 | tr -d '\n')
PUBLIC_KEY_B64=$(cat "$OUTPUT_DIR/public.pem" | base64 | tr -d '\n')

echo ""
echo "  Configuration pour variables d'environnement:"
echo "================================================"
echo ""
echo "# Ajouter à votre fichier .env ou configuration de déploiement:"
echo "JWT_PRIVATE_KEY='$PRIVATE_KEY_B64'"
echo "JWT_PUBLIC_KEY='$PUBLIC_KEY_B64'"
echo ""
echo "OU"
echo ""
echo "# Pour utiliser des fichiers (moins sécurisé):"
echo "JWT_PRIVATE_KEY_PATH='$OUTPUT_DIR/private.pem'"
echo "JWT_PUBLIC_KEY_PATH='$OUTPUT_DIR/public.pem'"
echo ""
echo "  IMPORTANT:"
echo "   - NE JAMAIS commiter les clés dans le dépôt Git"
echo "   - Utiliser un gestionnaire de secrets en production"
echo "   - Roter les clés régulièrement (tous les 90 jours recommandé)"
echo "   - Stocker les clés privées de manière sécurisée"

# Vérifier que les clés sont valides
echo ""
echo " Vérification des clés générées..."
openssl rsa -in "$OUTPUT_DIR/private.pem" -check -noout
echo " Clé privée valide"

# Donner des permissions sécurisées
chmod 600 "$OUTPUT_DIR/private.pem"
chmod 644 "$OUTPUT_DIR/public.pem"
echo " Permissions ajustées"

echo ""
echo "   Génération terminée avec succès!"
echo "   Les clés sont prêtes pour le déploiement."
#!/bin/bash
# Script de gestion des clés JWT avec rotation automatique
# Usage: ./scripts/manage_jwt_keys.sh [commande]

set -e

COMMAND="${1:-status}"
BACKUP_DIR="${BACKUP_DIR:-./jwt_backups}"
CONFIG_FILE="${CONFIG_FILE:-.env}"

echo " Gestion des clés JWT - fx-app-api"
echo "====================================="

# Charger la configuration
load_config() {
    if [ -f "$CONFIG_FILE" ]; then
        source "$CONFIG_FILE" 2>/dev/null || true
    fi
}

# Créer un backup des clés
backup_keys() {
    local timestamp=$(date +%Y%m%d_%H%M%S)
    local backup_path="$BACKUP_DIR/$timestamp"
    
    mkdir -p "$backup_path"
    
    echo " Création d'un backup des clés dans $backup_path..."
    
    # Backuper les clés actuelles si elles existent
    if [ -f "jwt/private.pem" ]; then
        cp "jwt/private.pem" "$backup_path/private.pem"
        echo "  private.pem sauvegardé"
    fi
    
    if [ -f "jwt/public.pem" ]; then
        cp "jwt/public.pem" "$backup_path/public.pem"
        echo "  public.pem sauvegardé"
    fi
    
    # Backuper la configuration
    if [ -f "$CONFIG_FILE" ]; then
        cp "$CONFIG_FILE" "$backup_path/env.backup"
        echo "$CONFIG_FILE sauvegardé"
    fi
    
    echo "Backup terminé: $backup_path"
}

# Générer de nouvelles clés
generate_keys() {
    local output_dir="${1:-./jwt}"
    
    echo "Génération de nouvelles clés JWT..."
    
    # Créer le dossier si nécessaire
    mkdir -p "$output_dir"
    
    # Générer la clé privée RSA 4096 bits
    openssl genrsa -out "$output_dir/private.pem" 4096
    echo "Clé privée générée: $output_dir/private.pem"
    
    # Générer la clé publique correspondante
    openssl rsa -in "$output_dir/private.pem" -pubout -out "$output_dir/public.pem"
    echo "Clé publique générée: $output_dir/public.pem"
    
    # Ajuster les permissions
    chmod 600 "$output_dir/private.pem"
    chmod 644 "$output_dir/public.pem"
    
    # Générer les versions base64
    local private_b64=$(cat "$output_dir/private.pem" | base64 | tr -d '\n')
    local public_b64=$(cat "$output_dir/public.pem" | base64 | tr -d '\n')
    
    echo ""
    echo "Configuration pour variables d'environnement:"
    echo "JWT_PRIVATE_KEY='$private_b64'"
    echo "JWT_PUBLIC_KEY='$public_b64'"
    
    # Mettre à jour le fichier .env si demandé
    if [ -f "$CONFIG_FILE" ]; then
        echo ""
        read -p "Mettre à jour $CONFIG_FILE avec les nouvelles clés? (y/N): " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            # Mettre à jour les variables JWT dans .env
            sed -i '' "/^JWT_PRIVATE_KEY=/d" "$CONFIG_FILE"
            sed -i '' "/^JWT_PUBLIC_KEY=/d" "$CONFIG_FILE"
            
            echo "JWT_PRIVATE_KEY='$private_b64'" >> "$CONFIG_FILE"
            echo "JWT_PUBLIC_KEY='$public_b64'" >> "$CONFIG_FILE"
            
            echo "$CONFIG_FILE mis à jour avec les nouvelles clés"
        fi
    fi
    
    echo ""
    echo "Génération terminée!"
}

# Effectuer une rotation des clés
rotate_keys() {
    echo "Rotation des clés JWT..."
    
    # Backuper les clés actuelles
    backup_keys
    
    # Générer de nouvelles clés
    generate_keys "./jwt_new_$(date +%Y%m%d)"
    
    echo ""
    echo "Instructions pour la rotation:"
    echo "1. Les anciennes clés ont été sauvegardées dans $BACKUP_DIR"
    echo "2. De nouvelles clés ont été générées"
    echo "3. Mettre à jour votre configuration avec les nouvelles clés"
    echo "4. Redémarrer l'application"
    echo ""
    echo "Pendant la transition:"
    echo "   - Les tokens existants signés avec l'ancienne clé resteront valides"
    echo "   - Les nouveaux tokens seront signés avec la nouvelle clé"
    echo "   - La période de transition dure jusqu'à l'expiration des tokens (24h)"
}

# Afficher le statut des clés
show_status() {
    load_config
    
    echo "Statut des clés JWT:"
    echo "----------------------"
    
    # Vérifier les fichiers de clés
    if [ -f "jwt/private.pem" ]; then
        echo "Fichier private.pem présent"
        local key_size=$(openssl rsa -in jwt/private.pem -text -noout 2>/dev/null | grep "Private-Key:" | awk '{print $2}')
        echo "   Taille: $key_size bits"
    else
        echo "Fichier private.pem absent"
    fi
    
    if [ -f "jwt/public.pem" ]; then
        echo "Fichier public.pem présent"
    else
        echo "Fichier public.pem absent"
    fi
    
    # Vérifier les variables d'environnement
    if [ -n "$JWT_PRIVATE_KEY" ]; then
        echo "Variable JWT_PRIVATE_KEY définie"
        local key_length=${#JWT_PRIVATE_KEY}
        echo "   Longueur: $key_length caractères (base64)"
    else
        echo "Variable JWT_PRIVATE_KEY non définie"
    fi
    
    if [ -n "$JWT_PUBLIC_KEY" ]; then
        echo "Variable JWT_PUBLIC_KEY définie"
        local key_length=${#JWT_PUBLIC_KEY}
        echo "   Longueur: $key_length caractères (base64)"
    else
        echo "Variable JWT_PUBLIC_KEY non définie"
    fi
    
    # Vérifier les chemins de fichiers
    if [ -n "$JWT_PRIVATE_KEY_PATH" ]; then
        echo "Variable JWT_PRIVATE_KEY_PATH définie: $JWT_PRIVATE_KEY_PATH"
        if [ -f "$JWT_PRIVATE_KEY_PATH" ]; then
            echo "   Fichier présent"
        else
            echo "   Fichier absent"
        fi
    fi
    
    if [ -n "$JWT_PUBLIC_KEY_PATH" ]; then
        echo "Variable JWT_PUBLIC_KEY_PATH définie: $JWT_PUBLIC_KEY_PATH"
        if [ -f "$JWT_PUBLIC_KEY_PATH" ]; then
            echo "   Fichier présent"
        else
            echo " Fichier absent"
        fi
    fi
    
    # Recommandations
    echo ""
    echo " Recommandations:"
    if [ -z "$JWT_PRIVATE_KEY" ] && [ -z "$JWT_PUBLIC_KEY" ]; then
        echo "Utilisez des variables d'environnement (plus sécurisé que les fichiers)"
        echo "   Commande: ./scripts/manage_jwt_keys.sh generate"
    fi
    
    if [ -f "jwt/private.pem" ] && [ ! -f ".gitignore" ] || ! grep -q "jwt/" ".gitignore" 2>/dev/null; then
        echo "Assurez-vous que 'jwt/' est dans .gitignore"
    fi
}

# Valider les clés actuelles
validate_keys() {
    echo "Validation des clés JWT..."
    
    load_config
    
    local has_error=false
    
    # Valider les fichiers de clés
    if [ -f "jwt/private.pem" ]; then
        if openssl rsa -in jwt/private.pem -check -noout 2>/dev/null; then
            echo "Clé privée (fichier) valide"
        else
            echo "Clé privée (fichier) invalide"
            has_error=true
        fi
    fi
    
    if [ -f "jwt/public.pem" ]; then
        if openssl rsa -in jwt/public.pem -pubin -check -noout 2>/dev/null; then
            echo "Clé publique (fichier) valide"
        else
            echo "Clé publique (fichier) invalide"
            has_error=true
        fi
    fi
    
    # Valider les clés base64
    if [ -n "$JWT_PRIVATE_KEY" ]; then
        if echo "$JWT_PRIVATE_KEY" | base64 -d 2>/dev/null | openssl rsa -check -noout 2>/dev/null; then
            echo "Clé privée (env) valide"
        else
            echo "Clé privée (env) invalide"
            has_error=true
        fi
    fi
    
    if [ -n "$JWT_PUBLIC_KEY" ]; then
        if echo "$JWT_PUBLIC_KEY" | base64 -d 2>/dev/null | openssl rsa -pubin -check -noout 2>/dev/null; then
            echo "Clé publique (env) valide"
        else
            echo "Clé publique (env) invalide"
            has_error=true
        fi
    fi
    
    # Valider la correspondance des clés
    if [ -f "jwt/private.pem" ] && [ -f "jwt/public.pem" ]; then
        local priv_modulus=$(openssl rsa -in jwt/private.pem -modulus -noout 2>/dev/null)
        local pub_modulus=$(openssl rsa -in jwt/public.pem -pubin -modulus -noout 2>/dev/null)
        
        if [ "$priv_modulus" = "$pub_modulus" ]; then
            echo "Les clés privée/publique (fichiers) correspondent"
        else
            echo "Les clés privée/publique (fichiers) ne correspondent pas"
            has_error=true
        fi
    fi
    
    if [ -n "$JWT_PRIVATE_KEY" ] && [ -n "$JWT_PUBLIC_KEY" ]; then
        local priv_modulus=$(echo "$JWT_PRIVATE_KEY" | base64 -d 2>/dev/null | openssl rsa -modulus -noout 2>/dev/null)
        local pub_modulus=$(echo "$JWT_PUBLIC_KEY" | base64 -d 2>/dev/null | openssl rsa -pubin -modulus -noout 2>/dev/null)
        
        if [ "$priv_modulus" = "$pub_modulus" ]; then
            echo "Les clés privée/publique (env) correspondent"
        else
            echo "Les clés privée/publique (env) ne correspondent pas"
            has_error=true
        fi
    fi
    
    if [ "$has_error" = true ]; then
        echo ""
        echo "Des problèmes ont été détectés avec les clés JWT"
        echo "   Utilisez: ./scripts/manage_jwt_keys.sh generate"
        return 1
    else
        echo ""
        echo "Toutes les validations ont réussi"
        return 0
    fi
}

# Nettoyer les anciens backups
cleanup_backups() {
    local days_to_keep=${1:-30}
    
    echo "🧹 Nettoyage des backups de plus de $days_to_keep jours..."
    
    if [ ! -d "$BACKUP_DIR" ]; then
        echo "Dossier de backup $BACKUP_DIR non trouvé"
        return 1
    fi
    
    find "$BACKUP_DIR" -type d -name "*" -mtime +$days_to_keep -exec echo "  Suppression: {}" \; -exec rm -rf {} \;
    
    echo "Nettoyage terminé"
}

# Afficher l'aide
show_help() {
    echo "Usage: $0 [commande]"
    echo ""
    echo "Commandes:"
    echo "  status     Afficher le statut des clés JWT (défaut)"
    echo "  validate   Valider les clés actuelles"
    echo "  generate   Générer de nouvelles clés JWT"
    echo "  rotate     Effectuer une rotation des clés (backup + génération)"
    echo "  backup     Créer un backup des clés actuelles"
    echo "  clean      Nettoyer les anciens backups (>30 jours)"
    echo "  help       Afficher cette aide"
    echo ""
    echo "Variables d'environnement:"
    echo "  BACKUP_DIR    Dossier pour les backups (défaut: ./jwt_backups)"
    echo "  CONFIG_FILE   Fichier de configuration (défaut: .env)"
    echo ""
    echo "Exemples:"
    echo "  $0 status"
    echo "  $0 generate"
    echo "  BACKUP_DIR=/backups $0 rotate"
}

# Exécuter la commande
case "$COMMAND" in
    status)
        show_status
        ;;
    validate)
        validate_keys
        ;;
    generate)
        generate_keys "./jwt"
        ;;
    rotate)
        rotate_keys
        ;;
    backup)
        backup_keys
        ;;
    clean)
        cleanup_backups 30
        ;;
    help|--help|-h)
        show_help
        ;;
    *)
        echo "Commande inconnue: $COMMAND"
        echo ""
        show_help
        exit 1
        ;;
esac

echo ""
echo "Documentation:"
echo "   - SECURITY.md pour les bonnes pratiques"
echo "   - .env.example pour les configurations recommandées"
echo ""
echo "Rappel de sécurité:"
echo "   - NE JAMAIS commiter les clés JWT dans Git"
echo "   - Utiliser des variables d'environnement en production"
echo "   - Roter les clés régulièrement (tous les 90 jours recommandé)"

# Instructions de Configuration de Sécurité

## 🚀 Démarrage Rapide

### Étape 1: Initialisation du Projet
```bash
# Cloner le dépôt (si ce n'est pas déjà fait)
git clone <votre-repo>
cd fx-app-api

# Nettoyer les anciens fichiers sensibles
rm -rf .gocache jwt/ bin/

# Initialiser les dépendances
go mod tidy
```

### Étape 2: Générer les Clés JWT
```bash
# Générer de nouvelles clés JWT sécurisées
./scripts/generate_jwt_keys.sh

# Cela affichera les clés base64 pour votre .env
# Copiez les lignes JWT_PRIVATE_KEY et JWT_PUBLIC_KEY
```

### Étape 3: Configurer l'Environnement
```bash
# Créer le fichier .env
cp .env.example .env

# Éditer .env avec vos configurations:
# 1. Coller les clés JWT générées
# 2. Configurer la base de données
# 3. Définir APP_ENV=development
```

### Étape 4: Tester la Configuration
```bash
# Tester les clés JWT
./scripts/manage_jwt_keys.sh validate

# Tester la sécurité SSL PostgreSQL
./scripts/test_postgres_ssl.sh

# Compiler et tester l'application
go build ./cmd
go run cmd/main.go
```

## 🔧 Scripts de Sécurité Disponibles

### Gestion des Clés JWT
```bash
# Afficher le statut
./scripts/manage_jwt_keys.sh status

# Générer de nouvelles clés
./scripts/generate_jwt_keys.sh

# Valider les clés actuelles
./scripts/manage_jwt_keys.sh validate

# Effectuer une rotation (backup + nouvelles clés)
./scripts/manage_jwt_keys.sh rotate

# Créer un backup
./scripts/manage_jwt_keys.sh backup
```

### Sécurité PostgreSQL SSL
```bash
# Tester la configuration actuelle
./scripts/test_postgres_ssl.sh

# Tester avec différents modes SSL
./scripts/test_postgres_ssl.sh prefer
./scripts/test_postgres_ssl.sh require
./scripts/test_postgres_ssl.sh verify-full
```

## 🛡️ Configuration de Production

### Variables d'Environnement Critiques
```bash
# Environnement
APP_ENV=production

# Sécurité JWT (base64 recommandé)
JWT_PRIVATE_KEY="base64_encoded_private_key"
JWT_PUBLIC_KEY="base64_encoded_public_key"

# Sécurité PostgreSQL SSL
DB_SSLMODE=verify-full  # Validation complète des certificats
DB_PASSWORD="votre_mot_de_passe_complexe"

# Monitoring
LOG_LEVEL=info
```

### Configuration Docker
```dockerfile
# Dockerfile
FROM golang:1.22-alpine

# Variables d'environnement de sécurité
ENV APP_ENV=production \
    DB_SSLMODE=require \
    JWT_KEY_ROTATION_INTERVAL=90d

# ... reste de la configuration
```

### Configuration Kubernetes
```yaml
# secret-jwt.yaml
apiVersion: v1
kind: Secret
metadata:
  name: jwt-keys
type: Opaque
data:
  private_key: <base64_encoded_private_key>
  public_key: <base64_encoded_public_key>

# deployment.yaml
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
      - name: app
        env:
        - name: JWT_PRIVATE_KEY
          valueFrom:
            secretKeyRef:
              name: jwt-keys
              key: private_key
        - name: JWT_PUBLIC_KEY
          valueFrom:
            secretKeyRef:
              name: jwt-keys
              key: public_key
        - name: DB_SSLMODE
          value: "verify-full"
```

## 🧪 Tests de Sécurité

### Tests Unitaires
```bash
# Lancer tous les tests
go test ./...

# Tests spécifiques de sécurité
go test ./internal/utils/validation/...
go test ./internal/storage/...
go test ./internal/utils/services/...
```

### Tests d'Intégration
```bash
# Tester la validation des entrées
go test -v ./internal/api/validation/...

# Tester la rotation des clés
go test -v ./internal/utils/services/... -run TestKeyRotation
```

### Tests Manuels
```bash
# 1. Tester la validation des requêtes
curl -X POST http://localhost:8080/auth/trader/login \
  -H "Content-Type: application/json" \
  -d '{"email":"invalid-email","password":"short"}'
# Devrait retourner des erreurs de validation

# 2. Vérifier les en-têtes de sécurité
curl -I http://localhost:8080/health
# Devrait inclure des en-têtes de sécurité
```

## 📚 Documentation

### Guides Complets
1. **`SECURITY_JWT_GUIDE.md`** - Tout sur les clés JWT
2. **`POSTGRES_SSL_SECURITY_GUIDE.md`** - SSL PostgreSQL
3. **`ALL_SECURITY_FIXES_SUMMARY.md`** - Résumé complet

### Fichiers de Configuration
1. **`.env.example`** - Template avec bonnes pratiques
2. **`.gitignore`** - Exclusions de sécurité

### Structures de Code
1. **`internal/utils/validation/`** - Validation des entrées
2. **`internal/utils/services/`** - Services de sécurité
3. **`internal/api/validation/`** - Validation API

## 🔄 Maintenance Continue

### Rotation Régulière des Clés
```bash
# Planifier la rotation tous les 90 jours
# Via cron ou outil d'orchestration

# Rotation manuelle
./scripts/manage_jwt_keys.sh rotate

# Vérifier l'état
./scripts/manage_jwt_keys.sh status
```

### Mises à Jour de Sécurité
```bash
# Mettre à jour les dépendances
go get -u ./...
go mod tidy

# Vérifier les vulnérabilités
go list -m all | grep -E "(vulnerability|CVE)"

# Mettre à jour Go
go mod edit -go=1.22
```

### Surveillance
```bash
# Vérifier les logs de sécurité
grep -i "security\|warning\|error\|ssl\|jwt" logs/app.log

# Monitorer les tentatives d'authentification
tail -f logs/auth.log | grep -E "(failed|success|attempt)"
```

## 🚨 Procédures d'Urgence

### En Cas de Compromission des Clés JWT
```bash
# 1. IMMÉDIAT: Révoquer les clés
./scripts/manage_jwt_keys.sh rotate

# 2. Notifier les utilisateurs
# Tous devront se reconnecter

# 3. Analyser les logs
grep -i "jwt\|token\|auth" logs/*.log

# 4. Mettre à jour tous les environnements
```

### En Cas de Problème SSL PostgreSQL
```bash
# 1. Basculer en mode dégradé
export DB_SSLMODE=prefer  # Au lieu de require

# 2. Diagnostiquer
./scripts/test_postgres_ssl.sh

# 3. Vérifier les certificats
openssl verify -CAfile ca.crt server.crt

# 4. Rétablir la sécurité
export DB_SSLMODE=verify-full
```

## 📊 Métriques de Sécurité

### À Surveiller
1. **Taux d'échec d'authentification** - Détection d'attaques par force brute
2. **Rotation des clés JWT** - Dernière rotation et prochaine due
3. **Connexions SSL** - Pourcentage de connexions chiffrées
4. **Erreurs de validation** - Tentatives d'injection ou données malformées

### Alertes à Configurer
- Rotation des clés JWT > 90 jours
- Taux d'échec auth > 10% sur 5 minutes
- Connexions non-SSL en production
- Erreurs de validation SQL

## 🤝 Support

### Pour les Développeurs
```bash
# Questions sur la validation
cat internal/utils/validation/validator.go | head -100

# Questions sur les clés JWT
./scripts/manage_jwt_keys.sh help

# Questions sur SSL
./scripts/test_postgres_ssl.sh --help
```

### Pour l'Équipe Ops
```bash
# Déploiement sécurisé
cat SECURITY_SETUP_INSTRUCTIONS.md

# Monitoring
cat ALL_SECURITY_FIXES_SUMMARY.md | grep -A5 "Monitoring"
```

### En Cas de Problème
1. Consulter la documentation correspondante
2. Exécuter les scripts de diagnostic
3. Vérifier les fichiers de log
4. Contacter l'équipe sécurité

---

## ✅ Checklist de Déploiement

### Pré-déploiement
- [ ] Clés JWT générées et validées
- [ ] SSL PostgreSQL configuré et testé
- [ ] Variables d'environnement de sécurité définies
- [ ] Tests de sécurité exécutés
- [ ] Backup des clés actuelles

### Post-déploiement
- [ ] Application démarre avec vérifications
- [ ] Tokens JWT fonctionnels
- [ ] Connexions SSL actives
- [ ] Validation des entrées opérationnelle
- [ ] Logs de sécurité accessibles

### Maintenance
- [ ] Rotation des clés planifiée
- [ ] Surveillance configurée
- [ ] Documentation à jour
- [ ] Équipe formée

---

**Dernière Vérification:** $(date +%Y-%m-%d)  
**Prochaine Vérification:** $(date -v+30d +%Y-%m-%d)  
**Contact Sécurité:** devops@example.com
# Guide de Sécurité JWT - fx-app-api

## Problème Résolu
**Les clés JWT étaient exposées dans le dépôt Git** (`jwt/private.pem` et `jwt/public.pem`).  
Un attaquant avec accès au dépôt pouvait générer des tokens JWT valides et usurper l'identité de n'importe quel utilisateur.

## Solution Implémentée

### 1. Nouvelles Fonctionnalités de Sécurité

#### **Système Multi-Sources pour les Clés JWT**
Le code maintenant supporte trois méthodes pour charger les clés JWT, par ordre de priorité:

1. **Variables d'environnement (Recommandé pour la production)**
   ```bash
   JWT_PRIVATE_KEY="base64_encoded_private_key"
   JWT_PUBLIC_KEY="base64_encoded_public_key"
   ```

2. **Fichiers spécifiés par variables d'environnement**
   ```bash
   JWT_PRIVATE_KEY_PATH="/chemin/securise/private.pem"
   JWT_PUBLIC_KEY_PATH="/chemin/securise/public.pem"
   ```

3. **Emplacements par défaut (Déconseillé en production)**
   ```bash
   jwt/private.pem
   jwt/public.pem
   ```

#### **Vérification au Démarrage**
L'application vérifie maintenant la configuration des clés JWT au démarrage et affiche des avertissements si la configuration n'est pas optimale.

### 2. Script de Génération Sécurisée

Un script utilitaire a été créé pour générer des clés JWT sécurisées:
```bash
./scripts/generate_jwt_keys.sh
```

Ce script génère:
- Une paire de clés RSA 4096 bits
- Les versions base64 pour les variables d'environnement
- Des instructions de déploiement sécurisé

## Procédures de Déploiement

### Pour le Développement Local

1. **Générer de nouvelles clés de développement:**
   ```bash
   ./scripts/generate_jwt_keys.sh ./temp_jwt_keys
   ```

2. **Configurer votre environnement:**
   ```bash
   # Option 1: Variables d'environnement (recommandé)
   export JWT_PRIVATE_KEY="$(cat ./temp_jwt_keys/private.pem | base64)"
   export JWT_PUBLIC_KEY="$(cat ./temp_jwt_keys/public.pem | base64)"
   
   # Option 2: Fichiers locaux
   export JWT_PRIVATE_KEY_PATH="./temp_jwt_keys/private.pem"
   export JWT_PUBLIC_KEY_PATH="./temp_jwt_keys/public.pem"
   ```

3. **Ajouter au .gitignore:**
   ```bash
   # Ajouter à .gitignore
   temp_jwt_keys/
   *.pem
   ```

### Pour la Production

#### **Option A: Variables d'Environnement (Recommandé)**
1. Générer des clés de production:
   ```bash
   ./scripts/generate_jwt_keys.sh /secure/jwt/production
   ```

2. Configurer dans votre plateforme de déploiement:
   ```yaml
   # Docker Compose
   environment:
     JWT_PRIVATE_KEY: "${JWT_PRIVATE_KEY}"
     JWT_PUBLIC_KEY: "${JWT_PUBLIC_KEY}"
   
   # Kubernetes Secret
   apiVersion: v1
   kind: Secret
   metadata:
     name: jwt-keys
   type: Opaque
   data:
     private_key: <base64_encoded_private_key>
     public_key: <base64_encoded_public_key>
   ```

#### **Option B: Gestionnaire de Secrets**
Pour AWS, Azure, ou GCP, utiliser leurs services de secrets management:

**AWS Secrets Manager:**
```bash
aws secretsmanager create-secret \
  --name production/jwt-keys \
  --secret-string '{
    "private_key": "base64_encoded_private_key",
    "public_key": "base64_encoded_public_key"
  }'
```

#### **Option C: Mount de Volumes**
Monter les fichiers de clés depuis un volume sécurisé:
```yaml
# Docker
volumes:
  - /etc/secrets/jwt:/app/jwt:ro
```

## Rotation des Clés

### Procédure de Rotation
1. **Générer une nouvelle paire de clés:**
   ```bash
   ./scripts/generate_jwt_keys.sh ./new_keys_$(date +%Y%m%d)
   ```

2. **Déployer les nouvelles clés:**
   - Mettre à jour les secrets/variables d'environnement
   - Redémarrer l'application

3. **Gestion des Tokens Existants:**
   - Les tokens signés avec l'ancienne clé seront rejetés
   - Les utilisateurs devront se reconnecter pour obtenir un nouveau token

### Planning de Rotation Recommandé
- **Production:** Tous les 90 jours
- **Staging:** Tous les 180 jours  
- **Développement:** Au besoin

## Dépannage

### Erreurs Courantes
```bash
# Erreur: "JWT keys not properly configured"
# Solution: Vérifier que les variables d'environnement sont définies
echo $JWT_PRIVATE_KEY | head -c 50
echo $JWT_PUBLIC_KEY | head -c 50

# Erreur: "failed to parse private key"
# Solution: Vérifier le format de la clé
echo $JWT_PRIVATE_KEY | base64 -d | head -5
```

### Vérification de la Configuration
```bash
# Tester la configuration des clés
cd /Users/kika/fx-app-api
go run cmd/main.go 2>&1 | grep -A2 -B2 "JWT"
```

## Bonnes Pratiques

### ✅ À FAIRE
- Utiliser des clés RSA 4096 bits minimum
- Stocker les clés dans un gestionnaire de secrets
- Roter les clés régulièrement
- Utiliser HTTPS en production
- Loguer les tentatives d'authentification échouées

### ❌ À ÉVITER
- Commiter des clés dans le dépôt Git
- Utiliser des clés de production en développement
- Partager les clés entre environnements
- Utiliser des clés expirées ou compromises

## Audit de Sécurité

### Vérifications à Effectuer
1. [ ] Les anciennes clés ont été supprimées du dépôt Git
2. [ ] .gitignore exclut les fichiers .pem
3. [ ] Les nouvelles clés sont générées avec RSA 4096
4. [ ] Les secrets sont stockés de manière sécurisée
5. [ ] Un plan de rotation est en place

### Commandes de Vérification
```bash
# Vérifier que les anciennes clés sont supprimées
git log --all --full-history -- "**/*.pem"

# Vérifier .gitignore
grep -i "pem\|jwt" .gitignore

# Tester la génération de token
cd /Users/kika/fx-app-api
go test ./internal/utils/services/... -v
```

## Support

Pour toute question sur la sécurité JWT:
1. Consulter ce guide
2. Exécuter les scripts de génération
3. Contacter l'équipe de sécurité

---

**Dernière Mise à Jour:** $(date +%Y-%m-%d)  
**Responsable Sécurité:** Équipe DevOps
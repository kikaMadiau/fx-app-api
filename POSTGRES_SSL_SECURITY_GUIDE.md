# Guide de Sécurité PostgreSQL SSL - fx-app-api

## Problème Résolu
**Faille de Sécurité:** Connexions PostgreSQL non chiffrées
- `DB_SSLMODE=disable` dans la configuration par défaut
- Données sensibles transmises en clair sur le réseau
- Risque: Interception des identifiants et données financières

## Solution Implémentée

### 1. Configuration SSL Améliorée

#### **Valeurs par Défaut Intelligentes**
- **Développement:** `DB_SSLMODE=prefer` (essaie SSL d'abord)
- **Production détectée:** `DB_SSLMODE=require` (exige SSL)

#### **Détection Automatique de l'Environnement**
L'application détecte automatiquement si elle s'exécute en production:
- Variable `APP_ENV=production`
- Hébergement cloud (Heroku, AWS, GCP, Azure)
- Base de données distante (non localhost)

#### **Avertissements de Sécurité**
- Avertissements pour `DB_SSLMODE=disable` ou `allow` en production
- Recommandation pour `verify-full` en production

### 2. Modes SSL PostgreSQL

#### **Options Disponibles (du moins au plus sécurisé)**

| Mode | Description | Sécurité | Usage |
|------|-------------|----------|-------|
| **disable** | Pas de SSL | ❌ DANGEREUX | Jamais en production |
| **allow** | SSL optionnel | ⚠️ RISQUÉ | Déconseillé |
| **prefer** | SSL si disponible | ✅ BON | Développement local |
| **require** | SSL requis | ✅ TRÈS BON | Production |
| **verify-ca** | SSL + validation CA | ✅ EXCELLENT | Production |
| **verify-full** | SSL + validation complète | ✅ OPTIMAL | Production |

### 3. Vérification de Sécurité

Une nouvelle fonction `CheckDatabaseSecurity()` vérifie:
- Mode SSL approprié pour l'environnement
- Présence du mot de passe de la base de données
- Configuration générale de sécurité

## Procédures de Configuration

### Pour le Développement Local

#### **Avec PostgreSQL Local**
```bash
# .env pour développement
DB_SSLMODE=prefer  # Bon compromis sécurité/performance
```

#### **Avec Docker**
```dockerfile
# docker-compose.yml
environment:
  DB_SSLMODE: "prefer"
```

### Pour la Production

#### **Option A: SSL Requis (Minimum)**
```bash
# .env.production
DB_SSLMODE=require
```

#### **Option B: Validation Complète (Recommandé)**
```bash
# .env.production  
DB_SSLMODE=verify-full
# Nécessite la configuration des certificats côté serveur
```

#### **Option C: Plateformes Cloud**
```bash
# Heroku, AWS RDS, Google Cloud SQL fournissent généralement SSL par défaut
# Utiliser leur configuration recommandée
```

## Migration depuis l'Ancienne Configuration

### Étape 1: Mettre à jour les Variables d'Environnement
```bash
# Ancien (INSÉCURE)
DB_SSLMODE=disable

# Nouveau (SÉCURISÉ)
DB_SSLMODE=prefer    # Développement
DB_SSLMODE=require   # Production
```

### Étape 2: Configurer PostgreSQL pour SSL

#### **Sur le Serveur PostgreSQL:**
```bash
# Éditer postgresql.conf
ssl = on
ssl_cert_file = 'server.crt'
ssl_key_file = 'server.key'
ssl_ca_file = 'root.crt'

# Redémarrer PostgreSQL
sudo systemctl restart postgresql
```

#### **Générer des Certificats (Development):**
```bash
# Générer un certificat auto-signé pour le développement
openssl req -new -x509 -days 365 -nodes -text \
  -out server.crt \
  -keyout server.key \
  -subj "/CN=localhost"

chmod 600 server.key
```

### Étape 3: Tester la Connexion SSL

#### **Test de Connexion:**
```bash
# Tester avec psql
psql "host=localhost dbname=fx_drc_db user=postgres sslmode=require"

# Vérifier la connexion SSL
\conninfo
```

#### **Vérifier dans l'Application:**
```bash
# Démarrer l'application
go run cmd/main.go

# Vérifier les logs pour les avertissements SSL
```

## Dépannage

### Erreurs Courantes

#### **"SSL is not enabled on the server"**
```bash
# Solution: Activer SSL dans PostgreSQL
# postgresql.conf:
# ssl = on
```

#### **"certificate verify failed"**
```bash
# Solution pour le développement:
DB_SSLMODE=prefer  # Au lieu de verify-full

# Solution pour la production:
# Configurer les certificats correctement
```

#### **"connection requires a valid client certificate"**
```bash
# Solution: Configurer l'authentification client SSL
# ou utiliser un mode moins strict
DB_SSLMODE=require  # Au lieu de verify-full
```

### Commandes de Diagnostic
```bash
# Vérifier la configuration SSL actuelle
echo $DB_SSLMODE

# Tester la connexion avec différents modes
psql "sslmode=disable"  # TEST SEULEMENT - INSÉCURE
psql "sslmode=prefer"   # Recommandé pour dev
psql "sslmode=require"  # Pour production

# Vérifier les logs de l'application
go run cmd/main.go 2>&1 | grep -i ssl
```

## Bonnes Pratiques

### ✅ À FAIRE
- Utiliser `prefer` ou `require` en développement
- Toujours utiliser `require` ou `verify-full` en production
- Valider les certificats en production (`verify-full`)
- Stocker les certificats de manière sécurisée
- Roter les certificats régulièrement

### ❌ À ÉVITER
- `DB_SSLMODE=disable` en production (CRITIQUE)
- `DB_SSLMODE=allow` (trop permissif)
- Certificats auto-signés en production
- Partager les clés privées des certificats

### 🔧 Configuration Recommandée par Environnement

| Environnement | DB_SSLMODE | Certificats | Notes |
|---------------|------------|-------------|-------|
| **Dev Local** | `prefer` | Auto-signés | Acceptable pour développement |
| **Staging** | `require` | Valides | Pré-production |
| **Production** | `verify-full` | Signés CA | Validation complète |
| **Cloud** | Selon fournisseur | Gérés | Suivre la documentation du fournisseur |

## Impact sur les Performances

### Overhead SSL
- **Négligeable** avec les processeurs modernes
- **Bénéfice sécurité** > Coût performance
- **Chiffrement** transparent pour l'application

### Recommandations Performance
```bash
# Pour maximiser les performances avec SSL:
# 1. Utiliser des certificats RSA 2048 bits (équilibre sécurité/performance)
# 2. Activer la persistance des connexions
# 3. Utiliser TLS 1.3 si disponible
```

## Audit de Sécurité

### Vérifications à Effectuer
1. [ ] `DB_SSLMODE` n'est pas `disable` ou `allow` en production
2. [ ] Les certificats sont valides et à jour
3. [ ] Les connexions distantes utilisent SSL
4. [ ] Les avertissements de sécurité sont pris en compte
5. [ ] La documentation est à jour

### Commandes de Vérification
```bash
# Vérifier toutes les configurations .env
grep -r "DB_SSLMODE" .

# Tester la connexion en production
APP_ENV=production go run cmd/main.go

# Vérifier les connexions actives avec SSL
psql -c "SELECT usename, ssl, client_addr FROM pg_stat_ssl JOIN pg_stat_activity ON pg_stat_ssl.pid = pg_stat_activity.pid;"
```

## Support

Pour toute question sur la sécurité PostgreSQL SSL:
1. Consulter ce guide
2. Vérifier la documentation PostgreSQL
3. Tester avec différents modes SSL
4. Contacter l'équipe DevOps

---

**Dernière Mise à Jour:** $(date +%Y-%m-%d)  
**Sévérité Initiale:** MOYENNE à ÉLEVÉE  
**Sévérité Actuelle:** RÉSOLU avec configuration appropriée  

**Note:** La sécurité SSL est critique pour protéger les données financières sensibles de l'application Forex.
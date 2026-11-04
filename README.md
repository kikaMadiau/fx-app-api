# FX App API

API Go pour la gestion de transactions de change, avec profils KYC clients, analyse AML/KYC, scoring de risque et alertes `risk_flags`.

## Fonctionnalites

- Gestion des cambistes/traders.
- Authentification trader avec JWT RS256.
- Gestion des clients.
- Creation de client avec profil KYC initial.
- Consultation et mise a jour du profil KYC.
- Recalcul manuel du risque KYC client.
- Creation de transaction FX avec conversion calculee depuis le taux fixe par le trader.
- Creation de transaction pour nouveau client ou client existant recherche par telephone.
- Analyse AML/KYC synchrone apres creation de transaction.
- Creation automatique de `risk_flags` quand une regle AML/KYC est declenchee.
- Consultation, filtrage, creation manuelle et suppression logique des `risk_flags`.
- Initialisation automatique des tables PostgreSQL au demarrage.
- RBAC par permissions avec les rôles TRADER, COMPLIANCE_OFFICER, SUPERVISOR et ADMIN.
- Rollback SQL automatique si l'analyse AML echoue apres creation de transaction.
- Transactions SQL englobant creation + analyse AML pour la coherence des donnees.

## Stack

- Go `1.26.2`
- PostgreSQL
- Driver SQL `github.com/lib/pq`
- Serveur HTTP natif `net/http`
- Port par defaut `8080`

## Demarrage

Au premier démarrage uniquement, si aucun administrateur actif n'existe, fournir
`BOOTSTRAP_ADMIN_EMAIL` et `BOOTSTRAP_ADMIN_PASSWORD` dans l'environnement. Le compte
ADMIN initial est créé (ou réactivé pour cette adresse) par la commande de démarrage,
jamais par une requête API. Une fois qu'un ADMIN actif existe, ces variables sont ignorées.

```bash
make run
```

Equivalent:

```bash
go build -o bin/fx-app ./cmd
./bin/fx-app
```

### Creer un utilisateur depuis le terminal

Le binaire permet aussi de creer un utilisateur sans lancer le serveur. La commande
lit sur stdin le même objet JSON que l'API:

```bash
go run ./cmd user create <<'JSON'
{
  "name": "Bureau Gombe",
  "first_name": "Jean",
  "last_name": "Kabila",
  "email": "jeank@example.com",
  "phone": "+243810000000",
  "status": "ACTIVE",
  "role": "TRADER",
  "password": "secret-password",
  "store_id": 2,
  "is_active": true
}
JSON
```

Le mot de passe doit contenir entre 12 et 72 octets. Les rôles acceptés sont `TRADER`,
`COMPLIANCE_OFFICER`, `SUPERVISOR` et `ADMIN`. La commande charge `.env`, initialise
la table des utilisateurs si nécessaire, puis crée le compte avec le rôle et l'état indiqués.

Au demarrage, l'application:

1. charge `.env` si le fichier existe;
2. ouvre la connexion PostgreSQL;
3. cree la base cible si elle n'existe pas;
4. initialise les tables;
5. expose l'API sur `http://localhost:8080`.

## Configuration

`DATABASE_URL` est prioritaire si elle est definie. Sinon l'application compose une chaine de connexion avec les variables suivantes.

| Variable | Defaut | Description |
| --- | --- | --- |
| `DATABASE_URL` | vide | URL PostgreSQL complete. |
| `DB_HOST` | `localhost` | Hote PostgreSQL. |
| `DB_PORT` | `5432` | Port PostgreSQL. |
| `DB_USER` | utilisateur systeme courant | Utilisateur PostgreSQL. |
| `DB_PASSWORD` | vide | Mot de passe PostgreSQL. |
| `DB_NAME` | `forex_trader_kyc_db` | Nom de la base applicative. |
| `DB_SSLMODE` | `prefer` en local, `require` en production | Mode SSL PostgreSQL. En production, privilégier `verify-full`. |

Exemple `.env`:

```env
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=postgres
DB_NAME=forex_trader_kyc_db
DB_SSLMODE=prefer
```

Pour la configuration des clés JWT, TLS PostgreSQL et les contrôles de sécurité, voir [SECURITY.md](SECURITY.md).

## Format Des Erreurs

Les erreurs HTTP sont renvoyees en JSON:

```json
{
  "message": "description de l'erreur"
}
```

Statuts utilises:

| Statut | Cas |
| --- | --- |
| `400 Bad Request` | JSON invalide, id invalide, champ obligatoire absent. |
| `404 Not Found` | Ressource introuvable sur les routes de lecture/suppression. |
| `500 Internal Server Error` | Erreur repository, contrainte SQL, erreur de persistence ou erreur metier non mappee. |

## Authentification

La route d'authentification trader est publique:

```http
POST /auth/trader/login
Content-Type: application/json
```

```json
{
  "email": "jean@example.com",
  "password": "secret-password"
}
```

Reponse `200 OK`:

```json
{
  "access_token": "<jwt>",
  "token_type": "Bearer",
  "expires_in": 86400,
  "trader": {
    "id": 1,
    "name": "Bureau Gombe",
    "first_name": "Jean",
    "last_name": "Kabila",
    "email": "jean@example.com",
    "phone": "+243810000000",
    "status": "ACTIVE",
    "created_at": "2026-08-21T12:30:00Z",
    "updated_at": "2026-08-21T12:30:00Z",
    "deleted_at": null,
    "role": "TRADER",
    "store_id": 1,
    "is_active": true
  }
}
```

Les routes metier protegees attendent ensuite:

```http
Authorization: Bearer <token>
```

Le token est signe en `RS256` avec les cles:

| Variable | Defaut | Description |
| --- | --- | --- |
| `JWT_PRIVATE_KEY_PATH` | `jwt/private.pem` | Cle privee utilisee pour signer les tokens. |
| `JWT_PUBLIC_KEY_PATH` | `jwt/public.pem` | Cle publique utilisee pour verifier les tokens. |

Expiration actuelle du token: 24 heures.

Routes accessibles sans jeton:

- `POST /auth/trader/login`

Toutes les autres routes, y compris l'analyse de change et la création d'un trader, exigent un bearer token. Les permissions du rôle sont renvoyées dans `trader.permissions` au login et sur `GET /auth/me`.

Toutes les autres routes exposees par `server.go` passent par la verification du bearer token.

## Routes

| Methode | Route | Description |
| --- | --- | --- |
| `POST` | `/auth/trader/login` | Authentifier un trader et obtenir un JWT. |
| `GET` | `/auth/me` | Récupérer le profil et les permissions du trader connecté. |
| `GET` | `/dashboard/summary` | Résumé opérationnel; les données du trader sont limitées à son périmètre. |
| `GET` | `/analysais` | Lister les analyses de change disponibles (permission `analysis:read`). |
| `POST` | `/traders` | Creer un cambiste. |
| `GET` | `/traders/{id}` | Recuperer un cambiste. |
| `PUT` | `/traders/{id}` | Mettre a jour un cambiste. |
| `DELETE` | `/traders/{id}` | Supprimer logiquement un cambiste. |
| `POST` | `/customers` | Creer un client simple. |
| `POST` | `/customers/kyc` | Creer un client avec profil KYC. |
| `GET` | `/customers/search?phone=...` | Rechercher un client par téléphone normalisé. |
| `GET` | `/customers/{id}` | Recuperer un client. |
| `PUT` | `/customers/{id}` | Mettre a jour un client. |
| `GET` | `/customers/{id}/kyc` | Recuperer le profil KYC d'un client. |
| `PUT` | `/customers/{id}/kyc` | Mettre a jour le KYC et recalculer le risque. |
| `POST` | `/customers/{id}/kyc/reassess` | Recalculer le risque KYC du client. |
| `PATCH` | `/customers/{id}/kyc/status` | Modifier le statut KYC (permission `kyc:verify`). |
| `POST` | `/transactions` | Creer une transaction avec conversion, pour un nouveau client ou un client existant. |
| `GET` | `/transactions` | Lister et filtrer les transactions; pagination et portée limitée pour TRADER. |
| `GET` | `/transactions/{id}` | Recuperer une transaction. |
| `POST` | `/risk-flags` | Creer une alerte de risque manuelle. |
| `GET` | `/risk-flags` | Lister les alertes de risque. |
| `GET` | `/risk-flags/{id}` | Recuperer une alerte de risque. |
| `DELETE` | `/risk-flags/{id}` | Supprimer logiquement une alerte de risque. |
| `PATCH` | `/risk-flags/{id}/status` | Affecter ou faire progresser une investigation d'alerte. |
| `GET` | `/risk-flags/{id}/history` | Consulter l'historique des changements de statut. |

## Stores et accès par rôle

Le modèle `stores` représente les maisons de change. Un trader peut porter plusieurs rôles dans un store (`TRADER`, `COMPLIANCE_OFFICER`, `SUPERVISOR`). `PLATFORM_ADMIN` est un indicateur du compte, sans store. Les rôles et le statut actif du trader sont relus en base lors de chaque requête authentifiée ; les rôles présents dans le JWT ne servent pas à autoriser les routes.

Au démarrage, la migration idempotente ajoute les stores et les colonnes `store_id`, `roles` et `is_platform_admin`. Les comptes et données historiques sont rattachés au store `Default`, créé avec l'agrément provisoire `MIGRATION-PENDING`. Les numéros d'agrément sont nettoyés (espaces retirés, majuscules) ; `BCC_LICENSE_PATTERN` peut contenir une expression régulière Go. Sans cette variable, tout numéro non vide est accepté.

Routes de gestion des stores :

| Méthode | Route | Permission |
| --- | --- | --- |
| `POST` | `/stores` | `PLATFORM_ADMIN` — crée le store et son premier superviseur dans une transaction SQL |
| `GET` | `/stores` | `PLATFORM_ADMIN` |
| `GET` | `/stores/{id}` | Membre du store concerné |
| `PATCH` | `/stores/{id}` | `PLATFORM_ADMIN` — modification auditée |
| `GET` | `/stores/{id}/members` | `SUPERVISOR` du store |
| `POST` | `/stores/{id}/members` | `SUPERVISOR` du store — rattache un compte libre ou crée un compte avec mot de passe temporaire |
| `PATCH` | `/stores/{id}/members/{traderId}` | `SUPERVISOR` du store — rôles et activation |

Les rôles de store ne peuvent être que `TRADER`, `COMPLIANCE_OFFICER` et `SUPERVISOR`. Un superviseur ne peut pas retirer le dernier rôle `SUPERVISOR` actif du store. Les modifications de store et de membres sont consignées dans `audit_log`. `GET /auth/me` retourne les rôles, permissions et informations du store du trader.

Exemple de création :

```json
{
  "name": "Maison de change Centre",
  "bcc_license_number": "BCC-12345",
  "license_issued_at": "2026-01-01T00:00:00Z",
  "license_expires_at": "2027-01-01T00:00:00Z",
  "supervisor": {
    "name": "Amina",
    "email": "amina@example.com"
  }
}
```

Une création de transaction ou modification de client/KYC est refusée avec `403` et le code `store_license_invalid` lorsque le store est suspendu, fermé ou que son agrément a expiré. Les lectures ne sont pas bloquées par ce contrôle.

> **Isolation entre stores :** chaque compte ne voit et ne modifie que les clients, transactions et alertes de son store (listes, recherche par telephone, tableau de bord, rapports). Les routes `/customers/{id}`, `/transactions/{id}` et `/risk-flags/{id}` repondent 404 pour une ligne d'un autre store (`StoreGuard`). Un meme telephone peut exister dans deux stores. L'administrateur de la plateforme voit tous les stores.

## RBAC (permissions par rôle)

Chaque route métier exige un JWT valide et la permission indiquée. Les rôles inconnus sont refusés. `401` indique un jeton absent/invalide; `403` indique une permission insuffisante. Les `TRADER` ne peuvent consulter que leurs transactions et leur périmètre du tableau de bord. La création d'une transaction est toujours rattachée au trader authentifié.

| Permission | TRADER | COMPLIANCE_OFFICER | SUPERVISOR | ADMIN |
| --- | :---: | :---: | :---: | :---: |
| `profile:read` | ✓ | ✓ | ✓ | ✓ |
| `dashboard:read` | ✓ | ✓ | ✓ | ✓ |
| `analysis:read` | — | ✓ | ✓ | ✓ |
| `trader:manage` | — | — | — | ✓ |
| `customer:read` | ✓ | ✓ | ✓ | ✓ |
| `customer:create` | ✓ | ✓ | ✓ | ✓ |
| `customer:update` | ✓ | ✓ | ✓ | — |
| `kyc:read` | ✓ | ✓ | — | ✓ |
| `kyc:update` | ✓ | ✓ | — | ✓ |
| `kyc:verify` | — | ✓ | — | — |
| `kyc:reassess` | — | ✓ | — | ✓ |
| `transaction:create` | ✓ | ✓ | ✓ | ✓ |
| `transaction:read` | ✓* | ✓ | ✓ | ✓ |
| `risk_flag:create` | ✓ | ✓ | ✓ | — |
| `risk_flag:read` | ✓ | ✓ | ✓ | ✓ |
| `risk_flag:investigate` | — | ✓ | — | — |
| `risk_flag:delete` | — | — | — | ✓ |

`✓*` : accès limité aux opérations du trader connecté, y compris `GET /transactions` et `GET /transactions/{id}`. La suppression d'alerte est logique. Les routes login restent publiques.

## Catalogue Des Requetes JSON

Cette section resume toutes les routes avec leurs payloads. Les routes `GET` et `DELETE` n'ont pas de body JSON.

### `POST /traders`

Body:

```json
{
  "name": "Bureau Gombe",
  "first_name": "Jean",
  "last_name": "Kabila",
  "email": "jean@example.com",
  "phone": "+243810000000",
  "status": "ACTIVE",
  "role": "TRADER",
  "password": "secret-password",
  "store_id": 1,
  "is_active": true
}
```

Response `201 Created`:

```json
{
  "id": 1,
  "name": "Bureau Gombe",
  "first_name": "Jean",
  "last_name": "Kabila",
  "email": "jean@example.com",
  "phone": "+243810000000",
  "status": "ACTIVE",
  "created_at": "2026-08-21T12:30:00Z",
  "updated_at": "2026-08-21T12:30:00Z",
  "deleted_at": null,
  "role": "TRADER",
  "store_id": 1,
  "is_active": true
}
```

### `POST /auth/trader/login`

Body:

```json
{
  "email": "jean@example.com",
  "password": "secret-password"
}
```

Response `200 OK`:

```json
{
  "access_token": "<jwt>",
  "token_type": "Bearer",
  "expires_in": 86400,
  "trader": {
    "id": 1,
    "name": "Bureau Gombe",
    "first_name": "Jean",
    "last_name": "Kabila",
    "email": "jean@example.com",
    "phone": "+243810000000",
    "status": "ACTIVE",
    "created_at": "2026-08-21T12:30:00Z",
    "updated_at": "2026-08-21T12:30:00Z",
    "deleted_at": null,
    "role": "TRADER",
    "store_id": 1,
    "is_active": true,
    "permissions": ["customer:create", "customer:read", "customer:update", "dashboard:read", "kyc:read", "kyc:update", "profile:read", "risk_flag:create", "risk_flag:read", "transaction:create", "transaction:read"]
  }
}
```

### `GET /auth/me`

En-tête: `Authorization: Bearer <token>`. Aucun body. Réponse `200 OK`: même objet `trader` que celui du login, y compris `permissions` (liste des permissions effectives du rôle). Exemple abrégé:

```json
{
  "id": 1,
  "name": "Bureau Gombe",
  "email": "jean@example.com",
  "role": "TRADER",
  "store_id": 1,
  "is_active": true,
  "permissions": ["customer:create", "customer:read", "customer:update", "dashboard:read", "kyc:read", "kyc:update", "profile:read", "risk_flag:create", "risk_flag:read", "transaction:create", "transaction:read"]
}
```

### `GET /dashboard/summary`

En-tête: `Authorization: Bearer <token>`. Aucun body. Réponse `200 OK`:

```json
{
  "transactions_today": 12,
  "transactions_yesterday": 9,
  "volume_today": [{"currency": "USD", "amount": 12500.75, "converted_amount": 35571408.75, "target_currency": "CDF"}],
  "active_customers": 8,
  "new_customers_this_month": 3,
  "open_alerts": 2,
  "urgent_alerts": 1
}
```

### `GET /analysais`

En-tête: `Authorization: Bearer <token>`, permission `analysis:read`. Aucun body. Réponse `200 OK`:

```json
[{"pair":"USD/CDF","buy_rate":2770,"sell_rate":2795,"change_rate":0.6}]
```

Les taux affichés sont configurables dans le fichier `.env` du backend avec les variables `EXCHANGE_RATE_<DEVISE>_CDF_BUY`, `EXCHANGE_RATE_<DEVISE>_CDF_SELL` et `EXCHANGE_RATE_<DEVISE>_CDF_CHANGE` (ex. `EXCHANGE_RATE_USD_CDF_BUY=2770`). Les valeurs par défaut sont celles de l'exemple `.env.example`. Redémarrer l'API après une modification. Ces taux alimentent l'affichage des analyses ; le taux enregistré pour une transaction reste celui fourni lors de sa création.

### `GET /traders/{id}`

Body: aucun.

Response `200 OK`: objet `Trader`.

```json
{
  "id": 1,
  "name": "Bureau Gombe",
  "first_name": "Jean",
  "last_name": "Kabila",
  "email": "jean@example.com",
  "phone": "+243810000000",
  "status": "ACTIVE",
  "created_at": "2026-08-21T12:30:00Z",
  "updated_at": "2026-08-21T12:30:00Z",
  "deleted_at": null,
  "role": "TRADER",
  "store_id": 1,
  "is_active": true
}
```

### `PUT /traders/{id}`

Body:

```json
{
  "name": "Bureau Gombe",
  "first_name": "Jean",
  "last_name": "Kabila",
  "email": "jean@example.com",
  "phone": "+243810000000",
  "status": "ACTIVE",
  "role": "COMPLIANCE_OFFICER",
  "store_id": 1,
  "is_active": true
}
```

Response `200 OK`: objet `Trader`.

### `DELETE /traders/{id}`

Body: aucun.

Response: `204 No Content`.

### `POST /customers`

Body:

```json
{
  "full_name": "Grace Hopper",
  "id_number": "ID-123456",
  "id_type": "PASSPORT",
  "phone": "+243820000000",
  "address": "Kinshasa"
}
```

Response `201 Created`:

```json
{
  "id": 1,
  "full_name": "Grace Hopper",
  "id_number": "ID-123456",
  "id_type": "PASSPORT",
  "phone": "+243820000000",
  "address": "Kinshasa",
  "created_at": "2026-08-21T12:30:00Z",
  "updated_at": "2026-08-21T12:30:00Z",
  "risk_level": "",
  "risk_score": 0
}
```

### `POST /customers/kyc`

Body:

```json
{
  "customer": {
    "full_name": "Grace Hopper",
    "id_number": "ID-123456",
    "id_type": "PASSPORT",
    "phone": "+243820000000",
    "address": "Kinshasa"
  },
  "kyc_profile": {
    "legal_nature": "INDIVIDUAL",
    "activity_profile": "BUSINESS",
    "profession": "Entrepreneur",
    "employer": "",
    "company_name": "",
    "registration_number": "",
    "source_of_funds": "Business revenue",
    "purpose_of_operations": "Currency exchange",
    "expected_volume": 50000,
    "expected_frequency": 12,
    "last_verification_date": "2026-08-21T12:30:00Z"
  }
}
```

Response `201 Created`: objet `Customer`.

```json
{
  "id": 1,
  "full_name": "Grace Hopper",
  "id_number": "ID-123456",
  "id_type": "PASSPORT",
  "phone": "+243820000000",
  "address": "Kinshasa",
  "created_at": "2026-08-21T12:30:00Z",
  "updated_at": "2026-08-21T12:30:00Z",
  "risk_level": "LOW",
  "risk_score": 20
}
```

### `GET /customers/{id}`

Body: aucun.

Response `200 OK`: objet `Customer`.

```json
{
  "id": 1,
  "full_name": "Grace Hopper",
  "id_number": "ID-123456",
  "id_type": "PASSPORT",
  "phone": "+243820000000",
  "address": "Kinshasa",
  "created_at": "2026-08-21T12:30:00Z",
  "updated_at": "2026-08-21T12:30:00Z",
  "deleted_at": null,
  "risk_level": "LOW",
  "risk_score": 20
}
```

### `GET /customers/search?phone=+243820000000`

En-tête bearer requis. Le téléphone est normalisé avant recherche. Réponse `200 OK`: objet `Customer` (même format que `GET /customers/{id}`). Si aucun client ne correspond, `404`; téléphone absent ou invalide, `400`.

### `PUT /customers/{id}`

Body:

```json
{
  "full_name": "Grace Hopper",
  "id_number": "ID-123456",
  "id_type": "NATIONAL_ID",
  "phone": "+243820000000",
  "address": "Gombe, Kinshasa"
}
```

Response `200 OK`: objet `Customer`.

### `GET /customers/{id}/kyc`

Body: aucun.

Response `200 OK`: objet `CustomerKYCProfile`.

```json
{
  "id": 1,
  "customer_id": 1,
  "legal_nature": "INDIVIDUAL",
  "activity_profile": "BUSINESS",
  "kyc_status": "UNVERIFIED",
  "profession": "Entrepreneur",
  "employer": "",
  "company_name": "",
  "registration_number": "",
  "source_of_funds": "Business revenue",
  "purpose_of_operations": "Currency exchange",
  "expected_volume": 50000,
  "expected_frequency": 12,
  "last_verification_date": "2026-08-21T12:30:00Z",
  "created_at": "2026-08-21T12:30:00Z",
  "updated_at": "2026-08-21T12:30:00Z"
}
```

### `PUT /customers/{id}/kyc`

Body:

```json
{
  "legal_nature": "INDIVIDUAL",
  "activity_profile": "PROFESSIONAL",
  "kyc_status": "VERIFIED",
  "profession": "Consultant",
  "employer": "Self-employed",
  "company_name": "",
  "registration_number": "",
  "source_of_funds": "Consulting income",
  "purpose_of_operations": "FX operations",
  "expected_volume": 25000,
  "expected_frequency": 8,
  "last_verification_date": "2026-08-21T12:30:00Z"
}
```

Response `200 OK`: profil KYC mis a jour.

### `POST /customers/{id}/kyc/reassess`

Body: aucun.

Response `200 OK`: objet `Customer` apres recalcul du risque.

```json
{
  "id": 1,
  "full_name": "Grace Hopper",
  "id_number": "ID-123456",
  "id_type": "NATIONAL_ID",
  "phone": "+243820000000",
  "address": "Gombe, Kinshasa",
  "created_at": "2026-08-21T12:30:00Z",
  "updated_at": "2026-08-21T12:35:00Z",
  "deleted_at": null,
  "risk_level": "LOW",
  "risk_score": 0
}
```

### `PATCH /customers/{id}/kyc/status`

Permission requise: `kyc:verify` (attribuée à `COMPLIANCE_OFFICER` uniquement). Cette route est le seul moyen de modifier le statut; `PUT /customers/{id}/kyc` conserve le statut existant.

```json
{"kyc_status":"VERIFIED"}
```

Réponse `200 OK`: objet `CustomerKYCProfile` mis à jour. Valeurs acceptées: `UNVERIFIED`, `PARTIALLY_VERIFIED`, `VERIFIED`, `ENHANCED_VERIFICATION`.

```json
{
  "id": 1,
  "customer_id": 1,
  "legal_nature": "INDIVIDUAL",
  "activity_profile": "PROFESSIONAL",
  "kyc_status": "VERIFIED",
  "profession": "Consultant",
  "source_of_funds": "Consulting income",
  "purpose_of_operations": "FX operations",
  "expected_volume": 25000,
  "expected_frequency": 8,
  "created_at": "2026-08-21T12:00:00Z",
  "updated_at": "2026-08-21T12:30:00Z"
}
```

### `POST /transactions`

Body nouveau client:

```json
{
  "client": {
    "first_name": "Jean",
    "last_name": "Dupont",
    "phone": "+243900000000"
  },
  "transaction": {
    "amount": 12500.75,
    "currency": "USD",
    "target_currency": "CDF",
    "type": "deposit",
    "rate": 2845.5,
    "status": "PENDING",
    "trader_id": 1
  }
}
```

Body client existant:

```json
{
  "client_phone": "+243900000000",
  "transaction": {
    "amount": 12500.75,
    "currency": "USD",
    "target_currency": "CDF",
    "type": "deposit",
    "rate": 2845.5,
    "status": "PENDING",
    "trader_id": 1
  }
}
```

Body historique toujours accepte:

```json
{
  "amount": 12500.75,
  "currency": "USD",
  "target_currency": "CDF",
  "type": "deposit",
  "rate": 2845.5,
  "status": "PENDING",
  "trader_id": 1,
  "customer_id": 1
}
```

Response `201 Created`: objet `Transaction` apres conversion et analyse AML/KYC synchrone.

Erreurs principales:

- `400 Bad Request`: payload invalide, telephone invalide, champs transaction manquants, ou plusieurs references client fournies.
- `404 Not Found`: `client_phone` ne correspond a aucun client existant.
- `409 Conflict`: tentative de creation d'un nouveau client avec un telephone deja utilise.

Le backend normalise toujours le numero de telephone avant la recherche. Pour un nouveau client, le client et la transaction sont crees dans une meme transaction SQL: si l'une des deux insertions echoue, toute l'operation est annulee.

```json
{
  "id": 10,
  "amount": 12500.75,
  "currency": "USD",
  "target_currency": "CDF",
  "type": "deposit",
  "rate": 2845.5,
  "converted_amount": 35571408.75,
  "risk_level": "MEDIUM",
  "risk_score": 30,
  "status": "PENDING",
  "created_at": "2026-08-21T12:30:00Z",
  "updated_at": "2026-08-21T12:30:01Z",
  "deleted_at": null,
  "trader_id": 1,
  "customer_id": 1
}
```

### `GET /transactions/{id}`

Body: aucun.

Response `200 OK`: objet `Transaction`.

### `GET /transactions`

En-tête bearer requis. Paramètres facultatifs: `q` (nom/téléphone client ou ID exact), `status`, `type`, `risk_level`, `customer_id`, `limit` (défaut 20, maximum 100), `offset` (défaut 0). Exemple: `GET /transactions?q=Grace&status=COMPLETED&limit=10&offset=0`.

Réponse `200 OK`; `items` contient les champs de `Transaction` plus les informations client:

```json
{
  "items": [{
    "id": 10,
    "amount": 12500.75,
    "currency": "USD",
    "target_currency": "CDF",
    "type": "BUY",
    "rate": 2845.5,
    "converted_amount": 35571408.75,
    "risk_level": "MEDIUM",
    "risk_score": 30,
    "status": "COMPLETED",
    "trader_id": 1,
    "customer_id": 1,
    "customer_name": "Grace Hopper",
    "customer_phone": "+243820000000",
    "customer_id_type": "PASSPORT",
    "customer_id_number": "ID-123456"
  }],
  "total": 1,
  "limit": 10,
  "offset": 0
}
```

### `POST /risk-flags`

Body:

```json
{
  "flag": "MANUAL_REVIEW",
  "reason": "Document KYC a verifier",
  "score": 15,
  "level": "MEDIUM",
  "transaction_id": 10,
  "trader_id": 1,
  "customer_id": 1
}
```

Response `201 Created`:

```json
{
  "id": 3,
  "flag": "MANUAL_REVIEW",
  "reason": "Document KYC a verifier",
  "score": 15,
  "level": "MEDIUM",
  "transaction_id": 10,
  "trader_id": 1,
  "customer_id": 1,
  "created_at": "2026-08-21T12:30:00Z",
  "updated_at": "2026-08-21T12:30:00Z",
  "deleted_at": null
}
```

### `GET /risk-flags`

Body: aucun.

Query params optionnels: `transaction_id`, `trader_id`, `customer_id`, `status`, `level`, `q`, `assigned_to`, `limit` (défaut 20, maximum 100), `offset` (défaut 0). Les niveaux acceptés: `LOW`, `MEDIUM`, `HIGH`, `CRITICAL`; `q` recherche dans les données associées.

Exemples:

```http
GET /risk-flags
GET /risk-flags?transaction_id=10
GET /risk-flags?customer_id=1&status=OPEN&level=HIGH&limit=10&offset=0
```

Response `200 OK`:

```json
{
  "items": [{
    "id": 3,
    "flag": "MANUAL_REVIEW",
    "reason": "Document KYC a verifier",
    "score": 15,
    "level": "MEDIUM",
    "transaction_id": 10,
    "trader_id": 1,
    "customer_id": 1,
    "status": "OPEN",
    "assigned_to": null,
    "customer_name": "Grace Hopper",
    "customer": {"id": 1, "full_name": "Grace Hopper", "phone": "+243820000000", "kyc_status": "VERIFIED"},
    "transaction": {"id": 10, "type": "BUY", "currency": "USD", "amount": 12500.75, "rate": 2845.5, "converted_amount": 35571408.75, "created_at": "2026-08-21T12:30:00Z"},
    "created_at": "2026-08-21T12:30:00Z",
    "updated_at": "2026-08-21T12:30:00Z",
    "deleted_at": null
  }],
  "total": 1,
  "limit": 10,
  "offset": 0
}
```

### `GET /risk-flags/{id}`

Body: aucun.

Response `200 OK`: objet `RiskFlag`.

### `DELETE /risk-flags/{id}`

Body: aucun.

Response: `204 No Content`.

### `PATCH /risk-flags/{id}/status`

Permission requise: `risk_flag:investigate` (`COMPLIANCE_OFFICER`). Body pour assigner et commencer l'examen:

```json
{"status":"IN_REVIEW","assigned_to":2}
```

Pour clôturer, `resolution_note` est obligatoire:

```json
{"status":"RESOLVED","assigned_to":2,"resolution_note":"Documents vérifiés, activité conforme."}
```

Les statuts sont `OPEN`, `IN_REVIEW`, `RESOLVED`, `FALSE_POSITIVE`; transitions invalides: `409 Conflict`. La fermeture par le trader à l'origine de la transaction est refusée (`403`). Réponse `200 OK`: objet `RiskFlag` actualisé, avec `status`, `assigned_to`, `resolution_note`, `resolved_at` et `resolved_by` selon l'état.

### `GET /risk-flags/{id}/history`

Permission `risk_flag:read`. Aucun body. Réponse `200 OK`:

```json
[{"id":1,"risk_flag_id":3,"from_status":"OPEN","to_status":"IN_REVIEW","changed_by":2,"note":"","created_at":"2026-08-21T12:35:00Z"}]
```

## Traders

### Creer Un Trader

```http
POST /traders
Content-Type: application/json
```

```json
{
  "name": "Bureau Gombe",
  "first_name": "Jean",
  "last_name": "Kabila",
  "email": "jean@example.com",
  "phone": "+243810000000",
  "status": "ACTIVE",
  "role": "TRADER",
  "password": "secret-password",
  "store_id": 1,
  "is_active": true
}
```

Champs obligatoires:

- `email`
- `password` ou `password_hash`

Reponse `201 Created`:

```json
{
  "id": 1,
  "name": "Bureau Gombe",
  "first_name": "Jean",
  "last_name": "Kabila",
  "email": "jean@example.com",
  "phone": "+243810000000",
  "status": "ACTIVE",
  "created_at": "2026-08-21T12:30:00Z",
  "updated_at": "2026-08-21T12:30:00Z",
  "deleted_at": null,
  "role": "TRADER",
  "store_id": 1,
  "is_active": true
}
```

### Recuperer Un Trader

```http
GET /traders/{id}
```

Reponse `200 OK`: objet `Trader`.

### Mettre A Jour Un Trader

```http
PUT /traders/{id}
Content-Type: application/json
```

Utilise le meme payload que la creation. Le handler ne modifie pas le mot de passe.

Champs obligatoires:

- `email`

Reponse `200 OK`: objet `Trader`.

### Supprimer Un Trader

```http
DELETE /traders/{id}
```

Reponse `204 No Content`. La suppression est logique via `deleted_at`.

## Customers

### Creer Un Client

```http
POST /customers
Content-Type: application/json
```

```json
{
  "full_name": "Grace Hopper",
  "id_number": "ID-123456",
  "id_type": "PASSPORT",
  "phone": "+243820000000",
  "address": "Kinshasa"
}
```

Champs obligatoires:

- `full_name`
- `id_number`

Reponse `201 Created`:

```json
{
  "id": 1,
  "full_name": "Grace Hopper",
  "id_number": "ID-123456",
  "id_type": "PASSPORT",
  "phone": "+243820000000",
  "address": "Kinshasa",
  "created_at": "2026-08-21T12:30:00Z",
  "updated_at": "2026-08-21T12:30:00Z",
  "risk_level": "",
  "risk_score": 0
}
```

### Creer Un Client Avec KYC

```http
POST /customers/kyc
Content-Type: application/json
```

```json
{
  "customer": {
    "full_name": "Grace Hopper",
    "id_number": "ID-123456",
    "id_type": "PASSPORT",
    "phone": "+243820000000",
    "address": "Kinshasa"
  },
  "kyc_profile": {
    "legal_nature": "INDIVIDUAL",
    "activity_profile": "BUSINESS",
    "profession": "Entrepreneur",
    "source_of_funds": "Business revenue",
    "purpose_of_operations": "Currency exchange",
    "expected_volume": 50000,
    "expected_frequency": 12
  }
}
```

Notes:

- `kyc_status` est force a `UNVERIFIED` a la creation initiale.
- Un risque initial est calcule et persiste sur le client.
- Une personne morale `LEGAL_ENTITY` ne peut pas avoir `activity_profile = PERSONAL`.

Reponse `201 Created`: objet `Customer` avec `risk_level` et `risk_score`.

### Recuperer Un Client

```http
GET /customers/{id}
```

Reponse `200 OK`: objet `Customer`.

### Mettre A Jour Un Client

```http
PUT /customers/{id}
Content-Type: application/json
```

Utilise le meme payload que `POST /customers`.

Reponse `200 OK`: objet `Customer`.

## KYC

### Recuperer Le Profil KYC

```http
GET /customers/{id}/kyc
```

Reponse `200 OK`:

```json
{
  "id": 1,
  "customer_id": 1,
  "legal_nature": "INDIVIDUAL",
  "activity_profile": "BUSINESS",
  "kyc_status": "VERIFIED",
  "profession": "Entrepreneur",
  "employer": "",
  "company_name": "",
  "registration_number": "",
  "source_of_funds": "Business revenue",
  "purpose_of_operations": "Currency exchange",
  "expected_volume": 50000,
  "expected_frequency": 12,
  "last_verification_date": "2026-08-21T12:30:00Z",
  "created_at": "2026-08-21T12:00:00Z",
  "updated_at": "2026-08-21T12:30:00Z"
}
```

### Mettre A Jour Le Profil KYC

```http
PUT /customers/{id}/kyc
Content-Type: application/json
```

```json
{
  "legal_nature": "INDIVIDUAL",
  "activity_profile": "PROFESSIONAL",
  "kyc_status": "VERIFIED",
  "profession": "Consultant",
  "employer": "Self-employed",
  "source_of_funds": "Consulting income",
  "purpose_of_operations": "FX operations",
  "expected_volume": 25000,
  "expected_frequency": 8,
  "last_verification_date": "2026-08-21T12:30:00Z"
}
```

Comportement:

- preserve `id`, `customer_id` et `created_at`;
- conserve l'ancien `kyc_status` si le champ est vide;
- conserve `last_verification_date` si le champ est vide;
- valide la coherence KYC;
- persiste le profil;
- relance automatiquement `TriggerRiskReassessment`.

Reponse `200 OK`: profil KYC mis a jour.

### Recalculer Le Risque KYC

```http
POST /customers/{id}/kyc/reassess
```

Reponse `200 OK`: objet `Customer` apres recalcul.

### Enumerations KYC

`legal_nature`:

- `INDIVIDUAL`
- `LEGAL_ENTITY`

`activity_profile`:

- `PERSONAL`
- `PROFESSIONAL`
- `BUSINESS`
- `OTHER`

`kyc_status`:

- `UNVERIFIED`
- `PARTIALLY_VERIFIED`
- `VERIFIED`
- `ENHANCED_VERIFICATION`

## Transactions FX

### Creer Une Transaction

```http
POST /transactions
Content-Type: application/json
```

Nouveau client:

```json
{
  "client": {
    "first_name": "Jean",
    "last_name": "Dupont",
    "phone": "+243900000000"
  },
  "transaction": {
    "amount": 12500.75,
    "currency": "USD",
    "target_currency": "CDF",
    "type": "deposit",
    "rate": 2845.5,
    "status": "PENDING",
    "trader_id": 1
  }
}
```

Client existant:

```json
{
  "client_phone": "+243900000000",
  "transaction": {
    "amount": 12500.75,
    "currency": "USD",
    "target_currency": "CDF",
    "type": "deposit",
    "rate": 2845.5,
    "status": "PENDING",
    "trader_id": 1
  }
}
```

Format historique avec `customer_id`:

```json
{
  "amount": 12500.75,
  "currency": "USD",
  "target_currency": "CDF",
  "type": "deposit",
  "rate": 2845.5,
  "status": "PENDING",
  "trader_id": 1,
  "customer_id": 2
}
```

| Champ | Type | Obligatoire | Description |
| --- | --- | --- | --- |
| `amount` | number | oui | Montant source. Doit etre `> 0`. |
| `currency` | string | oui | Devise source, convertie en majuscules. |
| `target_currency` | string | oui | Devise cible, convertie en majuscules. |
| `type` | string | non | Type metier, par exemple `deposit`, `withdrawal`, `buy`, `sell`. |
| `rate` | number | oui | Taux fixe par le trader. Doit etre `> 0`. |
| `status` | string | non | Statut initial. |
| `trader_id` | number | oui | Identifiant du trader. |
| `customer_id` | number | oui si pas de `client` ni `client_phone` | Identifiant du client. |
| `client_phone` | string | oui pour client existant | Telephone du client existant, normalise avant recherche. |
| `client` | object | oui pour nouveau client | Informations du nouveau client a creer. |
| `transaction` | object | oui pour les nouveaux formats | Informations de la transaction. |

Pour un nouveau client, le backend verifie d'abord que le telephone normalise n'existe pas. Si le telephone existe deja, la reponse est `409 Conflict`. Si le telephone est libre, le client et la transaction sont crees atomiquement dans une transaction SQL.

Pour un client existant, le backend recherche le client via `client_phone`, recupere son identifiant et associe la transaction sans modifier les informations du client. Si aucun client ne correspond au telephone fourni, la reponse est `404 Not Found`.

### Calcul De Conversion

Le calcul est fait avant la persistence:

```text
converted_amount = amount * rate
```

Le resultat est arrondi a 4 decimales.

Exemple:

```text
100 USD * 2850.55 = 285055 CDF
```

Reponse `201 Created`:

```json
{
  "id": 10,
  "amount": 12500.75,
  "currency": "USD",
  "target_currency": "CDF",
  "type": "deposit",
  "rate": 2845.5,
  "converted_amount": 35571408.75,
  "status": "PENDING",
  "created_at": "2026-08-21T12:30:00Z",
  "updated_at": "2026-08-21T12:30:00Z",
  "deleted_at": null,
  "trader_id": 1,
  "customer_id": 2
}
```

Important: l'analyse AML/KYC est executee avant la reponse. La reponse `201 Created` contient donc le `risk_level` et le `risk_score` transactionnels finalises.

### Recuperer Une Transaction

```http
GET /transactions/{id}
```

Reponse `200 OK`: objet `Transaction`, avec `risk_level` et `risk_score`.

## Risk Flags

Les `risk_flags` representent les alertes produites par le moteur AML/KYC ou ajoutees manuellement.

### Creer Un Risk Flag Manuel

```http
POST /risk-flags
Content-Type: application/json
```

```json
{
  "flag": "MANUAL_REVIEW",
  "reason": "Document KYC a verifier",
  "score": 15,
  "level": "MEDIUM",
  "transaction_id": 10,
  "trader_id": 1,
  "customer_id": 2
}
```

Champs obligatoires:

- `flag`
- `transaction_id`

Reponse `201 Created`:

```json
{
  "id": 3,
  "flag": "MANUAL_REVIEW",
  "reason": "Document KYC a verifier",
  "score": 15,
  "level": "MEDIUM",
  "transaction_id": 10,
  "trader_id": 1,
  "customer_id": 2,
  "created_at": "2026-08-21T12:30:00Z",
  "updated_at": "2026-08-21T12:30:00Z",
  "deleted_at": null
}
```

### Lister Les Risk Flags

```http
GET /risk-flags
GET /risk-flags?transaction_id=10
GET /risk-flags?customer_id=2
GET /risk-flags?trader_id=1
```

Filtres optionnels combinables:

- `transaction_id`
- `customer_id`
- `trader_id`

Reponse `200 OK`: tableau de `RiskFlag`, trie par `created_at DESC, id DESC`.

### Recuperer Un Risk Flag

```http
GET /risk-flags/{id}
```

Reponse `200 OK`: objet `RiskFlag`.

### Supprimer Un Risk Flag

```http
DELETE /risk-flags/{id}
```

Reponse `204 No Content`. La suppression est logique via `deleted_at`.

## Analyse AML/KYC

`RiskService.AnalyzeTransactionAndCustomer` est execute pendant chaque `POST /transactions`, apres la persistence de la transaction et avant la reponse HTTP.

Flux:

1. recuperation du client;
2. recuperation du profil KYC si disponible;
3. recuperation de l'historique transactionnel du client sur 30 jours;
4. execution des regles transactionnelles;
5. mise a jour du risque transaction;
6. execution des regles KYC/client;
7. creation des `risk_flags` pour toutes les regles declenchees;
8. mise a jour du risque global client avec `transactionRisk.Score + customerRisk.Score`.

### Regles Transactionnelles

| Code | Condition | Score |
| --- | --- | --- |
| `HIGH_TRANSACTION_AMOUNT` | `amount > 10000.0` | `30` |
| `CUMULATIVE_VOLUME_24H` | cumul client sur 24h `> 20000.0` | `35` |
| `CUMULATIVE_VOLUME_30D` | cumul client sur 30j `> 100000.0` | `40` |
| `UNUSUAL_FREQUENCY_24H` | nombre de transactions client sur 24h `> 10` | `25` |

Seuils du risque transaction:

| Score | Niveau |
| --- | --- |
| `>= 75` | `CRITICAL` |
| `>= 50` | `HIGH` |
| `>= 25` | `MEDIUM` |
| `< 25` | `LOW` |

### Regles KYC/Client

| Code | Condition | Score |
| --- | --- | --- |
| `INSUFFICIENT_KYC_STATUS` | profil KYC absent ou statut different de `VERIFIED` / `ENHANCED_VERIFICATION` | `45` |
| `INCONSISTENT_KYC_PROFILE` | profil absent, personne morale `PERSONAL`, donnees societe manquantes, profession manquante ou source/purpose manquants | `35` |
| `BUSINESS_ACTIVITY_PROFILE` | `activity_profile == BUSINESS` | `15` |
| `EXPECTED_VOLUME_EXCEEDED` | cumul client 30j `> expected_volume` declare au KYC | `30` |

Seuils du risque KYC/client:

| Score | Niveau |
| --- | --- |
| `>= 70` | `CRITICAL` |
| `>= 40` | `HIGH` |
| `>= 20` | `MEDIUM` |
| `< 20` | `LOW` |

Seuils du risque global client apres transaction:

| Score total | Niveau |
| --- | --- |
| `>= 90` | `CRITICAL` |
| `>= 60` | `HIGH` |
| `>= 30` | `MEDIUM` |
| `< 30` | `LOW` |

### Recalcul KYC Manuel

`POST /customers/{id}/kyc/reassess` utilise le profil KYC courant du client et met a jour `customers.risk_score` / `customers.risk_level`.

Ce recalcul manuel est plus simple que l'analyse AML post-transaction: il ne lance pas les regles de cumul/frequence transactionnelle.

## Modele De Donnees

### `traders`

| Colonne | Type | Notes |
| --- | --- | --- |
| `id` | `SERIAL` | Cle primaire. |
| `name` | `VARCHAR(255)` | Nom affiche. |
| `first_name` | `VARCHAR(255)` | Prenom. |
| `last_name` | `VARCHAR(255)` | Nom. |
| `email` | `VARCHAR(255)` | Unique, obligatoire. |
| `phone` | `VARCHAR(50)` | Telephone. |
| `status` | `VARCHAR(50)` | Statut. |
| `role` | `VARCHAR(50)` | Role. |
| `password_hash` | `VARCHAR(255)` | Hash bcrypt du mot de passe, obligatoire. |
| `store_id` | `INT` | Point de vente. |
| `is_active` | `BOOLEAN` | Defaut `TRUE`. |
| `created_at`, `updated_at`, `deleted_at` | `TIMESTAMPTZ` | Timestamps. |

### `customers`

| Colonne | Type | Notes |
| --- | --- | --- |
| `id` | `SERIAL` | Cle primaire. |
| `full_name` | `VARCHAR(255)` | Obligatoire. |
| `id_number` | `VARCHAR(100)` | Unique, obligatoire. |
| `id_type` | `VARCHAR(50)` | Type de piece. |
| `phone` | `VARCHAR(50)` | Telephone normalise, utilise pour rechercher le client. Unique pour les clients actifs. |
| `address` | `TEXT` | Adresse. |
| `risk_level` | `VARCHAR(50)` | `LOW`, `MEDIUM`, `HIGH`, `CRITICAL`. |
| `risk_score` | `NUMERIC(5, 2)` | Defaut `0`. |
| `created_at`, `updated_at`, `deleted_at` | `TIMESTAMPTZ` | Timestamps. |

### `customer_kyc_profiles`

| Colonne | Type | Notes |
| --- | --- | --- |
| `id` | `SERIAL` | Cle primaire. |
| `customer_id` | `INT` | Unique, reference `customers(id)`. |
| `legal_nature` | `VARCHAR(50)` | `INDIVIDUAL` ou `LEGAL_ENTITY`. |
| `activity_profile` | `VARCHAR(50)` | `PERSONAL`, `PROFESSIONAL`, `BUSINESS`, `OTHER`. |
| `kyc_status` | `VARCHAR(50)` | Statut KYC. |
| `profession`, `employer` | `VARCHAR(255)` | Champs personne physique. |
| `company_name`, `registration_number` | `VARCHAR(255)` | Champs personne morale. |
| `source_of_funds` | `TEXT` | Source des fonds. |
| `purpose_of_operations` | `TEXT` | Objectif des operations. |
| `expected_volume` | `NUMERIC(14, 2)` | Volume mensuel attendu. |
| `expected_frequency` | `INT` | Nombre de transactions mensuelles attendu. |
| `last_verification_date` | `TIMESTAMPTZ` | Derniere verification. |
| `created_at`, `updated_at` | `TIMESTAMPTZ` | Timestamps. |

### `transactions`

| Colonne | Type | Notes |
| --- | --- | --- |
| `id` | `SERIAL` | Cle primaire. |
| `amount` | `NUMERIC(19, 4)` | Montant source. |
| `currency` | `VARCHAR(10)` | Devise source. |
| `target_currency` | `VARCHAR(10)` | Devise cible. |
| `type` | `VARCHAR(50)` | Type de transaction. |
| `rate` | `NUMERIC(19, 6)` | Taux fixe par le trader. |
| `converted_amount` | `NUMERIC(19, 4)` | Resultat `amount * rate`. |
| `risk_level` | `VARCHAR(50)` | Mis a jour par l'analyse AML. |
| `risk_score` | `NUMERIC(5, 2)` | Mis a jour par l'analyse AML. |
| `status` | `VARCHAR(50)` | Statut transactionnel. |
| `trader_id` | `INT` | Reference `traders(id)`. |
| `customer_id` | `INT` | Reference `customers(id)`. |
| `created_at`, `updated_at`, `deleted_at` | `TIMESTAMPTZ` | Timestamps. |

### `risk_flags`

| Colonne | Type | Notes |
| --- | --- | --- |
| `id` | `SERIAL` | Cle primaire. |
| `flag` | `VARCHAR(255)` | Code de l'alerte. |
| `reason` | `TEXT` | Explication lisible. |
| `score` | `NUMERIC(5, 2)` | Score associe. |
| `level` | `VARCHAR(50)` | Niveau optionnel. |
| `transaction_id` | `INT` | Transaction concernee. |
| `trader_id` | `INT` | Trader concerne. |
| `customer_id` | `INT` | Client concerne. |
| `created_at`, `updated_at`, `deleted_at` | `TIMESTAMPTZ` | Timestamps. |

## Tests

```bash
go test ./...
```

Dans un environnement ou le cache Go utilisateur n'est pas accessible:

```bash
GOCACHE=/private/tmp/fx-app-api-go-build go test ./...
```

## Limitations Actuelles

- `RuleConfig.Enabled` existe mais n'est pas encore utilise pour desactiver une regle.
- Pas encore de screening sanctions, PEP, listes noires ou adverse media.
- Pas encore de screening sanctions, PEP, listes noires ou adverse media.
- Pas encore de gestion documentaire KYC: documents, expiration, verification de piece, justificatif d'adresse.
- Les rôles valides sont `TRADER`, `COMPLIANCE_OFFICER`, `SUPERVISOR` et `ADMIN`.

## Historique Des Modifications

### RBAC, investigation et sécurité KYC

- Permissions explicites appliquées à toutes les routes protégées et refus par défaut pour les rôles inconnus.
- Le statut KYC se modifie uniquement via `/customers/{id}/kyc/status`, réservé à la conformité.
- Les traders ne lisent que leurs transactions; `/dashboard/summary` applique aussi leur portée.
- Journal d'audit des changements de profil/statut KYC et des suppressions/clôtures d'alertes.
- Routes de recherche client, liste filtrable/paginée des transactions, résumé du dashboard et profil `/auth/me`.
- Workflow de risque avec affectation, transition de statut, notes de résolution et historique.

### 2026-08-21

- **RBAC par role (version initiale)** : documentation historique de la première configuration.

- **Rollback SQL apres analyse AML** : Les transactions SQL englobent maintenant la creation de transaction et l'analyse AML.
  - Si l'analyse AML echoue, toute la transaction est rollbackee
  - Aucune donnee n'est persistee si l'analyse echoue
  - Nouvelles methodes `WithTx` dans les repositories pour supporter les transactions SQL

### 2026-08-20

- **Analyse AML/KYC synchrone** : L'analyse est executee apres la creation de transaction et avant la reponse HTTP.
- **Scoring de risque** : Calcul automatique de `risk_score` et `risk_level` pour les transactions et les clients.
- **Risk flags** : Creation automatique d'alertes quand les regles AML/KYC sont declenchees.

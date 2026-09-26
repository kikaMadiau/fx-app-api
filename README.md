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
- RBAC (Role-Based Access Control) par role TRADER/MANAGER.
- Rollback SQL automatique si l'analyse AML echoue apres creation de transaction.
- Transactions SQL englobant creation + analyse AML pour la coherence des donnees.

## Stack

- Go `1.26.2`
- PostgreSQL
- Driver SQL `github.com/lib/pq`
- Serveur HTTP natif `net/http`
- Port par defaut `8080`

## Demarrage

```bash
make run
```

Equivalent:

```bash
go build -o bin/fx-app ./cmd
./bin/fx-app
```

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
| `DB_SSLMODE` | `disable` | Mode SSL PostgreSQL. |

Exemple `.env`:

```env
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=postgres
DB_NAME=forex_trader_kyc_db
DB_SSLMODE=disable
```

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

Routes publiques:

- `POST /auth/trader/login`
- `GET /analysais`
- `POST /traders`

Toutes les autres routes exposees par `server.go` passent par la verification du bearer token.

## Routes

| Methode | Route | Description |
| --- | --- | --- |
| `POST` | `/auth/trader/login` | Authentifier un trader et obtenir un JWT. |
| `GET` | `/analysais` | Lister les analyses de change disponibles. |
| `POST` | `/traders` | Creer un cambiste. |
| `GET` | `/traders/{id}` | Recuperer un cambiste. |
| `PUT` | `/traders/{id}` | Mettre a jour un cambiste. |
| `DELETE` | `/traders/{id}` | Supprimer logiquement un cambiste. |
| `POST` | `/customers` | Creer un client simple. |
| `POST` | `/customers/kyc` | Creer un client avec profil KYC. |
| `GET` | `/customers/{id}` | Recuperer un client. |
| `PUT` | `/customers/{id}` | Mettre a jour un client. |
| `GET` | `/customers/{id}/kyc` | Recuperer le profil KYC d'un client. |
| `PUT` | `/customers/{id}/kyc` | Mettre a jour le KYC et recalculer le risque. |
| `POST` | `/customers/{id}/kyc/reassess` | Recalculer le risque KYC du client. |
| `POST` | `/transactions` | Creer une transaction avec conversion, pour un nouveau client ou un client existant. |
| `GET` | `/transactions/{id}` | Recuperer une transaction. |
| `POST` | `/risk-flags` | Creer une alerte de risque manuelle. |
| `GET` | `/risk-flags` | Lister les alertes de risque. |
| `GET` | `/risk-flags/{id}` | Recuperer une alerte de risque. |
| `DELETE` | `/risk-flags/{id}` | Supprimer logiquement une alerte de risque. |

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
    "is_active": true
  }
}
```

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
  "role": "MANAGER",
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

Query params optionnels:

- `transaction_id`
- `trader_id`
- `customer_id`

Exemples:

```http
GET /risk-flags
GET /risk-flags?transaction_id=10
GET /risk-flags?customer_id=1&trader_id=1
```

Response `200 OK`:

```json
[
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
]
```

### `GET /risk-flags/{id}`

Body: aucun.

Response `200 OK`: objet `RiskFlag`.

### `DELETE /risk-flags/{id}`

Body: aucun.

Response: `204 No Content`.

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
- Pas encore de workflow d'investigation complet pour les alertes (`OPEN`, `IN_REVIEW`, `RESOLVED`, `FALSE_POSITIVE`).
- Pas encore de gestion documentaire KYC: documents, expiration, verification de piece, justificatif d'adresse.
- Les formats metier de `currency`, `status`, `type`, `role` ne sont pas encore normalises par enum stricte.

## Historique Des Modifications

### 2026-08-21

- **RBAC par role** : Connexion du `RoleMiddleware` aux routes avec definition des permissions par endpoint.
  - Routes TRADER/MANAGER : traders, customers, transactions, risk-flags
  - Routes MANAGER uniquement : suppression de traders et risk-flags
  - Routes publiques : `/auth/trader/login`, `/analysais`

- **Rollback SQL apres analyse AML** : Les transactions SQL englobent maintenant la creation de transaction et l'analyse AML.
  - Si l'analyse AML echoue, toute la transaction est rollbackee
  - Aucune donnee n'est persistee si l'analyse echoue
  - Nouvelles methodes `WithTx` dans les repositories pour supporter les transactions SQL

### 2026-08-20

- **Analyse AML/KYC synchrone** : L'analyse est executee apres la creation de transaction et avant la reponse HTTP.
- **Scoring de risque** : Calcul automatique de `risk_score` et `risk_level` pour les transactions et les clients.
- **Risk flags** : Creation automatique d'alertes quand les regles AML/KYC sont declenchees.

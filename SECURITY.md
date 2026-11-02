# Sécurité et configuration

Ce guide décrit la configuration réellement utilisée par `fx-app-api`. Le fichier `.env.example` est un modèle ; ne placez jamais de vraies clés ou mots de passe dans Git.

## Configuration locale

1. Copiez `.env.example` vers `.env` et renseignez les paramètres PostgreSQL.
2. Générez une paire de clés JWT locale :

   ```sh
   ./scripts/generate_jwt_keys.sh ./jwt
   ```

3. Ajoutez à `.env` les chemins `JWT_PRIVATE_KEY_PATH=./jwt/private.pem` et `JWT_PUBLIC_KEY_PATH=./jwt/public.pem`.
4. Au premier lancement, définissez `BOOTSTRAP_ADMIN_EMAIL` et `BOOTSTRAP_ADMIN_PASSWORD`. Le mot de passe doit faire entre 12 et 72 octets.
5. Lancez `make run`.

`.env`, les répertoires de clés et les clés privées sont exclus par `.gitignore`. La clé privée doit rester lisible uniquement par le compte qui exécute l’API.

## PostgreSQL

`DATABASE_URL` prévaut sur les paramètres `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME` et `DB_SSLMODE`.

- En développement local, `DB_SSLMODE=prefer` convient lorsque PostgreSQL local n’a pas TLS.
- En production, utilisez `DB_SSLMODE=verify-full` ou une `DATABASE_URL` avec `sslmode=verify-full` et le certificat CA configuré.
- `require` chiffre la connexion, mais ne valide pas l’identité du serveur comme `verify-full`.
- Le démarrage échoue si une base distante/production utilise `sslmode=disable`, `allow`, ou aucun mode TLS explicite dans `DATABASE_URL`.

Test de configuration :

```sh
./scripts/test_postgres_ssl.sh verify-full
```

## Clés JWT

L’API signe les jetons en RS256. Elle lit les clés, par ordre de priorité, depuis les variables base64 `JWT_PRIVATE_KEY` et `JWT_PUBLIC_KEY`, les chemins `JWT_PRIVATE_KEY_PATH` et `JWT_PUBLIC_KEY_PATH`, puis `jwt/private.pem` et `jwt/public.pem`.

En production, injectez les secrets via le gestionnaire de secrets de la plateforme ou montez des fichiers protégés. N’insérez jamais les clés privées dans une image, un dépôt, un ticket ou des logs.

La durée de vie des jetons est de 24 heures. Les rôles et l’état actif sont rechargés depuis la base à chaque requête authentifiée. Une rotation doit être planifiée et déployée de manière coordonnée : le générateur produit une nouvelle paire, mais la rotation automatique persistante n’est pas activée par l’application.

Génération et vérification :

```sh
./scripts/generate_jwt_keys.sh ./jwt
go test ./internal/utils/services
```

## Contrôles de l’API

- Les routes protégées déclarent une permission ; un rôle inconnu est refusé.
- Les traders sont limités à leurs transactions, et les accès hors périmètre répondent `404`.
- Seul le rôle conformité peut valider le statut KYC ou clôturer une alerte de risque.
- Le créateur d’une transaction ne peut pas clôturer l’alerte qui lui est associée.
- Les corps JSON sont limités à 1 Mio ; les mots de passe sont limités à 72 octets pour respecter bcrypt.
- Le limiteur compte les échecs `401`/`403` sur la connexion et applique le bannissement configuré.
- Le serveur HTTP définit des délais de lecture et d’écriture. En production, exposez l’API derrière un proxy TLS correctement configuré ; le serveur Go écoute en HTTP.
- Les erreurs internes sont masquées dans les réponses HTTP. Ne journalisez jamais les mots de passe, jetons ou clés.

Les changements sensibles sont audités, mais certaines écritures métier et leur entrée d’audit ne partagent pas encore une transaction SQL atomique. Une panne d’écriture d’audit après la mutation peut donc laisser une action sans entrée correspondante.

## Vérifications

```sh
go test ./...
go vet ./...
git check-ignore .env jwt/private.pem
```

Avant un déploiement, vérifiez également que le proxy impose TLS, que les secrets sont injectés hors du dépôt et que PostgreSQL valide le certificat du serveur.

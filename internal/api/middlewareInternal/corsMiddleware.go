package middlewareInternal

import "net/http"

// NOTE: Pour la production, remplacez "*" par votre domaine front-end.
// Exemple: "https://votre-app.com"
var allowedOrigins = []string{"*"}

func CorsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Pour une sécurité accrue, vérifiez l'origine et ne mettez l'en-tête que si elle est autorisée.
		// Pour cet exemple, nous gardons '*' mais avec une note.
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigins[0])
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

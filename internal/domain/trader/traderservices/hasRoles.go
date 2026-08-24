package traderservices

// HasRole vérifie si une slice de rôles contient un rôle spécifique.
func HasRole(roles []string, target string) bool {
	for _, role := range roles {
		if role == target {
			return true
		}
	}
	return false
}

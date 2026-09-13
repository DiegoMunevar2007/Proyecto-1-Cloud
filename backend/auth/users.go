package auth

import (
	"gorm.io/gorm"
)

// LookupUser resuelve username → (userID, rol fresco de DB).
// Comparte la consulta que cada dominio necesita tras RequireAuth/RequireRole.
func LookupUser(db *gorm.DB, username string) (uint, string) {
	var u UserModel
	if err := db.Where("username = ?", username).First(&u).Error; err != nil {
		return 0, ""
	}
	return u.ID, u.Role
}

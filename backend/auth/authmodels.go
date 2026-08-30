package auth

import (
	"strings"

	"gorm.io/gorm"
)

// Roles permitidos para los usuarios.
const (
	RoleStudent   = "student"
	RoleProfessor = "professor"
	RoleAdmin     = "admin"
)

// ValidRoles es el conjunto de roles válidos.
var ValidRoles = map[string]bool{
	RoleStudent:   true,
	RoleProfessor: true,
	RoleAdmin:     true,
}

type UserModel struct {
	gorm.Model
	Username   string `gorm:"uniqueIndex;not null"`
	Email      string `gorm:"not null"`
	Password   string `gorm:"not null"`
	Role       string `gorm:"not null;default:'student'"`
	IsVerified bool   `gorm:"not null;default:false"`
}

// NormalizeRole normaliza el rol a minúsculas sin espacios; si está vacío retorna student.
func NormalizeRole(role string) string {
	role = strings.TrimSpace(strings.ToLower(role))
	if role == "" {
		return RoleStudent
	}
	return role
}

// IsValidRole verifica si el rol es uno de los permitidos.
func IsValidRole(role string) bool {
	return ValidRoles[role]
}

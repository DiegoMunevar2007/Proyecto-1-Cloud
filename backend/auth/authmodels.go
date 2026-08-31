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

// Estados permitidos para los usuarios.
const (
	StatusActive   = "active"
	StatusInactive = "inactive"
	StatusBlocked  = "blocked"
)

// ValidStatus es el conjunto de estados válidos.
var ValidStatus = map[string]bool{
	StatusActive:   true,
	StatusInactive: true,
	StatusBlocked:  true,
}

type UserModel struct {
	gorm.Model
	Username   string `gorm:"uniqueIndex;not null"`
	Email      string `gorm:"not null"`
	Password   string `gorm:"not null"`
	Role       string `gorm:"not null;default:'student'"`
	Status     string `gorm:"not null;default:'active';index"`
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

// NormalizeStatus normaliza el estado a minúsculas sin espacios; si está vacío retorna active.
func NormalizeStatus(status string) string {
	status = strings.TrimSpace(strings.ToLower(status))
	if status == "" {
		return StatusActive
	}
	return status
}

// IsValidStatus verifica si el estado es uno de los permitidos.
func IsValidStatus(status string) bool {
	return ValidStatus[status]
}

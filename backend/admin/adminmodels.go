package admin

import (
	"gorm.io/gorm"
)

// AuditLog registra acciones administrativas para auditoría.
type AuditLog struct {
	gorm.Model
	ActorID       *uint  `gorm:"index" json:"actor_id"`
	ActorUsername string `gorm:"index" json:"actor_username"`
	Action        string `gorm:"index;not null" json:"action"` // role_change | status_change | delete | restore | revoke_sessions | revoke_one | login_blocked
	TargetUserID  *uint  `gorm:"index" json:"target_user_id"`
	TargetUsername string `gorm:"index" json:"target_username"`
	IP            string `json:"ip"`
	UserAgent     string `json:"user_agent"`
	Detail        string `gorm:"type:text" json:"detail"`
}

// Acciones de auditoría.
const (
	ActionRoleChange      = "role_change"
	ActionStatusChange    = "status_change"
	ActionDelete          = "delete"
	ActionRestore         = "restore"
	ActionRevokeSessions  = "revoke_sessions"
	ActionRevokeOne       = "revoke_one"
	ActionLoginBlocked    = "login_blocked"
)

// Filtros para listado de usuarios.
type UserFilter struct {
	Search   string
	Role     string
	Status   string
	Verified *bool
	Page     int
	Limit    int
	Sort     string // e.g. "created_at desc"
}

// Filtros para auditoría.
type AuditFilter struct {
	Actor  string
	Target string
	Action string
	From   string
	To     string
	Page   int
	Limit  int
}

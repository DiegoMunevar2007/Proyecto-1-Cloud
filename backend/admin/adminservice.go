package admin

import (
	"errors"
	"fmt"
	"strings"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/auth"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// ErrLastAdmin es el error al intentar dejar sin administradores activos.
var ErrLastAdmin = errors.New("no se puede dejar el sistema sin ningún administrador activo")

// Actor representa quién ejecuta la acción administrativa, sin depender de gin.Context.
type Actor struct {
	Username  string
	IP        string
	UserAgent string
}

func audit(db *gorm.DB, actor Actor, action string, targetID *uint, targetUsername, detail string) {
	var actorID *uint
	if actor.Username != "" {
		var u auth.UserModel
		if err := db.Unscoped().Where("username = ?", actor.Username).First(&u).Error; err == nil {
			actorID = &u.ID
		}
	}
	entry := AuditLog{
		ActorID:        actorID,
		ActorUsername:  actor.Username,
		Action:         action,
		TargetUserID:   targetID,
		TargetUsername: targetUsername,
		IP:             actor.IP,
		UserAgent:      actor.UserAgent,
		Detail:         detail,
	}
	_ = db.Create(&entry).Error
}

func countActiveAdmins(db *gorm.DB) (int64, error) {
	var count int64
	err := db.Model(&auth.UserModel{}).Where("role = ? AND status = ?", auth.RoleAdmin, auth.StatusActive).Count(&count).Error
	return count, err
}

func isLastActiveAdmin(db *gorm.DB, user *auth.UserModel) (bool, error) {
	if auth.NormalizeRole(user.Role) != auth.RoleAdmin || auth.NormalizeStatus(user.Status) != auth.StatusActive {
		return false, nil
	}
	count, err := countActiveAdmins(db)
	if err != nil {
		return false, err
	}
	return count <= 1, nil
}

// ListUsers retorna usuarios paginados con filtros.
func ListUsers(db *gorm.DB, f UserFilter) ([]auth.UserModel, int64, error) {
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	query := db.Model(&auth.UserModel{})

	if f.Search != "" {
		like := "%" + strings.TrimSpace(f.Search) + "%"
		query = query.Where("username ILIKE ? OR email ILIKE ?", like, like)
	}
	if f.Role != "" {
		role := auth.NormalizeRole(f.Role)
		if auth.IsValidRole(role) {
			query = query.Where("role = ?", role)
		}
	}
	if f.Status != "" {
		status := auth.NormalizeStatus(f.Status)
		if auth.IsValidStatus(status) {
			query = query.Where("status = ?", status)
		}
	}
	if f.Verified != nil {
		query = query.Where("is_verified = ?", *f.Verified)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	sort := "created_at desc"
	if f.Sort != "" {
		// whitelist simple: permitir solo columnas conocidas
		allowed := map[string]bool{"created_at": true, "username": true, "email": true, "role": true, "status": true}
		parts := strings.Fields(f.Sort)
		if len(parts) > 0 && allowed[parts[0]] {
			order := "asc"
			if len(parts) > 1 && strings.ToLower(parts[1]) == "desc" {
				order = "desc"
			}
			sort = parts[0] + " " + order
		}
	}
	var users []auth.UserModel
	if err := query.Order(sort).Offset((f.Page - 1) * f.Limit).Limit(f.Limit).Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

func GetUserByID(db *gorm.DB, id uint) (*auth.UserModel, error) {
	var user auth.UserModel
	if err := db.First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func UpdateUserRole(db *gorm.DB, actor Actor, id uint, newRole string) (*auth.UserModel, error) {
	newRole = auth.NormalizeRole(newRole)
	if !auth.IsValidRole(newRole) {
		return nil, errors.New("rol inválido. Roles permitidos: student, professor, admin")
	}
	var user auth.UserModel
	if err := db.First(&user, id).Error; err != nil {
		return nil, err
	}
	oldRole := user.Role
	if oldRole == newRole {
		return &user, nil
	}
	// Proteger último admin activo: si se degrada un admin activo
	if auth.NormalizeRole(oldRole) == auth.RoleAdmin && auth.NormalizeStatus(user.Status) == auth.StatusActive && newRole != auth.RoleAdmin {
		isLast, err := isLastActiveAdmin(db, &user)
		if err != nil {
			return nil, err
		}
		if isLast {
			return nil, ErrLastAdmin
		}
	}
	user.Role = newRole
	if err := db.Save(&user).Error; err != nil {
		return nil, err
	}
	audit(db, actor, ActionRoleChange, &user.ID, user.Username, fmt.Sprintf("rol cambiado de %s a %s", oldRole, newRole))
	return &user, nil
}

func UpdateUserStatus(db *gorm.DB, actor Actor, rdb *redis.Client, id uint, newStatus string) (*auth.UserModel, error) {
	newStatus = auth.NormalizeStatus(newStatus)
	if !auth.IsValidStatus(newStatus) {
		return nil, errors.New("estado inválido. Estados permitidos: active, inactive, blocked")
	}
	var user auth.UserModel
	if err := db.First(&user, id).Error; err != nil {
		return nil, err
	}
	oldStatus := auth.NormalizeStatus(user.Status)
	if oldStatus == newStatus {
		return &user, nil
	}
	// Proteger último admin: no se puede inactivar/bloquear al último admin activo
	if auth.NormalizeRole(user.Role) == auth.RoleAdmin && oldStatus == auth.StatusActive && newStatus != auth.StatusActive {
		isLast, err := isLastActiveAdmin(db, &user)
		if err != nil {
			return nil, err
		}
		if isLast {
			return nil, ErrLastAdmin
		}
	}
	user.Status = newStatus
	if err := db.Save(&user).Error; err != nil {
		return nil, err
	}
	// Si se desactiva/bloquea, revocar todas las sesiones del usuario
	if newStatus != auth.StatusActive {
		_, _ = auth.RevokeAllSessionsForUser(user.Username, rdb)
	}
	audit(db, actor, ActionStatusChange, &user.ID, user.Username, fmt.Sprintf("estado cambiado de %s a %s", oldStatus, newStatus))
	return &user, nil
}

func DeleteUser(db *gorm.DB, actor Actor, rdb *redis.Client, id uint) error {
	var user auth.UserModel
	if err := db.First(&user, id).Error; err != nil {
		return err
	}
	// Proteger último admin activo
	if auth.NormalizeRole(user.Role) == auth.RoleAdmin && auth.NormalizeStatus(user.Status) == auth.StatusActive {
		isLast, err := isLastActiveAdmin(db, &user)
		if err != nil {
			return err
		}
		if isLast {
			return ErrLastAdmin
		}
	}
	// Soft delete
	if err := db.Delete(&user).Error; err != nil {
		return err
	}
	// Revocar sesiones
	_, _ = auth.RevokeAllSessionsForUser(user.Username, rdb)
	audit(db, actor, ActionDelete, &user.ID, user.Username, "usuario eliminado (soft delete)")
	return nil
}

func RestoreUser(db *gorm.DB, actor Actor, id uint) (*auth.UserModel, error) {
	var user auth.UserModel
	if err := db.Unscoped().First(&user, id).Error; err != nil {
		return nil, err
	}
	if user.DeletedAt.Time.IsZero() {
		return nil, errors.New("el usuario no está eliminado")
	}
	if err := db.Unscoped().Model(&user).Update("deleted_at", nil).Error; err != nil {
		return nil, err
	}
	audit(db, actor, ActionRestore, &user.ID, user.Username, "usuario restaurado")
	return &user, nil
}

func ListAuditLogs(db *gorm.DB, f AuditFilter) ([]AuditLog, int64, error) {
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	query := db.Model(&AuditLog{})
	if f.Actor != "" {
		query = query.Where("actor_username ILIKE ?", "%"+f.Actor+"%")
	}
	if f.Target != "" {
		query = query.Where("target_username ILIKE ?", "%"+f.Target+"%")
	}
	if f.Action != "" {
		query = query.Where("action = ?", f.Action)
	}
	if f.From != "" {
		query = query.Where("created_at >= ?", f.From)
	}
	if f.To != "" {
		query = query.Where("created_at <= ?", f.To)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var logs []AuditLog
	if err := query.Order("created_at desc").Offset((f.Page - 1) * f.Limit).Limit(f.Limit).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

// Session helpers delegados a auth pero con auditoría

func RevokeUserSessions(db *gorm.DB, actor Actor, rdb *redis.Client, userID uint) (int, error) {
	user, err := GetUserByID(db, userID)
	if err != nil {
		return 0, err
	}
	count, err := auth.RevokeAllSessionsForUser(user.Username, rdb)
	if err != nil {
		return 0, err
	}
	audit(db, actor, ActionRevokeSessions, &user.ID, user.Username, fmt.Sprintf("sesiones revocadas: %d", count))
	return count, nil
}

func RevokeOneSession(db *gorm.DB, actor Actor, rdb *redis.Client, token string) error {
	// Intentar obtener username del token para auditoría, pero no es crítico
	username, _, _ := auth.ResolveSessionTokenWithRole(token, rdb)
	if err := auth.RevokeSession(token, rdb); err != nil {
		return err
	}
	var targetID *uint
	var targetUsername string
	if username != "" {
		targetUsername = username
		var u auth.UserModel
		if err := db.Where("username = ?", username).First(&u).Error; err == nil {
			targetID = &u.ID
		}
	}
	audit(db, actor, ActionRevokeOne, targetID, targetUsername, "sesión individual revocada")
	return nil
}

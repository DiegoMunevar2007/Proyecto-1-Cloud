package admin

import "github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/auth"

// RoleUpdateRequest contiene el nuevo rol para un usuario.
type RoleUpdateRequest struct {
	Role string `json:"role" example:"professor"`
}

// StatusUpdateRequest contiene el nuevo estado para un usuario.
type StatusUpdateRequest struct {
	Status string `json:"status" example:"inactive"`
}

// SessionTokenRequest transporta un token de sesión a revocar.
type SessionTokenRequest struct {
	Token string `json:"token" example:"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."`
}

// UserListResponse es la respuesta paginada del listado de usuarios.
type UserListResponse struct {
	Users []auth.UserResponse `json:"users"`
	Total int64               `json:"total" example:"42"`
	Page  int                 `json:"page" example:"1"`
	Limit int                 `json:"limit" example:"20"`
}

// UserDetailResponse envuelve un usuario en la respuesta.
type UserDetailResponse struct {
	User auth.UserResponse `json:"user"`
}

// UserMessageResponse combina mensaje informativo con el usuario afectado.
type UserMessageResponse struct {
	Message string            `json:"message" example:"Rol actualizado correctamente"`
	User    auth.UserResponse `json:"user"`
}

// SessionsResponse lista las sesiones activas de un usuario.
type SessionsResponse struct {
	Username string   `json:"username" example:"estudiante1"`
	Sessions []string `json:"sessions"`
	Count    int      `json:"count" example:"2"`
}

// RevokeSessionsResponse informa cuántas sesiones fueron revocadas.
type RevokeSessionsResponse struct {
	Message string `json:"message" example:"Sesiones revocadas"`
	Count   int    `json:"count" example:"2"`
}

// AuditLogResponse es la representación OpenAPI de un registro de auditoría.
type AuditLogResponse struct {
	ID             uint   `json:"id" example:"1"`
	CreatedAt      string `json:"created_at" example:"2026-09-10T02:00:00Z"`
	ActorID        *uint  `json:"actor_id" example:"1"`
	ActorUsername  string `json:"actor_username" example:"admin1"`
	Action         string `json:"action" example:"role_change"`
	TargetUserID   *uint  `json:"target_user_id" example:"5"`
	TargetUsername string `json:"target_username" example:"profe1"`
	IP             string `json:"ip" example:"172.18.0.1"`
	UserAgent      string `json:"user_agent" example:"Mozilla/5.0"`
	Detail         string `json:"detail" example:"rol cambiado de student a professor"`
}

// AuditListResponse es la respuesta paginada de la auditoría.
type AuditListResponse struct {
	Logs  []AuditLogResponse `json:"logs"`
	Total int64              `json:"total" example:"15"`
	Page  int                `json:"page" example:"1"`
	Limit int                `json:"limit" example:"20"`
}

// StatsResponse agrega conteos de usuarios por estado y rol.
type StatsResponse struct {
	Total        int64 `json:"total" example:"100"`
	Active       int64 `json:"active" example:"90"`
	Inactive     int64 `json:"inactive" example:"7"`
	Blocked      int64 `json:"blocked" example:"3"`
	AdminsActive int64 `json:"admins_active" example:"2"`
}

// ToUserResponses convierte una lista de modelos a su representación pública.
func ToUserResponses(users []auth.UserModel) []auth.UserResponse {
	out := make([]auth.UserResponse, 0, len(users))
	for _, u := range users {
		out = append(out, auth.ToUserResponse(u))
	}
	return out
}

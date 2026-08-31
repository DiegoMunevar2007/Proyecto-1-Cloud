package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/mail"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// sessionTTL es la duración de validez de un token JWT/sesión.
const sessionTTL = 24 * time.Hour

var ctx = context.Background()

// Claims define los datos incluidos en el JWT.
type Claims struct {
	Username string `json:"username"`
	UserID   uint   `json:"user_id"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

func getJWTSecret() []byte {
	return []byte(utils.GetJWTSecret())
}

func AuthenticateUser(username, password string, db *gorm.DB) bool {
	/*
		Autentica al usuario verificando su nombre de usuario y contraseña en la base de datos.
		Si la autenticación es exitosa y la cuenta está activa, devuelve true; de lo contrario, devuelve false.
		Las cuentas con status != active (inactive/blocked) se bloquean en el login.
	*/
	var user UserModel
	result := db.Where("username = ?", username).First(&user)
	if result.Error != nil {
		return false
	}
	// Bloquear login si la cuenta no está activa (soft-delete ya filtra DeletedAt).
	if NormalizeStatus(user.Status) != StatusActive {
		return false
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return false
	}
	return true
}

// AuthError define errores tipados para login bloqueado.
var (
	ErrAccountInactive = errors.New("cuenta desactivada")
	ErrAccountBlocked  = errors.New("cuenta bloqueada")
)

func AuthenticateUserDetailed(username, password string, db *gorm.DB) (bool, string) {
	/*
		Variante detallada que distingue motivo de bloqueo para responder 403.
		Retorna (ok, motivo) donde motivo es "", "inactive" o "blocked".
	*/
	var user UserModel
	result := db.Where("username = ?", username).First(&user)
	if result.Error != nil {
		return false, ""
	}
	status := NormalizeStatus(user.Status)
	if status == StatusInactive {
		return false, StatusInactive
	}
	if status == StatusBlocked {
		return false, StatusBlocked
	}
	if status != StatusActive {
		return false, status
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return false, ""
	}
	return true, ""
}

func GetUserID(username string, db *gorm.DB) (uint, error) {
	/*
		Obtiene el ID del usuario a partir de su nombre de usuario.
		Devuelve un error si el usuario no existe.
	*/
	var user UserModel
	if err := db.Where("username = ?", username).First(&user).Error; err != nil {
		return 0, err
	}
	return user.ID, nil
}

func hashPassword(password string) string {
	/*
		Hace el hasheo de la contraseña utilizando bcrypt y devuelve la contraseña hasheada.
	*/
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return string(hashedPassword)
}

func CreateSession(userID uint, db *gorm.DB, rdb *redis.Client) (string, error) {
	/*
		Crea un JWT para el usuario autenticado, lo almacena en Redis con expiración
		automática (sessionTTL) y devuelve el token firmado. El cliente debe enviarlo
		en el header Authorization ("Bearer <token>") en las siguientes peticiones.
		Implementa patrón multi-sesión: guarda tanto session:<jwt> -> username como
		índice invertido user_sessions:<username> (Sorted Set con score=exp).
	*/
	var user UserModel
	if err := db.First(&user, userID).Error; err != nil {
		return "", errors.New("usuario no encontrado")
	}
	if NormalizeStatus(user.Status) != StatusActive {
		return "", errors.New("cuenta no activa")
	}

	now := time.Now()
	exp := now.Add(sessionTTL)
	claims := Claims{
		Username: user.Username,
		UserID:   userID,
		Role:     user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprint(userID),
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString(getJWTSecret())
	if err != nil {
		return "", errors.New("no se pudo firmar el token: " + err.Error())
	}

	// Almacenar en Redis con TTL para expiración automática y permitir revocación.
	if err := rdb.Set(ctx, "session:"+signedToken, user.Username, sessionTTL).Err(); err != nil {
		return "", errors.New("no se pudo guardar la sesión en Redis: " + err.Error())
	}
	// Índice invertido multi-sesión: user_sessions:<username> Sorted Set score=expUnix
	zKey := "user_sessions:" + user.Username
	if err := rdb.ZAdd(ctx, zKey, redis.Z{Score: float64(exp.Unix()), Member: signedToken}).Err(); err != nil {
		// Limpiar la clave principal si falla el índice
		_ = rdb.Del(ctx, "session:"+signedToken).Err()
		return "", errors.New("no se pudo indexar la sesión en Redis: " + err.Error())
	}
	// Asegurar expiración del índice (ligeramente mayor que TTL para permitir limpieza perezosa)
	_ = rdb.Expire(ctx, zKey, sessionTTL+time.Hour).Err()

	return signedToken, nil
}

func ResolveSessionTokenWithRole(tokenString string, rdb *redis.Client) (string, string, error) {
	/*
		Valida un JWT verificando firma, expiración y existencia en Redis.
		Si es válido devuelve el username y el rol; en caso contrario retorna error genérico.
		El rol proviene del claim del JWT (firmado) para que RequireAuth siga siendo solo Redis.
	*/
	// 1. Verificar que exista en Redis (no revocado y no expirado por TTL).
	username, err := rdb.Get(ctx, "session:"+tokenString).Result()
	if err != nil {
		if err == redis.Nil {
			return "", "", errors.New("token de sesión inválido o expirado")
		}
		return "", "", errors.New("error al consultar la sesión: " + err.Error())
	}

	// 2. Verificar firma y expiración del JWT.
	claims := &Claims{}
	parsed, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("método de firma no soportado")
		}
		return getJWTSecret(), nil
	})
	if err != nil || !parsed.Valid {
		return "", "", errors.New("token de sesión inválido o expirado")
	}

	// Coherencia opcional: el username del JWT debe coincidir con el de Redis.
	if claims.Username != "" && claims.Username != username {
		return "", "", errors.New("token de sesión inválido o expirado")
	}

	return username, claims.Role, nil
}

func ResolveSessionToken(tokenString string, rdb *redis.Client) (string, error) {
	/*
		Valida un JWT verificando firma, expiración y existencia en Redis.
		Si es válido devuelve el username; en caso contrario retorna error genérico.
		Wrapper sobre ResolveSessionTokenWithRole para compatibilidad.
	*/
	username, _, err := ResolveSessionTokenWithRole(tokenString, rdb)
	return username, err
}

func DeleteSession(tokenString string, rdb *redis.Client) error {
	/*
		Invalida un token de sesión (cierre de sesión) eliminándolo de Redis.
		Es idempotente: no retorna error si el token ya no existe.
		Limpia tanto session:<jwt> como el índice user_sessions:<username>.
	*/
	if tokenString == "" {
		return errors.New("token vacío")
	}
	// Intentar obtener username para limpiar índice invertido (best-effort).
	// Si ya expiró, Get fallará y solo se hace Del de la clave principal.
	if username, err := rdb.Get(ctx, "session:"+tokenString).Result(); err == nil && username != "" {
		_ = rdb.ZRem(ctx, "user_sessions:"+username, tokenString).Err()
	} else {
		// Fallback: si no está en Redis, decodificar JWT sin validar para extraer username y limpiar índice.
		claims := &Claims{}
		if _, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
			return getJWTSecret(), nil
		}); err == nil && claims.Username != "" {
			_ = rdb.ZRem(ctx, "user_sessions:"+claims.Username, tokenString).Err()
		}
	}
	if err := rdb.Del(ctx, "session:"+tokenString).Err(); err != nil {
		return errors.New("no se pudo eliminar la sesión: " + err.Error())
	}
	return nil
}

// RevokeAllSessionsForUser revoca todas las sesiones activas de un usuario (multi-sesión).
func RevokeAllSessionsForUser(username string, rdb *redis.Client) (int, error) {
	zKey := "user_sessions:" + username
	tokens, err := rdb.ZRange(ctx, zKey, 0, -1).Result()
	if err != nil && err != redis.Nil {
		return 0, err
	}
	if len(tokens) == 0 {
		return 0, nil
	}
	pipe := rdb.Pipeline()
	for _, t := range tokens {
		pipe.Del(ctx, "session:"+t)
	}
	pipe.Del(ctx, zKey)
	_, err = pipe.Exec(ctx)
	if err != nil {
		return 0, err
	}
	return len(tokens), nil
}

// ListSessionsForUser lista los JWT activos de un usuario, purgando expirados perezosamente.
func ListSessionsForUser(username string, rdb *redis.Client) ([]string, error) {
	zKey := "user_sessions:" + username
	// Purga expirados por score
	now := float64(time.Now().Unix())
	_ = rdb.ZRemRangeByScore(ctx, zKey, "-inf", fmt.Sprint(now)).Err()
	// Filtrar solo tokens cuya clave session:<jwt> aún existe (por si TTL expiró antes que ZSet)
	members, err := rdb.ZRange(ctx, zKey, 0, -1).Result()
	if err != nil {
		if err == redis.Nil {
			return []string{}, nil
		}
		return nil, err
	}
	active := make([]string, 0, len(members))
	for _, t := range members {
		exists, _ := rdb.Exists(ctx, "session:"+t).Result()
		if exists == 1 {
			active = append(active, t)
		} else {
			// Limpieza perezosa: remover huérfano
			_ = rdb.ZRem(ctx, zKey, t).Err()
		}
	}
	return active, nil
}

// RevokeSession es un alias de DeleteSession para el endpoint /revoke-session.
// Se expone sin RequireAuth solo para pruebas, según lo solicitado.
func RevokeSession(tokenString string, rdb *redis.Client) error {
	return DeleteSession(tokenString, rdb)
}

func RegisterUser(username string, email string, password string, role string, db *gorm.DB, rdb *redis.Client) (string, int) {
	/*
		Registra un nuevo usuario con el rol especificado. Valida el rol,
		normalizándolo a minúsculas. Si el rol es inválido retorna 400.
		Si el usuario/email ya existe retorna 409. En éxito 201.
		La verificación por correo se mantiene igual.
	*/
	role = NormalizeRole(role)
	if !IsValidRole(role) {
		return "Rol inválido. Roles permitidos: student, professor, admin", 400
	}

	var existing UserModel
	result := db.Where("username = ? OR email = ?", username, email).First(&existing)
	if result.Error == nil {
		return "El nombre de usuario o correo electrónico ya está en uso", 409
	}
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return "Error al verificar el usuario: " + result.Error.Error(), 500
	}

	hashedPassword := hashPassword(password)
	user := UserModel{Username: username, Email: email, Password: hashedPassword, Role: role}
	if err := db.Create(&user).Error; err != nil {
		return "Error al crear el usuario: " + err.Error(), 500
	}

	if err := SendVerificationCode(username, email, rdb); err != nil {
		return "Usuario " + username + " registrado, pero error al enviar el correo de verificación: " + err.Error(), 201
	}
	return "Usuario " + username + " registrado exitosamente con rol " + role, 201
}

func SendVerificationCode(username string, email string, rdb *redis.Client) error {
	// Generar un código de verificación de 6 dígitos para el usuario recién registrado
	verificationCode := uuid.New().String()[:6] // Tomamos los primeros 6 caracteres del UUID como código de verificación

	// Guardar el código de verificación en Redis con un tiempo de expiración de 10 minutos
	err := rdb.Set(ctx, "verification:"+username, verificationCode, 10*time.Minute).Err()
	if err != nil {
		return errors.New("Error al guardar el código de verificación: " + err.Error())
	}

	// Enviar el correo con el código de verificación
	err = mail.SendEmail(email, "Verificación de cuenta", "Hola "+username+", \n\nEste es tu código de verificación: "+verificationCode)
	if err != nil {
		return errors.New("Error al enviar el correo de verificación: " + err.Error())
	}
	return nil
}

func VerifyUser(username string, verificationCode string, db *gorm.DB, rdb *redis.Client) error {
	/*
		Verifica la identidad de un usuario a partir de su nombre de usuario y un código de verificación.
		Devuelve un error si el usuario no existe o si el código de verificación es inválido.
	*/
	var user UserModel
	if err := db.Where("username = ?", username).First(&user).Error; err != nil {
		return err
	}
	if user.IsVerified {
		return errors.New("usuario ya verificado")
	}

	obtainedCode, err := rdb.Get(ctx, "verification:"+username).Result()
	if err != nil {
		return errors.New("Error al obtener el código de verificación: " + err.Error())
	}
	if obtainedCode != verificationCode {
		return errors.New("El código de verificación es incorrecto")
	}

	if err := rdb.Del(ctx, "verification:"+username).Err(); err != nil {
		return errors.New("no se pudo eliminar el código de verificación")
	}

	user.IsVerified = true
	if err := db.Save(&user).Error; err != nil {
		return errors.New("no se pudo verificar al usuario")
	}
	return nil
}

func RecoverPassword(username string, db *gorm.DB, rdb *redis.Client) error {
	/*
		Genera un código de recuperación de contraseña para el usuario especificado y lo envía por correo electrónico.
		El código de recuperación se guarda en Redis con un tiempo de expiración de 10 minutos.
	*/
	var user UserModel
	if err := db.Where("username = ?", username).First(&user).Error; err != nil {
		return errors.New("usuario no encontrado")
	}

	recoveryCode := uuid.New().String()[:6] // Tomamos los primeros 6 caracteres del UUID como código de recuperación

	err := rdb.Set(ctx, "recovery:"+username, recoveryCode, 10*time.Minute).Err()
	if err != nil {
		return errors.New("error al guardar el código de recuperación: " + err.Error())
	}

	err = mail.SendEmail(user.Email, "Recuperación de contraseña", "Hola "+username+", \n\nEste es tu código de recuperación: "+recoveryCode)
	if err != nil {
		return errors.New("error al enviar el correo de recuperación: " + err.Error())
	}

	return nil
}

func ResetPassword(username string, recoveryCode string, newPassword string, db *gorm.DB, rdb *redis.Client) error {
	/*
		Restablece la contraseña del usuario especificado si el código de recuperación es válido.
		La nueva contraseña se hashea antes de almacenarla en la base de datos.
	*/
	var user UserModel
	if err := db.Where("username = ?", username).First(&user).Error; err != nil {
		return errors.New("usuario no encontrado")
	}

	obtainedCode, err := rdb.Get(ctx, "recovery:"+username).Result()
	if err != nil {
		return errors.New("error al obtener el código de recuperación: " + err.Error())
	}
	if obtainedCode != recoveryCode {
		return errors.New("el código de recuperación es incorrecto")
	}

	if err := rdb.Del(ctx, "recovery:"+username).Err(); err != nil {
		return errors.New("no se pudo eliminar el código de recuperación")
	}

	hashedPassword := hashPassword(newPassword)
	user.Password = hashedPassword
	if err := db.Save(&user).Error; err != nil {
		return errors.New("no se pudo restablecer la contraseña")
	}
	return nil
}

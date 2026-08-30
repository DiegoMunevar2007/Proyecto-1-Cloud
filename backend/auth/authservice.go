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
		Si la autenticación es exitosa, devuelve true; de lo contrario, devuelve false.
	*/
	var user UserModel
	result := db.Where("username = ?", username).First(&user)
	if result.Error != nil {
		return false
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return false
	}
	return true
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
	*/
	var user UserModel
	if err := db.First(&user, userID).Error; err != nil {
		return "", errors.New("usuario no encontrado")
	}

	now := time.Now()
	claims := Claims{
		Username: user.Username,
		UserID:   userID,
		Role:     user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprint(userID),
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(sessionTTL)),
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
	*/
	if tokenString == "" {
		return errors.New("token vacío")
	}
	if err := rdb.Del(ctx, "session:"+tokenString).Err(); err != nil {
		return errors.New("no se pudo eliminar la sesión: " + err.Error())
	}
	return nil
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

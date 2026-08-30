package mail

import (
	"net/smtp"
)

func SendEmail(to, subject, body string) error {
	// Configuración del servidor SMTP
	smtpHost := "smtp.gmail.com"
	smtpPort := "587"
	smtpUser := "algo@algo.com"
	smtpPass := "contrasenia"

	// Configuración del mensaje
	msg := "From: " + smtpUser + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=\"UTF-8\"\r\n\r\n" +
		body

	// Autenticación y envío del correo
	auth := smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)
	err := smtp.SendMail(smtpHost+":"+smtpPort, auth, smtpUser, []string{to}, []byte(msg))
	if err != nil {
		return err
	}
	return nil
}

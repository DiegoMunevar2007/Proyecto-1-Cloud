package mail

import (
	"net/smtp"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
)

func SendEmail(to, subject, body string) error {
	// Configuración del servidor SMTP
	smtpHost, smtpPort, smtpUser := utils.GetSMTPConfig()
	// Configuración del mensaje
	msg := "From: " + smtpUser + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=\"UTF-8\"\r\n\r\n" +
		body

	// Autenticación y envío del correo
	err := smtp.SendMail(smtpHost+":"+smtpPort, nil, smtpUser, []string{to}, []byte(msg))
	if err != nil {
		return err
	}
	return nil
}

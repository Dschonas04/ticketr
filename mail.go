package main

import (
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Mailer schickt Antworten an die meldende Person. Ohne TICKETR_SMTP gibt es
// keinen, und die Oberfläche bietet das Senden nicht an.
type Mailer struct {
	server, nutzer, kennwort, absender string
}

func neuerMailer() *Mailer {
	s := umgebung("TICKETR_SMTP", "")
	if s == "" {
		return nil
	}
	m := &Mailer{server: s, nutzer: umgebung("TICKETR_SMTP_NUTZER", ""),
		kennwort: umgebung("TICKETR_SMTP_KENNWORT", ""), absender: umgebung("TICKETR_ABSENDER", "")}
	if m.absender == "" {
		m.absender = m.nutzer
	}
	return m
}

func (m *Mailer) senden(an, betreff, text string) error {
	if strings.ContainsAny(an, "\r\n") || strings.ContainsAny(betreff, "\r\n") {
		return fmt.Errorf("ungültige Adresse oder Betreff")
	}
	host, _, err := net.SplitHostPort(m.server)
	if err != nil {
		return err
	}
	var auth smtp.Auth
	if m.nutzer != "" {
		auth = smtp.PlainAuth("", m.nutzer, m.kennwort, host)
	}
	nachricht := "From: " + m.absender + "\r\n" +
		"To: " + an + "\r\n" +
		"Subject: " + mime.QEncoding.Encode("utf-8", betreff) + "\r\n" +
		"Date: " + time.Now().Format(time.RFC1123Z) + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" +
		strings.ReplaceAll(text, "\n", "\r\n") + "\r\n"
	return smtp.SendMail(m.server, auth, m.absender, []string{an}, []byte(nachricht))
}

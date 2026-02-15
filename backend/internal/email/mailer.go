package email

import (
	"fmt"
	"log/slog"
	"net/smtp"
	"os"
	"strings"
)

// Mailer handles sending verification and notification emails via SMTP.
type Mailer struct {
	host     string
	port     string
	user     string
	password string
	sender   string
	log      *slog.Logger
}

// NewMailer initializes a Mailer reading from environment variables.
func NewMailer(log *slog.Logger) *Mailer {
	if log == nil {
		log = slog.Default()
	}
	host := getEnvOrDefault("SMTP_HOST", "")
	port := getEnvOrDefault("SMTP_PORT", "587")
	user := getEnvOrDefault("SMTP_USER", "")
	password := getEnvOrDefault("SMTP_PASS", "")
	sender := getEnvOrDefault("SMTP_SENDER", user)

	return &Mailer{
		host:     host,
		port:     port,
		user:     user,
		password: password,
		sender:   sender,
		log:      log,
	}
}

// SendVerificationEmail dispatches a 6-digit verification code HTML email matching Precision Architectural theme.
func (m *Mailer) SendVerificationEmail(toEmail, code string) error {
	if m.host == "" || m.user == "" || m.password == "" {
		m.log.Warn("SMTP not fully configured; logging verification code for dev testing",
			"to", toEmail, "verification_code", code)
		return nil
	}

	subject := "Verify Your Blob-Cloud Account"
	body := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s</title>
</head>
<body style="margin:0; padding:0; background-color:#090a0c; font-family:'Plus Jakarta Sans', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; color:#f4f4f5;">
  <table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0" style="background-color:#090a0c; padding: 40px 16px;">
    <tr>
      <td align="center">
        <table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0" style="max-width:480px; background-color:#111317; border:1px solid #242830; border-radius:8px; padding:36px 28px; text-align:center;">
          <tr>
            <td>
              <div style="display:inline-block; background-color:#f59e0b; color:#090a0c; font-weight:900; font-size:18px; padding:6px 12px; border-radius:4px; margin-bottom:20px; font-family:'Syne', sans-serif;">Blob-Cloud</div>
              <h1 style="font-size:20px; font-weight:700; color:#ffffff; margin:0 0 12px 0; font-family:'Syne', sans-serif;">Email Verification</h1>
              <p style="font-size:13px; color:#a1a1aa; line-height:1.6; margin:0 0 24px 0;">Please enter the following 6-digit verification code to complete your registration. This code expires in 15 minutes.</p>
              <div style="background-color:#15181e; border:1px solid #282e3b; border-radius:6px; padding:16px 24px; font-size:32px; font-weight:800; letter-spacing:8px; color:#fbbf24; margin-bottom:24px; display:inline-block; font-family:'JetBrains Mono', Consolas, monospace;">%s</div>
              <p style="font-size:12px; color:#71717a; line-height:1.5; margin:0 0 24px 0;">If you did not request this code, you can safely ignore this email.</p>
              <div style="font-size:11px; color:#52525b; border-top:1px solid #242830; padding-top:20px; margin-top:24px; font-family:'JetBrains Mono', monospace;">&copy; Blob-Cloud Cloud Storage • Precision Architectural</div>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, subject, code)

	mime := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"
	msg := []byte(fmt.Sprintf("From: %s\nTo: %s\nSubject: %s\n%s%s", m.sender, toEmail, subject, mime, body))

	auth := smtp.PlainAuth("", m.user, m.password, m.host)
	addr := fmt.Sprintf("%s:%s", m.host, m.port)

	err := smtp.SendMail(addr, auth, m.sender, []string{toEmail}, msg)
	if err != nil {
		m.log.Error("failed to send verification email via SMTP", "to", toEmail, "err", err)
		return fmt.Errorf("send mail: %w", err)
	}

	m.log.Info("verification email sent successfully", "to", toEmail)
	return nil
}

// SendPasswordResetEmail dispatches a password recovery HTML email matching Precision Architectural theme.
func (m *Mailer) SendPasswordResetEmail(toEmail, resetLink string) error {
	if m.host == "" || m.user == "" || m.password == "" {
		m.log.Warn("SMTP not fully configured; logging password reset link for dev testing",
			"to", toEmail, "reset_link", resetLink)
		return nil
	}

	subject := "Reset Your Blob-Cloud Password"
	body := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s</title>
</head>
<body style="margin:0; padding:0; background-color:#090a0c; font-family:'Plus Jakarta Sans', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; color:#f4f4f5;">
  <table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0" style="background-color:#090a0c; padding: 40px 16px;">
    <tr>
      <td align="center">
        <table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0" style="max-width:480px; background-color:#111317; border:1px solid #242830; border-radius:8px; padding:36px 28px; text-align:center;">
          <tr>
            <td>
              <div style="display:inline-block; background-color:#f59e0b; color:#090a0c; font-weight:900; font-size:18px; padding:6px 12px; border-radius:4px; margin-bottom:20px; font-family:'Syne', sans-serif;">Blob-Cloud</div>
              <h1 style="font-size:20px; font-weight:700; color:#ffffff; margin:0 0 12px 0; font-family:'Syne', sans-serif;">Password Recovery Request</h1>
              <p style="font-size:13px; color:#a1a1aa; line-height:1.6; margin:0 0 28px 0;">We received a request to reset your password. Click the button below to choose a new password. This link expires in 15 minutes.</p>
              <div style="margin-bottom:28px;">
                <!--[if mso]>
                <v:roundrect xmlns:v="urn:schemas-microsoft-com:vml" xmlns:w="urn:schemas-microsoft-com:office:word" href="%s" style="height:40px;v-text-anchor:middle;width:200px;" arcsize="10%%" stroke="f" fillcolor="#f59e0b">
                  <w:anchorlock/>
                  <center style="color:#090a0c;font-family:sans-serif;font-size:14px;font-weight:bold;">Reset Password</center>
                </v:roundrect>
                <![endif]-->
                <a href="%s" target="_blank" style="background-color:#f59e0b; color:#090a0c !important; font-size:13px; font-weight:700; text-decoration:none; padding:12px 28px; border-radius:6px; display:inline-block;">Reset Password</a>
              </div>
              <p style="font-size:12px; color:#71717a; margin:0 0 8px 0;">Or copy and paste this link into your browser:</p>
              <div style="background-color:#15181e; border:1px solid #282e3b; border-radius:6px; padding:12px; margin-bottom:24px; text-align:left; word-break:break-all;">
                <a href="%s" style="color:#fbbf24 !important; font-size:11px; text-decoration:underline; font-family:'JetBrains Mono', Consolas, monospace;">%s</a>
              </div>
              <p style="font-size:12px; color:#71717a; line-height:1.5; margin:0 0 20px 0;">If you did not request a password reset, you can safely ignore this email.</p>
              <div style="font-size:11px; color:#52525b; border-top:1px solid #242830; padding-top:20px; margin-top:24px; font-family:'JetBrains Mono', monospace;">&copy; Blob-Cloud Cloud Storage • Precision Architectural</div>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, subject, resetLink, resetLink, resetLink, resetLink)

	mime := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"
	msg := []byte(fmt.Sprintf("From: %s\nTo: %s\nSubject: %s\n%s%s", m.sender, toEmail, subject, mime, body))

	auth := smtp.PlainAuth("", m.user, m.password, m.host)
	addr := fmt.Sprintf("%s:%s", m.host, m.port)

	err := smtp.SendMail(addr, auth, m.sender, []string{toEmail}, msg)
	if err != nil {
		m.log.Error("failed to send password reset email via SMTP", "to", toEmail, "err", err)
		return fmt.Errorf("send password reset mail: %w", err)
	}

	m.log.Info("password reset email sent successfully", "to", toEmail)
	return nil
}

// SendShareNotificationEmail dispatches a file share notification email.
func (m *Mailer) SendShareNotificationEmail(toEmail, sharedBy, filename, role, link string) error {
	if m.host == "" || m.user == "" || m.password == "" {
		m.log.Warn("SMTP not fully configured; logging share notification for dev testing",
			"to", toEmail, "shared_by", sharedBy, "filename", filename, "role", role, "link", link)
		return nil
	}

	subject := fmt.Sprintf("%s shared a file with you: %s", sharedBy, filename)
	body := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s</title>
</head>
<body style="margin:0; padding:0; background-color:#090a0c; font-family:'Plus Jakarta Sans', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; color:#f4f4f5;">
  <table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0" style="background-color:#090a0c; padding: 40px 16px;">
    <tr>
      <td align="center">
        <table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0" style="max-width:480px; background-color:#111317; border:1px solid #242830; border-radius:8px; padding:36px 28px; text-align:center;">
          <tr>
            <td>
              <div style="display:inline-block; background-color:#f59e0b; color:#090a0c; font-weight:900; font-size:18px; padding:6px 12px; border-radius:4px; margin-bottom:20px; font-family:'Syne', sans-serif;">Blob-Cloud</div>
              <h1 style="font-size:20px; font-weight:700; color:#ffffff; margin:0 0 12px 0; font-family:'Syne', sans-serif;">File Shared With You</h1>
              <p style="font-size:13px; color:#a1a1aa; line-height:1.6; margin:0 0 28px 0;"><strong>%s</strong> has shared the file "<strong>%s</strong>" with you. You have been granted <strong>%s</strong> access.</p>
              <div style="margin-bottom:28px;">
                <a href="%s" target="_blank" style="background-color:#f59e0b; color:#090a0c !important; font-size:13px; font-weight:700; text-decoration:none; padding:12px 28px; border-radius:6px; display:inline-block;">Open File</a>
              </div>
              <div style="font-size:11px; color:#52525b; border-top:1px solid #242830; padding-top:20px; margin-top:24px; font-family:'JetBrains Mono', monospace;">&copy; Blob-Cloud Cloud Storage • Precision Architectural</div>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, subject, sharedBy, filename, role, link)

	mime := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"
	msg := []byte(fmt.Sprintf("From: %s\nTo: %s\nSubject: %s\n%s%s", m.sender, toEmail, subject, mime, body))

	auth := smtp.PlainAuth("", m.user, m.password, m.host)
	addr := fmt.Sprintf("%s:%s", m.host, m.port)

	err := smtp.SendMail(addr, auth, m.sender, []string{toEmail}, msg)
	if err != nil {
		m.log.Error("failed to send share notification email via SMTP", "to", toEmail, "err", err)
		return fmt.Errorf("send share notification mail: %w", err)
	}

	m.log.Info("share notification email sent successfully", "to", toEmail)
	return nil
}

func getEnvOrDefault(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

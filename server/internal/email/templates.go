package email

import "fmt"

// brandedWrap wraps email content in the Digits branded template.
func brandedWrap(content string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"></head>
<body style="margin:0; padding:0; background-color:#0f1117; font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;">
<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#0f1117; padding:40px 20px;">
<tr><td align="center">
<table width="480" cellpadding="0" cellspacing="0" style="max-width:480px; width:100%%.">

<!-- Header -->
<tr><td style="padding:0 0 24px 0;">
  <span style="font-size:14px; font-weight:700; letter-spacing:3px; color:#58a6ff;">&#9742; DIGITS</span>
</td></tr>

<!-- Content -->
<tr><td style="background-color:#161b22; border:1px solid #21262d; border-radius:12px; padding:32px;">
%s
</td></tr>

<!-- Footer -->
<tr><td style="padding:24px 0 0 0; text-align:center;">
  <p style="color:#484f58; font-size:12px; margin:0;">Digits &#8212; A phone for real conversations</p>
  <p style="color:#484f58; font-size:11px; margin:4px 0 0 0;">No screens. No surveillance. Just voice.</p>
</td></tr>

</table>
</td></tr>
</table>
</body>
</html>`, content)
}

// MagicLinkEmail returns subject and HTML body for a magic link sign-in email.
func MagicLinkEmail(link string) (subject, body string) {
	subject = "Sign in to Digits"
	content := fmt.Sprintf(`<h2 style="color:#e6edf3; margin:0 0 16px 0; font-size:20px;">Sign in to Digits</h2>
  <p style="color:#8b949e; font-size:14px; margin:0 0 24px 0;">Click the button below to sign in. This link expires in 15 minutes.</p>
  <p style="text-align:center; margin:0 0 24px 0;">
    <a href="%s" style="display:inline-block; background-color:#1f6feb; color:#ffffff; text-decoration:none; padding:12px 24px; border-radius:6px; font-size:14px; font-weight:600;">Sign In</a>
  </p>
  <p style="color:#484f58; font-size:12px; margin:0;">If you didn&#39;t request this, you can safely ignore this email.</p>`, link)
	body = brandedWrap(content)
	return
}

// ContactInviteEmail returns subject and HTML body for a contact invite notification.
func ContactInviteEmail(fromPhoneName, toPhoneName, baseURL string) (subject, body string) {
	subject = fmt.Sprintf("Digits: Contact request for %s", toPhoneName)
	content := fmt.Sprintf(`<h2 style="color:#e6edf3; margin:0 0 16px 0; font-size:20px;">New Contact Request</h2>
  <p style="color:#8b949e; font-size:14px; margin:0 0 16px 0;"><strong>%s</strong> wants to add <strong>%s</strong> as a contact on Digits.</p>
  <p style="color:#8b949e; font-size:14px; margin:0;">Review and respond at <a href="%s/contacts" style="color:#58a6ff;">%s/contacts</a>.</p>`,
		fromPhoneName, toPhoneName, baseURL, baseURL)
	body = brandedWrap(content)
	return
}

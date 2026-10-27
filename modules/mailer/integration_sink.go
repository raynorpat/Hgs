//go:build integration

package mailer

// CaptureIntegrationMail replaces only the in-process queue in isolated integration
// fixtures. No SMTP worker starts. Consume expected messages before restoring.
func CaptureIntegrationMail() (<-chan *Message, func()) {
	previous := mailQueue
	captured := make(chan *Message, 100)
	mailQueue = captured
	return captured, func() { mailQueue = previous }
}

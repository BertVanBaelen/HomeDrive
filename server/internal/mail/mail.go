// Package mail sends Homedrive's emails: login codes and invitations.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/config"
)

type Message struct {
	To      string
	Subject string
	Body    string // plain text
}

type Sender interface {
	Send(ctx context.Context, m Message) error
}

// New returns the SMTP sender, or with SMTP not configured (allowed only for
// a localhost public_url) a sender that writes messages to the log.
func New(cfg config.SMTP, log *slog.Logger) Sender {
	if !cfg.Configured() {
		return logSender{log}
	}
	return &smtpSender{cfg: cfg}
}

type logSender struct{ log *slog.Logger }

func (l logSender) Send(ctx context.Context, m Message) error {
	l.log.Warn("email not sent (SMTP not configured)", "to", m.To, "subject", m.Subject, "body", m.Body)
	return nil
}

type smtpSender struct{ cfg config.SMTP }

const smtpTimeout = 30 * time.Second

func (s *smtpSender) Send(ctx context.Context, m Message) error {
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return fmt.Errorf("recipient: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, smtpTimeout)
	defer cancel()

	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	tlsConfig := &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	dialer := &net.Dialer{}
	if s.cfg.Security == config.SMTPTLS {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", addr, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if s.cfg.Security == config.SMTPStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("server does not offer STARTTLS")
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if s.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("authenticating: %w", err)
		}
	}
	if err := c.Mail(s.cfg.From.Address); err != nil {
		return err
	}
	if err := c.Rcpt(to.Address); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(s.compose(to, m)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func (s *smtpSender) compose(to *mail.Address, m Message) []byte {
	var b bytes.Buffer
	domain := s.cfg.From.Address[strings.LastIndexByte(s.cfg.From.Address, '@')+1:]
	header := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	header("From", s.cfg.From.String())
	header("To", to.String())
	header("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header("Date", time.Now().Format(time.RFC1123Z))
	header("Message-ID", "<"+rand.Text()+"@"+domain+">")
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "8bit")
	header("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\n", "\r\n"))
	return b.Bytes()
}

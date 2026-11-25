package core

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// Message is one reminder or alert.
type Message struct {
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	Link    string   `json:"link"`
	Actions []Action `json:"actions,omitempty"` // buttons on phone push (ntfy)
}

// Action is a one-tap button that POSTs to URL.
type Action struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Channels a user can switch on. "inapp" (bell) and "device" (Windows toast / browser pop-up) need no setup.
var Channels = []string{"inapp", "device", "ntfy", "email", "sms", "webhook"}

// Notifier sends messages over the outside channels (ntfy, email, SMS, webhooks).
type Notifier struct {
	Store  *Store
	Client *http.Client
}

// NewNotifier makes a Notifier with sane timeouts.
func NewNotifier(s *Store) *Notifier {
	return &Notifier{Store: s, Client: &http.Client{Timeout: 20 * time.Second}}
}

// Deliver sends m to every outside channel the user switched on and returns a short result per channel.
func (n *Notifier) Deliver(u *User, m Message) string {
	cfg := n.Store.Settings(false)
	var res []string
	try := func(name string, f func() error) {
		if err := f(); err != nil {
			res = append(res, name+": "+err.Error())
		} else {
			res = append(res, name+": ok")
		}
	}
	if u.HasChannel("ntfy") && u.NtfyTopic != "" {
		try("ntfy", func() error { return n.ntfy(cfg, u.NtfyTopic, m) })
	}
	if u.HasChannel("email") && u.Email != "" {
		try("email", func() error { return n.email(cfg, u.Email, m, false) })
	}
	if u.HasChannel("sms") {
		try("sms", func() error { return n.sms(cfg, u, m) })
	}
	if u.HasChannel("webhook") && u.WebhookURL != "" {
		try("webhook", func() error { return n.webhook(u.WebhookURL, u.DisplayName, m) })
	}
	return strings.Join(res, "; ")
}

// Household posts to the household-wide webhook (for example a family Discord channel), if one is set.
func (n *Notifier) Household(m Message) error {
	h := n.Store.Setting("household_webhook")
	if h == "" {
		return nil
	}
	return n.webhook(h, "", m)
}

func (n *Notifier) post(req *http.Request) error {
	resp, err := n.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func (n *Notifier) ntfy(cfg map[string]string, topic string, m Message) error {
	server := strings.TrimRight(cfg["ntfy_server"], "/")
	if server == "" {
		server = "https://ntfy.sh"
	}
	req, err := http.NewRequest("POST", server+"/"+url.PathEscape(topic), strings.NewReader(m.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", m.Title)
	req.Header.Set("Tags", "calendar")
	if m.Link != "" {
		req.Header.Set("Click", m.Link)
	}
	var acts []string
	for _, a := range m.Actions {
		if len(acts) < 3 && !strings.ContainsAny(a.Label+a.URL, ",;\r\n") {
			acts = append(acts, "http, "+a.Label+", "+a.URL+", method=POST, clear=true")
		}
	}
	if len(acts) > 0 {
		req.Header.Set("Actions", strings.Join(acts, "; "))
	}
	if t := cfg["ntfy_token"]; t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	return n.post(req)
}

func (n *Notifier) webhook(hook, who string, m Message) error {
	var body []byte
	if strings.Contains(hook, "discord.com/api/webhooks") || strings.Contains(hook, "discordapp.com/api/webhooks") {
		txt := "**" + m.Title + "**"
		if m.Body != "" {
			txt += "\n" + m.Body
		}
		if who != "" {
			txt += "\n_for " + who + "_"
		}
		body, _ = json.Marshal(map[string]string{"content": txt})
	} else {
		body, _ = json.Marshal(map[string]string{"title": m.Title, "body": m.Body, "link": m.Link, "for": who, "text": m.Title + ": " + m.Body})
	}
	req, err := http.NewRequest("POST", hook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return n.post(req)
}

// sms texts the user's phone through Twilio when it is set up, else through the user's carrier email-to-text address.
func (n *Notifier) sms(cfg map[string]string, u *User, m Message) error {
	text := m.Title
	if m.Body != "" {
		text += " - " + m.Body
	}
	if len(text) > 300 {
		text = text[:300]
	}
	if cfg["twilio_sid"] != "" && cfg["twilio_token"] != "" && cfg["twilio_from"] != "" && u.Phone != "" {
		form := url.Values{"To": {u.Phone}, "From": {cfg["twilio_from"]}, "Body": {text}}
		req, err := http.NewRequest("POST", "https://api.twilio.com/2010-04-01/Accounts/"+url.PathEscape(cfg["twilio_sid"])+"/Messages.json", strings.NewReader(form.Encode()))
		if err != nil {
			return err
		}
		req.SetBasicAuth(cfg["twilio_sid"], cfg["twilio_token"])
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return n.post(req)
	}
	if u.SMSGateway != "" {
		return n.email(cfg, u.SMSGateway, Message{Title: "", Body: text}, true)
	}
	return errors.New("no Twilio settings and no email-to-text address")
}

// email sends a plain-text mail. Port 465 uses TLS from the start, other ports use STARTTLS when offered.
func (n *Notifier) email(cfg map[string]string, to string, m Message, short bool) error {
	host, port, from := cfg["smtp_host"], cfg["smtp_port"], cfg["smtp_from"]
	if host == "" {
		return errors.New("SMTP is not set up (Settings > Household)")
	}
	if port == "" {
		port = "587"
	}
	if from == "" {
		from = cfg["smtp_user"]
	}
	if strings.ContainsAny(to, "\r\n") || strings.ContainsAny(from, "\r\n") {
		return errors.New("bad address")
	}
	subject := strings.NewReplacer("\r", " ", "\n", " ").Replace(m.Title)
	if subject == "" && !short {
		subject = "Household Hub"
	}
	body := m.Body
	if m.Link != "" && !short {
		body += "\n\n" + m.Link
	}
	msg := "From: " + from + "\r\nTo: " + to + "\r\n"
	if subject != "" {
		msg += "Subject: " + subject + "\r\n"
	}
	msg += "MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nDate: " + time.Now().Format(time.RFC1123Z) + "\r\n\r\n" + body + "\r\n"
	var auth smtp.Auth
	if cfg["smtp_user"] != "" {
		auth = smtp.PlainAuth("", cfg["smtp_user"], cfg["smtp_pass"], host)
	}
	addr := net.JoinHostPort(host, port)
	if port != "465" {
		return smtp.SendMail(addr, auth, from, []string{to}, []byte(msg))
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 20 * time.Second}, "tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Close()
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

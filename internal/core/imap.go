package core

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	_ "github.com/emersion/go-message/charset" // non-UTF-8 mail
	"github.com/emersion/go-message/mail"
)

const maxPerScan = 400

// IMAPFetch reads new messages read-only (EXAMINE + BODY.PEEK), so nothing in the mailbox changes, not even the
// unread marks. The first scan (or after the server renumbers the folder) looks back SinceDays days.
func IMAPFetch(m Mailbox, secret string, uidValidity, lastUID uint32) ([]Email, uint32, uint32, error) {
	if m.AuthType != "password" {
		return nil, uidValidity, lastUID, errors.New("only app-password sign-in is supported; forward this mail to a Gmail bills address instead")
	}
	c, err := imapConnect(m, secret)
	if err != nil {
		return nil, uidValidity, lastUID, err
	}
	defer c.Logout()
	st, err := c.Select(m.Folder, true) // read-only
	if err != nil {
		return nil, uidValidity, lastUID, fmt.Errorf("open folder %q: %w", m.Folder, err)
	}
	crit := imap.NewSearchCriteria()
	if st.UidValidity != uidValidity || lastUID == 0 {
		crit.Since = time.Now().AddDate(0, 0, -m.SinceDays)
		lastUID = 0
	} else {
		set := new(imap.SeqSet)
		set.AddRange(lastUID+1, 0) // 0 = "*"
		crit.Uid = set
	}
	uids, err := c.UidSearch(crit)
	if err != nil {
		return nil, uidValidity, lastUID, fmt.Errorf("search: %w", err)
	}
	var want []uint32
	for _, u := range uids {
		if u > lastUID {
			want = append(want, u)
		}
	}
	if len(want) > maxPerScan {
		want = want[len(want)-maxPerScan:] // newest first on a huge first scan
	}
	newLast := lastUID
	for _, u := range want {
		if u > newLast {
			newLast = u
		}
	}
	if len(want) == 0 {
		return nil, st.UidValidity, newLast, nil
	}
	set := new(imap.SeqSet)
	set.AddNum(want...)
	section := &imap.BodySectionName{Peek: true}
	ch := make(chan *imap.Message, 20)
	done := make(chan error, 1)
	go func() { done <- c.UidFetch(set, []imap.FetchItem{section.FetchItem(), imap.FetchUid}, ch) }()
	var out []Email
	for msg := range ch {
		r := msg.GetBody(section)
		if r == nil {
			continue
		}
		e, err := ReadEmail(io.LimitReader(r, 4<<20))
		if err != nil {
			log.Printf("mailbox %s uid %d: %v", m.Label, msg.Uid, err)
			continue
		}
		out = append(out, *e)
	}
	if err := <-done; err != nil {
		return out, uidValidity, lastUID, fmt.Errorf("fetch: %w", err)
	}
	return out, st.UidValidity, newLast, nil
}

func imapConnect(m Mailbox, secret string) (*client.Client, error) {
	addr := net.JoinHostPort(m.Host, fmt.Sprint(m.Port))
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	var c *client.Client
	var err error
	if m.Port == 143 {
		c, err = client.DialWithDialer(dialer, addr)
		if err == nil {
			err = c.StartTLS(&tls.Config{ServerName: m.Host})
		}
	} else {
		c, err = client.DialWithDialerTLS(dialer, addr, &tls.Config{ServerName: m.Host})
	}
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", addr, err)
	}
	c.Timeout = 2 * time.Minute
	if err := c.Login(m.Username, secret); err != nil {
		c.Logout()
		return nil, fmt.Errorf("sign-in failed (use an app password, not your normal password): %w", err)
	}
	return c, nil
}

// IMAPListFolders lists the folders (Gmail: labels) of a mailbox, so the user can tick which to read.
func IMAPListFolders(m Mailbox, secret string) ([]string, error) {
	if m.AuthType != "password" {
		return nil, errors.New("only app-password sign-in is supported")
	}
	c, err := imapConnect(m, secret)
	if err != nil {
		return nil, err
	}
	defer c.Logout()
	ch := make(chan *imap.MailboxInfo, 50)
	done := make(chan error, 1)
	go func() { done <- c.List("", "*", ch) }()
	var out []string
	for mi := range ch {
		skip := false
		for _, a := range mi.Attributes {
			if a == imap.NoSelectAttr {
				skip = true
			}
		}
		if !skip {
			out = append(out, mi.Name)
		}
	}
	return out, <-done
}

// ListFolders returns the folder names of a saved mailbox.
func (s *Store) ListFolders(id int64) ([]string, error) {
	mbs, err := s.ListMailboxes()
	if err != nil {
		return nil, err
	}
	for _, m := range mbs {
		if m.ID == id {
			return IMAPListFolders(m, s.mailboxSecret(id))
		}
	}
	return nil, ErrNotFound
}

// ReadEmail parses a raw RFC 822 message into headers and a plain-text body.
func ReadEmail(r io.Reader) (*Email, error) {
	mr, err := mail.CreateReader(r)
	if err != nil && mr == nil {
		return nil, err
	}
	h := mr.Header
	e := &Email{}
	e.MessageID, _ = h.MessageID()
	e.Subject, _ = h.Subject()
	e.Date, _ = h.Date()
	if from, err := h.AddressList("From"); err == nil {
		var p []string
		for _, a := range from {
			p = append(p, a.Name+" <"+a.Address+">")
		}
		e.From = strings.Join(p, ", ")
	}
	var to []string
	for _, k := range []string{"To", "Delivered-To", "X-Original-To", "Cc"} {
		if list, err := h.AddressList(k); err == nil {
			for _, a := range list {
				to = append(to, a.Address)
			}
		} else if v := h.Get(k); v != "" {
			to = append(to, v)
		}
	}
	e.To = strings.Join(to, ", ")
	var plain, htm strings.Builder
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		ih, ok := part.Header.(*mail.InlineHeader)
		if !ok {
			continue
		}
		ct, _, _ := mime.ParseMediaType(ih.Get("Content-Type"))
		b, _ := io.ReadAll(io.LimitReader(part.Body, 1<<20))
		switch {
		case ct == "text/html":
			htm.Write(b)
		case ct == "" || ct == "text/plain":
			plain.Write(b)
		}
	}
	if strings.TrimSpace(plain.String()) != "" {
		e.Body = strings.TrimSpace(plain.String())
		if htm.Len() > 0 && len(e.Body) < 40 { // a stub plain part: the HTML has the content
			e.Body = HTMLToText(htm.String())
		}
	} else {
		e.Body = HTMLToText(htm.String())
	}
	if e.Date.IsZero() {
		e.Date = time.Now()
	}
	return e, nil
}

// RunMailEngine checks the mailboxes and watch folders every 10 minutes until ctx ends.
func (s *Store) RunMailEngine(ctx context.Context, send Sender) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	time.Sleep(30 * time.Second)
	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("mail engine panic: %v", r)
				}
			}()
			s.ScanAll(IMAPFetch, send)
			s.ScanAllWatchFolders(send)
		}()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

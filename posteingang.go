package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // Zeichensätze außer UTF-8
	"github.com/emersion/go-message/mail"
	"github.com/jackc/pgx/v5"
)

// Mail-Eingang: Eine neue Mail wird ein Ticket, eine Antwort mit [#Nummer] im
// Betreff hängt sich an, wenn sie von der meldenden Adresse kommt.

const (
	mailMax   = 25 << 20
	mailJeZug = 20
	mailTeile = 50
)

var ticketNummer = regexp.MustCompile(`\[#(\d{1,18})\]`)

type Posteingang struct {
	server, nutzer, kennwort, ordner string
}

func neuerPosteingang() *Posteingang {
	s := umgebung("TICKETR_IMAP", "")
	if s == "" {
		return nil
	}
	return &Posteingang{server: s, nutzer: umgebung("TICKETR_IMAP_NUTZER", ""),
		kennwort: umgebung("TICKETR_IMAP_KENNWORT", ""), ordner: umgebung("TICKETR_IMAP_ORDNER", "INBOX")}
}

func (a *App) postHolen(ctx context.Context, p *Posteingang) error {
	host, _, err := net.SplitHostPort(p.server)
	if err != nil {
		return err
	}
	c, err := client.DialWithDialerTLS(&net.Dialer{Timeout: 15 * time.Second}, p.server,
		&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	defer c.Logout()
	c.Timeout = time.Minute
	if err := c.Login(p.nutzer, p.kennwort); err != nil {
		return err
	}
	return a.postLesen(ctx, c, p.ordner)
}

// postLesen übernimmt ungelesene Mails. Gelesen wird eine erst, wenn sie
// gespeichert ist: ein Ausfall verliert nichts.
func (a *App) postLesen(ctx context.Context, c *client.Client, ordner string) error {
	if _, err := c.Select(ordner, false); err != nil {
		return err
	}
	k := imap.NewSearchCriteria()
	k.WithoutFlags = []string{imap.SeenFlag}
	uids, err := c.UidSearch(k)
	if err != nil {
		return err
	}
	if len(uids) > mailJeZug {
		uids = uids[:mailJeZug]
	}
	var fehler error
	for _, uid := range uids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.postEine(ctx, c, uid); err != nil {
			fehler = errors.Join(fehler, fmt.Errorf("Mail %d: %w", uid, err))
		}
	}
	return fehler
}

func postAbruf(c *client.Client, uid uint32, was imap.FetchItem) (*imap.Message, error) {
	satz := new(imap.SeqSet)
	satz.AddNum(uid)
	k := make(chan *imap.Message, 4)
	ende := make(chan error, 1)
	go func() { ende <- c.UidFetch(satz, []imap.FetchItem{was, imap.FetchUid}, k) }()
	var m *imap.Message
	for x := range k {
		if x.Uid == uid {
			m = x
		}
	}
	if err := <-ende; err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("nicht mehr da")
	}
	return m, nil
}

func (a *App) postEine(ctx context.Context, c *client.Client, uid uint32) error {
	m, err := postAbruf(c, uid, imap.FetchRFC822Size)
	if err != nil {
		return err
	}
	if m.Size > mailMax {
		log.Printf("post: Mail %d ist größer als 25 MB und bleibt nur im Postfach", uid)
	} else {
		teil := &imap.BodySectionName{Peek: true}
		if m, err = postAbruf(c, uid, teil.FetchItem()); err != nil {
			return err
		}
		l := m.GetBody(teil)
		if l == nil {
			return errors.New("ohne Inhalt")
		}
		roh, err := io.ReadAll(io.LimitReader(l, mailMax))
		if err != nil {
			return err
		}
		if err := a.mailUebernehmen(ctx, roh); err != nil {
			return err
		}
	}
	satz := new(imap.SeqSet)
	satz.AddNum(uid)
	return c.UidStore(satz, imap.FormatFlagsOp(imap.AddFlags, true), []any{imap.SeenFlag}, nil)
}

type post struct {
	eingang
	id          string
	automatisch bool
}

func mailLesen(roh []byte) post {
	h := sha256.Sum256(roh)
	p := post{id: hex.EncodeToString(h[:])}
	unlesbar := func() post {
		p.betreff = "Unlesbare E-Mail"
		p.text = "Die Mail ließ sich nicht lesen und hängt als Datei an."
		p.dateien = nil
		if len(roh) <= anhangMax {
			p.dateien = []datei{{"nachricht.eml", roh}}
		}
		return p
	}
	r, err := mail.CreateReader(bytes.NewReader(roh))
	if err != nil && !message.IsUnknownCharset(err) || r == nil {
		return unlesbar()
	}
	defer r.Close()

	if l, err := r.Header.AddressList("From"); err == nil && len(l) > 0 {
		p.name, p.email = einzeilig(l[0].Name, 120), strings.ToLower(einzeilig(l[0].Address, 254))
	}
	betreff, err := r.Header.Subject()
	if err != nil {
		betreff = r.Header.Get("Subject")
	}
	if p.betreff = einzeilig(betreff, 200); p.betreff == "" {
		p.betreff = "(ohne Betreff)"
	}
	if id, err := r.Header.MessageID(); err == nil && id != "" {
		h := sha256.Sum256([]byte(p.email + "\x00" + id))
		p.id = hex.EncodeToString(h[:])
	}
	selbst := strings.ToLower(strings.TrimSpace(r.Header.Get("Auto-Submitted")))
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Precedence"))) {
	case "bulk", "junk", "list", "auto_reply":
		p.automatisch = true
	}
	p.automatisch = p.automatisch || (selbst != "" && selbst != "no") || strings.HasPrefix(p.email, "mailer-daemon@")

	var html []byte
	for i := 0; i < mailTeile; i++ {
		teil, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil && !message.IsUnknownCharset(err) || teil == nil {
			return unlesbar()
		}
		b, err := io.ReadAll(io.LimitReader(teil.Body, anhangMax+1))
		if err != nil {
			return unlesbar()
		}
		switch k := teil.Header.(type) {
		case *mail.InlineHeader:
			typ, _, err := k.ContentType()
			if err != nil {
				typ = "text/plain"
			}
			switch {
			case typ == "text/plain" && p.text == "":
				p.text = sauber(string(b), 100000)
			case typ == "text/html" && html == nil:
				html = b
			}
		case *mail.AttachmentHeader:
			name, err := k.Filename()
			if err != nil {
				name = ""
			}
			if len(b) <= anhangMax && len(p.dateien) < anhangAnzahl {
				p.dateien = append(p.dateien, datei{dateiName(strings.ReplaceAll(name, "\x00", "")), b})
			}
		}
	}
	if p.text == "" {
		p.text = "(Die Mail hat keinen Text.)"
		if html != nil && len(html) <= anhangMax {
			// HTML wird nie angezeigt, nur zum Herunterladen angehängt.
			p.text = "(Die Mail gibt es nur als HTML, sie hängt als Datei an.)"
			p.dateien = append(p.dateien, datei{"nachricht.html", html})
		}
	}
	return p
}

func (a *App) mailUebernehmen(ctx context.Context, roh []byte) error {
	p := mailLesen(roh)
	if p.automatisch {
		log.Printf("post: automatische Mail übergangen")
		return nil
	}
	if a.mail != nil && p.email != "" && strings.EqualFold(p.email, a.mail.adresse()) {
		return nil // die eigene Benachrichtigung, sonst liefe sie im Kreis
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	e, err := tx.Exec(ctx, `INSERT INTO mails (id) VALUES ($1) ON CONFLICT DO NOTHING`, p.id)
	if err != nil {
		return err
	}
	if e.RowsAffected() == 0 {
		return nil
	}
	var id int64
	if t := ticketNummer.FindStringSubmatch(p.betreff); t != nil && p.email != "" {
		if n, err := strconv.ParseInt(t[1], 10, 64); err == nil {
			// Der Absender muss passen, die Nummer allein ist zu raten.
			err := tx.QueryRow(ctx, `SELECT id FROM tickets WHERE id = $1 AND lower(melder_email) = $2`, n, p.email).Scan(&id)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
	}
	was := "ticket.antwort"
	if id == 0 {
		was = "ticket.neu"
		id, err = ticketVonAussen(ctx, tx, p.eingang)
	} else {
		err = beitragVonAussen(ctx, tx, id, p.eingang)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE mails SET ticket = $2 WHERE id = $1`, p.id, id); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	a.melden(was, id, p.betreff)
	return nil
}

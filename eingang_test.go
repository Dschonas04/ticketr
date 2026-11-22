package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-imap/server"
)

func zaehle(t *testing.T, a *App, sql string) (n int) {
	t.Helper()
	if err := a.db.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return
}

func TestPortal(t *testing.T) {
	a := pruefApp(t)
	agent := person(t, a, "agent", "agent")
	gast := &pruefPerson{a: a, kekse: []*http.Cookie{{Name: "formular", Value: strings.Repeat("a", 32)}}}
	anfrage := func() url.Values {
		return url.Values{"name": {"Anna"}, "betreff": {"Drucker"}, "text": {"druckt nicht"}}
	}
	if w := gast.ruf("GET", "/hilfe", nil); w.Code == http.StatusOK {
		t.Fatal("Portal ist ohne Einschalten offen")
	}
	a.portal = true
	a.sso.Frei = []string{"/hilfe"}

	w := gast.ruf("POST", "/hilfe", anfrage())
	ziel, _, _ := strings.Cut(w.Header().Get("Location"), "?")
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(ziel, "/hilfe/t/") {
		t.Fatalf("Anfrage: %d %s", w.Code, ziel)
	}
	agent.ruf("POST", "/t/1/beitrag", url.Values{"text": {"GEHEIME-NOTIZ"}, "intern": {"ja"}})
	agent.ruf("POST", "/t/1/beitrag", url.Values{"text": {"Kommen gleich"}})
	seite := gast.ruf("GET", ziel, nil).Body.String()
	if strings.Contains(seite, "GEHEIME-NOTIZ") || !strings.Contains(seite, "Kommen gleich") || strings.Contains(seite, `href="/berichte"`) {
		t.Error("Portal zeigt das Falsche")
	}
	if gast.ruf("GET", ziel+"x", nil).Code != http.StatusNotFound {
		t.Error("falscher Schlüssel geht")
	}
	for _, p := range []string{"/", "/t/1", "/berichte", "/api/tickets"} {
		if gast.ruf("GET", p, nil).Code == http.StatusOK {
			t.Errorf("Gast kommt an %s", p)
		}
	}
	code := 0
	for range 11 {
		code = gast.ruf("POST", "/hilfe", anfrage()).Code
	}
	if code != http.StatusTooManyRequests {
		t.Errorf("keine Bremse: %d", code)
	}
}

func testMail(von, betreff, id, mehr, text string) []byte {
	return []byte("From: " + von + "\r\nTo: support@example.org\r\nSubject: " + betreff + "\r\nMessage-ID: <" + id + "@example.org>\r\n" +
		mehr + "\r\n" + text + "\r\n")
}

func TestMailEingang(t *testing.T) {
	a := pruefApp(t)
	if _, err := a.db.Exec(context.Background(), `TRUNCATE mails`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(context.Background(), `INSERT INTO warteschlangen (name, reihe, stichworte) VALUES ('Netz', 5, 'vpn, wlan')`); err != nil {
		t.Fatal(err)
	}
	nimm := func(b []byte) {
		t.Helper()
		if err := a.mailUebernehmen(context.Background(), b); err != nil {
			t.Fatal(err)
		}
	}
	erste := testMail("Anna <anna@example.org>", "VPN geht nicht", "m1", "", "seit heute")
	nimm(erste)
	nimm(erste)
	if n := zaehle(t, a, `SELECT count(*) FROM tickets`); n != 1 {
		t.Fatalf("doppelte Mail: %d Tickets", n)
	}
	if zaehle(t, a, `SELECT count(*) FROM tickets t JOIN warteschlangen w ON w.id = t.warteschlange WHERE w.name = 'Netz'`) != 1 {
		t.Error("Stichwortregel greift nicht")
	}
	nimm(testMail("anna@example.org", "Re: [#1] VPN geht nicht", "m2", "", "immer noch"))
	if zaehle(t, a, `SELECT count(*) FROM beitraege WHERE ticket = 1`) != 2 {
		t.Error("Antwort hängt sich nicht an")
	}
	nimm(testMail("mallory@example.org", "Re: [#1] VPN geht nicht", "m3", "", "untergeschoben"))
	if zaehle(t, a, `SELECT count(*) FROM beitraege WHERE ticket = 1`) != 2 || zaehle(t, a, `SELECT count(*) FROM tickets`) != 2 {
		t.Error("fremder Absender schreibt in fremdes Ticket")
	}
	nimm(testMail("bot@example.org", "Abwesend", "m4", "Auto-Submitted: auto-replied\r\n", "bin weg"))
	if zaehle(t, a, `SELECT count(*) FROM tickets`) != 2 {
		t.Error("automatische Mail wurde ein Ticket")
	}
	nimm(testMail("carl@example.org", "Nur HTML", "m5", "Content-Type: text/html; charset=utf-8\r\n", "<script>x()</script>"))
	if zaehle(t, a, `SELECT count(*) FROM anhaenge WHERE name = 'nachricht.html'`) != 1 {
		t.Error("HTML hängt nicht als Datei an")
	}
	nimm(testMail("dora@example.org", "Null\x00zeichen", "m6", "", "a\x00b\xff"))
	nimm([]byte("kaputt\x00\xff"))
}

func TestIMAP(t *testing.T) {
	a := pruefApp(t)
	if _, err := a.db.Exec(context.Background(), `TRUNCATE mails`); err != nil {
		t.Fatal(err)
	}
	s := server.New(memory.New())
	s.AllowInsecureAuth = true
	s.ErrorLog = nopLog{}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(l)
	defer s.Close()
	c, err := client.Dial(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Logout()
	if err := c.Login("username", "password"); err != nil {
		t.Fatal(err)
	}
	if err := c.Append("INBOX", nil, time.Now(), bytes.NewBuffer(testMail("eva@example.org", "Per IMAP", "i1", "", "hallo"))); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := a.postLesen(context.Background(), c, "INBOX"); err != nil {
			t.Fatal(err)
		}
	}
	if n := zaehle(t, a, `SELECT count(*) FROM tickets WHERE betreff = 'Per IMAP'`); n != 1 {
		t.Errorf("%d Tickets statt 1", n)
	}
}

type nopLog struct{}

func (nopLog) Printf(string, ...any) {}
func (nopLog) Println(...any)        {}

func TestErinnerungEinmalJeFrist(t *testing.T) {
	a := pruefApp(t)
	p := person(t, a, "agent", "agent")
	p.ruf("POST", "/neu", url.Values{"betreff": {"Eilig"}, "prioritaet": {"dringend"}})
	ctx := context.Background()
	stelle := func(sql string) {
		if _, err := a.db.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := a.fristenPruefen(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	stelle(`UPDATE tickets SET erstellt = now() - interval '5 hours', faellig = now() - interval '1 hour'`)
	if n := zaehle(t, a, `SELECT count(*) FROM beitraege WHERE art = 'ereignis'`); n != 2 {
		t.Fatalf("%d Erinnerungen statt 2", n)
	}
	stelle(`UPDATE tickets SET faellig = now() + interval '30 minutes'`)
	if n := zaehle(t, a, `SELECT count(*) FROM beitraege WHERE art = 'ereignis'`); n != 3 {
		t.Errorf("neue Frist erinnert nicht genau einmal: %d", n)
	}
}

func TestWebhookUnterschrift(t *testing.T) {
	ok := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		m := hmac.New(sha256.New, []byte("testgeheimnis"))
		m.Write(b)
		ok = r.Header.Get("X-Ticketr-Signatur") == "sha256="+hex.EncodeToString(m.Sum(nil)) && strings.Contains(string(b), "ticket.neu")
	}))
	defer s.Close()
	if err := neuerWebhook(s.URL, "testgeheimnis").senden("ticket.neu", 1, "Test"); err != nil || !ok {
		t.Errorf("Webhook: %v %v", err, ok)
	}
}

func TestAnsichten(t *testing.T) {
	a := pruefApp(t)
	eine, andere := person(t, a, "eine", "agent"), person(t, a, "andere", "agent")
	eine.ruf("POST", "/ansichten", url.Values{"name": {"Meine Eiligen"}, "prioritaet": {"dringend"}})
	if !strings.Contains(eine.ruf("GET", "/", nil).Body.String(), "Meine Eiligen") {
		t.Error("Ansicht fehlt")
	}
	if strings.Contains(andere.ruf("GET", "/", nil).Body.String(), "Meine Eiligen") {
		t.Error("fremde Ansicht sichtbar")
	}
}

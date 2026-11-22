package main

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func (p *pruefPerson) hochladen(t *testing.T, pfad string, f map[string]string, dateien map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var b bytes.Buffer
	m := multipart.NewWriter(&b)
	f["token"] = strings.Repeat("a", 32)
	for k, v := range f {
		if err := m.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	for name, daten := range dateien {
		w, err := m.CreateFormFile("dateien", name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(daten)
	}
	m.Close()
	r := httptest.NewRequest("POST", pfad, &b)
	r.Header.Set("Content-Type", m.FormDataContentType())
	r.Header.Set("Origin", "http://"+r.Host)
	for _, c := range p.kekse {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	p.a.wege().ServeHTTP(w, r)
	return w
}

func TestAnhaenge(t *testing.T) {
	a := pruefApp(t)
	p := person(t, a, "agent", "agent")
	inhalt := []byte("<script>alert(1)</script>\x00\xff")
	if w := p.hochladen(t, "/neu", map[string]string{"betreff": "Mit Datei"}, map[string][]byte{"seite.html": inhalt}); w.Code != http.StatusSeeOther {
		t.Fatalf("anlegen: %d", w.Code)
	}
	p.hochladen(t, "/neu", map[string]string{"betreff": "Anderes Ticket"}, nil)
	var id string
	if err := a.db.QueryRow(context.Background(), `SELECT id::text FROM anhaenge WHERE ticket=1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	w := p.ruf("GET", "/t/1/anhang/"+id, nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), inhalt) {
		t.Fatalf("Inhalt kommt nicht unverändert zurück: %d", w.Code)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") || w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Error("HTML-Anhang würde im Browser angezeigt")
	}
	if w := p.ruf("GET", "/t/2/anhang/"+id, nil); w.Code != http.StatusNotFound {
		t.Errorf("Anhang unter fremdem Ticket: %d", w.Code)
	}
	if !strings.Contains(p.ruf("GET", "/t/1", nil).Body.String(), "seite.html") {
		t.Error("Anhang fehlt im Verlauf")
	}

	p.hochladen(t, "/t/1/beitrag", map[string]string{"text": "zu groß"}, map[string][]byte{"gross.bin": make([]byte, anhangMax+1)})
	var n int
	if err := a.db.QueryRow(context.Background(), `SELECT count(*) FROM anhaenge`).Scan(&n); err != nil || n != 1 {
		t.Errorf("zu große Datei angenommen: %d %v", n, err)
	}
}

func TestDateiName(t *testing.T) {
	for roh, soll := range map[string]string{"../../etc/passwd": "passwd", `C:\Users\x\Bild.png`: "Bild.png", "..": "anhang", "a\u202egpj.exe": "agpj.exe", "": "anhang"} {
		if ist := dateiName(roh); ist != soll {
			t.Errorf("%q: %q statt %q", roh, ist, soll)
		}
	}
}

func TestVorlagen(t *testing.T) {
	a := pruefApp(t)
	chefin, agent := person(t, a, "chefin", "admin"), person(t, a, "agent", "agent")
	if w := agent.ruf("POST", "/vorlagen", url.Values{"name": {"X"}, "text": {"Y"}}); w.Code != http.StatusForbidden {
		t.Errorf("Agent ändert Vorlagen: %d", w.Code)
	}
	chefin.ruf("POST", "/vorlagen", url.Values{"name": {"Rückfrage"}, "text": {"Welche Fehlermeldung kommt?"}})
	agent.ruf("POST", "/neu", url.Values{"betreff": {"Test"}})
	if !strings.Contains(agent.ruf("GET", "/t/1", nil).Body.String(), "Welche Fehlermeldung kommt?") {
		t.Error("Vorlage fehlt am Ticket")
	}
}

func TestBerichteUndUeberfaellig(t *testing.T) {
	a := pruefApp(t)
	p := person(t, a, "agent", "agent")
	for _, b := range []string{"Alt", "Frisch", "Fertig"} {
		p.ruf("POST", "/neu", url.Values{"betreff": {b}})
	}
	// Leerer Zeitraum und gefüllter müssen beide rendern.
	if w := p.ruf("GET", "/berichte", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "keine Daten") {
		t.Fatalf("Bericht ohne gelöste Tickets: %d", w.Code)
	}
	for _, q := range []string{
		`UPDATE tickets SET faellig = now() - interval '1 hour' WHERE betreff = 'Alt'`,
		`UPDATE tickets SET status = 'geloest', geloest_am = now(), erste_antwort = now() WHERE betreff = 'Fertig'`,
	} {
		if _, err := a.db.Exec(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	if b := p.ruf("GET", "/?status=ueberfaellig", nil).Body.String(); !strings.Contains(b, "Alt</strong>") || strings.Contains(b, "Frisch</strong>") {
		t.Error("Filter Überfällige stimmt nicht")
	}
	if b := p.ruf("GET", "/berichte?tage=7", nil).Body.String(); !strings.Contains(b, "100 %") {
		t.Error("Fristquote stimmt nicht")
	}
}

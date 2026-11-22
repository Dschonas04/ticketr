package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pruefApp braucht eine leere Wegwerf-Datenbank in TICKETR_TEST_DB. Der Name
// muss mit pruef beginnen, denn der Test leert alle Tabellen.
func pruefApp(t *testing.T) *App {
	t.Helper()
	adresse := os.Getenv("TICKETR_TEST_DB")
	if adresse == "" {
		t.Skip("TICKETR_TEST_DB ist nicht gesetzt")
	}
	k, err := pgxpool.ParseConfig(adresse)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k.ConnConfig.Database, "pruef") {
		t.Fatal("TICKETR_TEST_DB muss auf eine Datenbank pruef* zeigen")
	}
	ctx := context.Background()
	db := datenbank(ctx, adresse)
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, `TRUNCATE beitraege, tickets, warteschlangen, konten RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, schemaSQL); err != nil {
		t.Fatal(err)
	}
	return &App{db: db, v: ladeVorlagen(),
		sso: NeuesSSO("Ticketr", filepath.Join(t.TempDir(), "sso.json"), "TICKETR_TEST_SSO_AUS")}
}

type pruefPerson struct {
	a     *App
	kekse []*http.Cookie
	id    string
}

func person(t *testing.T, a *App, name, rolle string) *pruefPerson {
	t.Helper()
	k := ssoKonto{Name: name, Hash: ssoPasswortHash("nur-fuer-den-test-" + name)}
	a.sso.konten = append(a.sso.konten, k)
	rec := httptest.NewRecorder()
	a.sso.lokalAnmelden(rec, httptest.NewRequest("POST", "/", nil), k)
	p := &pruefPerson{a: a, kekse: rec.Result().Cookies()}

	r := httptest.NewRequest("GET", "/", nil)
	for _, c := range p.kekse {
		r.AddCookie(c)
	}
	konto, err := a.ich(r)
	if err != nil {
		t.Fatal(err)
	}
	p.id = konto.ID
	if _, err := a.db.Exec(r.Context(), `UPDATE konten SET rolle=$2 WHERE id::text=$1`, p.id, rolle); err != nil {
		t.Fatal(err)
	}
	p.kekse = append(p.kekse, &http.Cookie{Name: "formular", Value: strings.Repeat("a", 32)})
	return p
}

func (p *pruefPerson) ruf(methode, pfad string, f url.Values) *httptest.ResponseRecorder {
	if f == nil {
		f = url.Values{}
	}
	var r *http.Request
	if methode == "GET" {
		r = httptest.NewRequest(methode, pfad, nil)
	} else {
		f.Set("token", strings.Repeat("a", 32))
		r = httptest.NewRequest(methode, pfad, strings.NewReader(f.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "http://"+r.Host)
	}
	for _, c := range p.kekse {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	p.a.wege().ServeHTTP(w, r)
	return w
}

func TestVerwaltungNurAdmins(t *testing.T) {
	a := pruefApp(t)
	chefin := person(t, a, "chefin", "admin")
	agent := person(t, a, "agent", "agent")
	var schlange string
	if err := a.db.QueryRow(context.Background(), `SELECT id::text FROM warteschlangen LIMIT 1`).Scan(&schlange); err != nil {
		t.Fatal(err)
	}

	for _, f := range []struct {
		methode, pfad string
		werte         url.Values
	}{
		{"GET", "/warteschlangen", nil},
		{"POST", "/warteschlangen", url.Values{"name": {"Fremd"}}},
		{"POST", "/warteschlangen", url.Values{"id": {schlange}, "name": {"Umbenannt"}}},
		{"POST", "/warteschlangen/" + schlange + "/loeschen", nil},
		{"POST", "/konten/" + chefin.id + "/rolle", url.Values{"rolle": {"agent"}}},
	} {
		if w := agent.ruf(f.methode, f.pfad, f.werte); w.Code != http.StatusForbidden {
			t.Errorf("Agent %s %s: %d statt 403", f.methode, f.pfad, w.Code)
		}
	}
	if w := chefin.ruf("GET", "/warteschlangen", nil); w.Code != http.StatusOK {
		t.Errorf("Admin sieht die Verwaltung nicht: %d", w.Code)
	}
	if b := agent.ruf("GET", "/", nil).Body.String(); strings.Contains(b, `href="/warteschlangen"`) {
		t.Error("Agent sieht den Menüpunkt Verwaltung")
	}
}

func TestRolleAendern(t *testing.T) {
	a := pruefApp(t)
	chefin := person(t, a, "chefin", "admin")
	agent := person(t, a, "agent", "agent")
	rolle := func(id string) (r string) {
		if err := a.db.QueryRow(context.Background(), `SELECT rolle FROM konten WHERE id::text=$1`, id).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return
	}
	chefin.ruf("POST", "/konten/"+chefin.id+"/rolle", url.Values{"rolle": {"agent"}})
	if rolle(chefin.id) != "admin" {
		t.Error("Admin konnte sich selbst aussperren")
	}
	chefin.ruf("POST", "/konten/"+agent.id+"/rolle", url.Values{"rolle": {"admin"}})
	if rolle(agent.id) != "admin" {
		t.Error("Rolle nicht gesetzt")
	}
}

func TestAPIFiltertNachPrioritaet(t *testing.T) {
	a := pruefApp(t)
	p := person(t, a, "chefin", "admin")
	p.ruf("POST", "/neu", url.Values{"betreff": {"Eilig"}, "prioritaet": {"dringend"}})
	p.ruf("POST", "/neu", url.Values{"betreff": {"Gemütlich"}, "prioritaet": {"niedrig"}})
	b := p.ruf("GET", "/api/tickets?prioritaet=dringend", nil).Body.String()
	if !strings.Contains(b, "Eilig") || strings.Contains(b, "Gemütlich") {
		t.Errorf("Filter greift nicht: %s", b)
	}
}

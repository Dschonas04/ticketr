// Ticketr: Ticketsystem für Anfragen, Störungen und Aufgaben. Tickets mit
// Status, Priorität, Warteschlange und Zuständigkeit, Verlauf aus Antworten und
// internen Notizen, Fristen nach Priorität, Benachrichtigung per Mail.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type App struct {
	db   *pgxpool.Pool
	sso  *SSO
	v    Vorlagen
	mail *Mailer
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := &App{
		db:   datenbank(ctx, umgebung("TICKETR_DB", "postgres://ticketr:ticketr@localhost:5432/ticketr?sslmode=disable")),
		sso:  NeuesSSO("Ticketr", umgebung("TICKETR_SSO_DATEI", "./einstellungen/sso.json"), "TICKETR_SSO_AUS"),
		v:    ladeVorlagen(),
		mail: neuerMailer(),
	}
	a.sso.Frei = []string{"/statisch/", "/healthz"}

	adresse := ":" + umgebung("TICKETR_PORT", "8350")
	srv := &http.Server{Addr: adresse, Handler: a.wege(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("Ticketr %s lauscht auf %s", version, adresse)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	ende, abbruch := context.WithTimeout(context.Background(), 10*time.Second)
	defer abbruch()
	srv.Shutdown(ende)
}

func (a *App) wege() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", gesund)
	m.HandleFunc("GET /favicon.svg", logo)
	m.HandleFunc("GET /favicon.ico", logo)
	m.Handle("GET /statisch/", statisch())
	a.sso.Wege(m, a.angemeldet)

	m.HandleFunc("GET /{$}", a.liste)
	m.HandleFunc("GET /neu", a.neuFormular)
	m.HandleFunc("POST /neu", a.neuAnlegen)
	m.HandleFunc("GET /t/{id}", a.ticketZeigen)
	m.HandleFunc("POST /t/{id}/beitrag", a.beitragSchreiben)
	m.HandleFunc("POST /t/{id}/eigenschaften", a.eigenschaftenSetzen)
	m.HandleFunc("GET /warteschlangen", a.nurAdmin(a.warteschlangen))
	m.HandleFunc("POST /warteschlangen", a.nurAdmin(a.warteschlangeSpeichern))
	m.HandleFunc("POST /warteschlangen/{id}/loeschen", a.nurAdmin(a.warteschlangeLoeschen))
	m.HandleFunc("POST /konten/{id}/rolle", a.nurAdmin(a.rolleSetzen))
	m.HandleFunc("GET /api/tickets", a.apiListe)
	return sicherheitskopf(nurAngemeldet(a.sso, m))
}

func (a *App) angemeldet(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Anmeldung und Konten verwalten nur Admins; vor der Einrichtung, wer
		// den Code aus dem Log hat.
		if !a.sso.Geschuetzt() && a.sso.EinrichtungErlaubt(w, r) {
			h(w, r)
			return
		}
		if _, ok := a.sso.Wer(r); !ok {
			http.Error(w, "Bitte anmelden", http.StatusUnauthorized)
			return
		}
		if k, err := a.ich(r); err != nil || k.Rolle != "admin" {
			http.Error(w, "Nur für Admins", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (a *App) daten(r *http.Request, titel, nav string, d any) Daten {
	n, _ := a.sso.Wer(r)
	return Daten{App: "Ticketr", Titel: titel, Nav: nav, Nutzer: n, Admin: a.istAdmin(r), D: d}
}

// Ticketr: Ticketsystem für Anfragen, Störungen und Aufgaben. Tickets mit
// Status, Priorität, Warteschlange und Zuständigkeit, Verlauf aus Antworten und
// internen Notizen, Fristen nach Priorität, Benachrichtigung per Mail.
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type App struct {
	// verwaltung: Änderungen an Konten, Rollen und Anmeldung laufen nacheinander.
	verwaltung Sperre
	portal     bool // Anfragen ohne Konto unter /hilfe
	bremse     bremse
	webhook    *Webhook
	admins     []string // Vorgabe des Betreibers
	db         *pgxpool.Pool
	sso        *SSO
	v          Vorlagen
	mail       *Mailer
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
	a.admins = adminListe(umgebung("TICKETR_ADMIN", ""))
	a.sso.ErstesKonto = a.ersterAdmin
	a.sso.KontoWeg = a.adminEntziehen
	a.sso.Frei = []string{"/statisch/", "/healthz"}
	if a.portal = umgebung("TICKETR_PORTAL", "") == "1"; a.portal {
		a.sso.Frei = append(a.sso.Frei, "/hilfe")
	}
	a.webhook = neuerWebhook(umgebung("TICKETR_WEBHOOK", ""), umgebung("TICKETR_WEBHOOK_GEHEIMNIS", ""))
	go regelmaessig(ctx, "fristen", a.fristenPruefen)
	if p := neuerPosteingang(); p != nil {
		go regelmaessig(ctx, "post", func(c context.Context) error { return a.postHolen(c, p) })
	}

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
	m.HandleFunc("POST /konten/{id}/rolle", a.nacheinander(a.nurAdmin(a.rolleSetzen)))
	m.HandleFunc("GET /t/{id}/anhang/{anhang}", a.anhangLaden)
	m.HandleFunc("GET /berichte", a.berichte)
	m.HandleFunc("GET /vorlagen", a.nurAdmin(a.vorlagenSeite))
	m.HandleFunc("POST /vorlagen", a.nurAdmin(a.vorlageSpeichern))
	m.HandleFunc("POST /vorlagen/{id}/loeschen", a.nurAdmin(a.vorlageLoeschen))
	m.HandleFunc("GET /api/tickets", a.apiListe)
	m.HandleFunc("POST /ansichten", a.ansichtSpeichern)
	m.HandleFunc("POST /ansichten/{id}/loeschen", a.ansichtLoeschen)
	if a.portal {
		m.HandleFunc("GET /hilfe", a.portalStart)
		m.HandleFunc("POST /hilfe", a.portalAnlegen)
		m.HandleFunc("GET /hilfe/t/{schluessel}", a.portalTicket)
		m.HandleFunc("POST /hilfe/t/{schluessel}", a.portalAntwort)
	}
	return sicherheitskopf(nurAngemeldet(a.sso, m))
}

func (a *App) angemeldet(h http.HandlerFunc) http.HandlerFunc {
	// Anmeldung und Konten verwalten nur Admins; vor der Einrichtung, wer den
	// Code aus dem Log hat.
	darf := func(w http.ResponseWriter, r *http.Request) bool {
		if !a.sso.Geschuetzt() && a.sso.EinrichtungErlaubt(w, r) {
			return true
		}
		if _, ok := a.sso.Wer(r); !ok {
			http.Error(w, "Bitte anmelden", http.StatusUnauthorized)
			return false
		}
		if !a.istAdmin(r) {
			http.Error(w, "Nur für Admins", http.StatusForbidden)
			return false
		}
		return true
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !inhaltZuerst(w, r) {
			return
		}
		// Lesen und Testen ändern nichts und brauchen keine Sperre.
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.URL.Path == "/api/sso/testen" {
			if darf(w, r) {
				h(w, r)
			}
			return
		}
		// Der Anbieter wird vor der Sperre gefragt, und nur von Admins.
		if r.URL.Path == "/api/sso/einstellungen" {
			if !darf(w, r) {
				return
			}
			roh, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Die Anfrage ist unvollständig", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(roh))
			var ok bool
			if r, ok = a.sso.ausstellerVorab(w, r, roh); !ok {
				return
			}
		}
		// Erst sperren, dann prüfen: bis zum Speichern kann niemand die Rechte
		// entziehen, auf die sich die Prüfung stützt.
		a.verwaltung.fuehreAus(w, r, func(w http.ResponseWriter, r *http.Request) {
			if darf(w, r) {
				h(w, r)
			}
		})
	}
}

func (a *App) daten(r *http.Request, titel, nav string, d any) Daten {
	n, _ := a.sso.Wer(r)
	return Daten{App: "Ticketr", Titel: titel, Nav: nav, Nutzer: n, Admin: a.istAdmin(r), D: d}
}

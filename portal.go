package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	netmail "net/mail"
	"sync"
	"time"
)

// Kundenportal: Anfragen stellen und den Stand sehen, ohne Konto. Es gibt es
// nur mit TICKETR_PORTAL=1. Zu einem Ticket führt allein sein geheimer Link,
// gespeichert ist davon nur der Hash.

const portalMax = 64 << 10

// bremse zählt je Absender in festen Fenstern.
type bremse struct {
	mu     sync.Mutex
	anzahl map[string]int
	ab     time.Time
}

func (b *bremse) darf(wer string, hoechstens int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.anzahl == nil || time.Since(b.ab) > time.Hour {
		b.anzahl, b.ab = map[string]int{}, time.Now()
	}
	if b.anzahl[wer] >= hoechstens || len(b.anzahl) > 50000 {
		return false
	}
	b.anzahl[wer]++
	return true
}

func portalHash(schluessel string) string {
	h := sha256.Sum256([]byte(schluessel))
	return hex.EncodeToString(h[:])
}

func (a *App) portalDaten(titel string, d any) Daten {
	return Daten{App: "Ticketr", Titel: titel, Nav: "portal", D: d}
}

// portalFormular bremst und begrenzt, bevor irgendetwas gelesen wird.
func (a *App) portalFormular(w http.ResponseWriter, r *http.Request, art string, hoechstens int) bool {
	if !a.bremse.darf(art+ssoAdresse(r), hoechstens) {
		http.Error(w, "Zu viele Nachrichten. Bitte später noch einmal.", http.StatusTooManyRequests)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, portalMax)
	return pruefeFormular(w, r)
}

func (a *App) portalStart(w http.ResponseWriter, r *http.Request) {
	a.v.zeigen(w, r, "portal", a.portalDaten("Anfrage stellen", nil))
}

func (a *App) portalAnlegen(w http.ResponseWriter, r *http.Request) {
	if !a.portalFormular(w, r, "neu:", 10) {
		return
	}
	e := eingang{betreff: einzeilig(r.FormValue("betreff"), 200), text: sauber(r.FormValue("text"), 20000),
		name: einzeilig(r.FormValue("name"), 120), email: einzeilig(r.FormValue("email"), 254)}
	if e.betreff == "" || e.text == "" || e.name == "" {
		zurueck(w, r, "/hilfe", "Name, Betreff und Beschreibung gehören dazu")
		return
	}
	if e.email != "" {
		if m, err := netmail.ParseAddress(e.email); err != nil || m.Address != e.email {
			zurueck(w, r, "/hilfe", "Die E-Mail-Adresse stimmt nicht")
			return
		}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, "Kein Zufall", http.StatusInternalServerError)
		return
	}
	schluessel := base64.RawURLEncoding.EncodeToString(b)
	e.portalHash = portalHash(schluessel)

	tx, err := a.db.Begin(r.Context())
	if err != nil {
		portalFehler(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	id, err := ticketVonAussen(r.Context(), tx, e)
	if err != nil {
		portalFehler(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		portalFehler(w, err)
		return
	}
	a.melden("ticket.neu", id, e.betreff)
	zurueck(w, r, "/hilfe/t/"+schluessel, "Angekommen. Diese Adresse bitte aufheben, nur über sie ist die Anfrage zu sehen.")
}

func (a *App) portalTicket(w http.ResponseWriter, r *http.Request) {
	var t Ticket
	if err := a.db.QueryRow(r.Context(), `SELECT id, betreff, status, erstellt FROM tickets WHERE portal_hash = $1`,
		portalHash(r.PathValue("schluessel"))).Scan(&t.ID, &t.Betreff, &t.Status, &t.Erstellt); err != nil {
		http.NotFound(w, r)
		return
	}
	// Nur Antworten: Notizen und Ereignisse sind intern.
	rows, err := a.db.Query(r.Context(), `SELECT COALESCE(k.name, NULLIF(b.von_name, ''), 'Unbekannt'), b.text, b.erstellt
		FROM beitraege b LEFT JOIN konten k ON k.id = b.verfasser
		WHERE b.ticket = $1 AND b.art = 'antwort' ORDER BY b.erstellt, b.id`, t.ID)
	if err != nil {
		portalFehler(w, err)
		return
	}
	defer rows.Close()
	var verlauf []Beitrag
	for rows.Next() {
		var b Beitrag
		if err := rows.Scan(&b.Verfasser, &b.Text, &b.Erstellt); err != nil {
			portalFehler(w, err)
			return
		}
		verlauf = append(verlauf, b)
	}
	if err := rows.Err(); err != nil {
		portalFehler(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Referrer-Policy", "same-origin")
	a.v.zeigen(w, r, "portal_ticket", a.portalDaten(fmt.Sprintf("Anfrage %d", t.ID),
		map[string]any{"T": t, "Verlauf": verlauf, "Schluessel": r.PathValue("schluessel")}))
}

func (a *App) portalAntwort(w http.ResponseWriter, r *http.Request) {
	if !a.portalFormular(w, r, "antwort:", 30) {
		return
	}
	schluessel := r.PathValue("schluessel")
	var id int64
	var betreff string
	e := eingang{text: sauber(r.FormValue("text"), 20000)}
	if err := a.db.QueryRow(r.Context(), `SELECT id, betreff, melder_name, melder_email FROM tickets WHERE portal_hash = $1`,
		portalHash(schluessel)).Scan(&id, &betreff, &e.name, &e.email); err != nil {
		http.NotFound(w, r)
		return
	}
	if e.text == "" {
		zurueck(w, r, "/hilfe/t/"+schluessel, "Kein Text")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		portalFehler(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if err := beitragVonAussen(r.Context(), tx, id, e); err != nil {
		portalFehler(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		portalFehler(w, err)
		return
	}
	a.melden("ticket.antwort", id, betreff)
	zurueck(w, r, "/hilfe/t/"+schluessel, "Nachricht angekommen")
}

// portalFehler verrät Fremden nichts über die Datenbank.
func portalFehler(w http.ResponseWriter, err error) {
	log.Printf("portal: %v", err)
	http.Error(w, "Das hat nicht geklappt. Bitte noch einmal versuchen.", http.StatusInternalServerError)
}

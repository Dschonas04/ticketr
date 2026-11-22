package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------- Werte ----------

type Wahl struct{ Wert, Name string }

var Stati = []Wahl{{"neu", "Neu"}, {"offen", "Offen"}, {"wartet", "Wartet"}, {"geloest", "Gelöst"}, {"geschlossen", "Geschlossen"}}
var Prioritaeten = []Wahl{{"niedrig", "Niedrig"}, {"normal", "Normal"}, {"hoch", "Hoch"}, {"dringend", "Dringend"}}

func name(l []Wahl, wert string) string {
	for _, w := range l {
		if w.Wert == wert {
			return w.Name
		}
	}
	return wert
}

func gueltig(l []Wahl, wert string) bool {
	for _, w := range l {
		if w.Wert == wert {
			return true
		}
	}
	return false
}

// frist: bis wann ein Ticket je nach Priorität gelöst sein soll.
func frist(prio string, ab time.Time) time.Time {
	switch prio {
	case "dringend":
		return ab.Add(4 * time.Hour)
	case "hoch":
		return ab.Add(24 * time.Hour)
	case "niedrig":
		return ab.Add(7 * 24 * time.Hour)
	}
	return ab.Add(3 * 24 * time.Hour)
}

type Konto struct {
	ID, Name, Email, Rolle string
}

type Ticket struct {
	ID              int64
	Betreff         string
	Status          string
	Prioritaet      string
	Warteschlange   string
	WarteschlangeID string
	MelderName      string
	MelderEmail     string
	Zustaendig      string
	ZustaendigID    string
	Faellig         *time.Time
	ErsteAntwort    *time.Time
	Erstellt        time.Time
	Geaendert       time.Time
	Beitraege       int
}

func (t Ticket) StatusName() string     { return name(Stati, t.Status) }
func (t Ticket) PrioritaetName() string { return name(Prioritaeten, t.Prioritaet) }
func (t Ticket) Offen() bool            { return t.Status != "geloest" && t.Status != "geschlossen" }
func (t Ticket) Ueberfaellig() bool {
	return t.Offen() && t.Faellig != nil && time.Now().After(*t.Faellig)
}
func (t Ticket) StatusKlasse() string {
	switch t.Status {
	case "neu":
		return "info"
	case "offen":
		return "warn"
	case "geloest", "geschlossen":
		return "gut"
	}
	return ""
}
func (t Ticket) PrioKlasse() string {
	switch t.Prioritaet {
	case "dringend":
		return "schlecht"
	case "hoch":
		return "warn"
	}
	return ""
}

type Beitrag struct {
	ID        int64
	Verfasser string
	Art       string
	Text      string
	Erstellt  time.Time
}

// ich legt das Konto der angemeldeten Person an oder frischt es auf. Neue
// Personen sind Agenten, außer der Betreiber gibt sie als Admin vor.
func (a *App) ich(r *http.Request) (Konto, error) {
	n, _ := a.sso.Wer(r)
	kennung := n.Kennung
	if kennung == "" {
		kennung = "lokal:" + n.Nutzer
	}
	anzeige := n.Name
	if anzeige == "" {
		anzeige = n.Nutzer
	}
	if anzeige == "" {
		anzeige = "Unbekannt"
	}
	var k Konto
	err := a.db.QueryRow(r.Context(), `
		INSERT INTO konten (kennung, name, email, rolle)
		VALUES ($1, $2, $3, CASE WHEN $4 THEN 'admin' ELSE 'agent' END)
		ON CONFLICT (kennung) DO UPDATE SET name=$2, email=$3, zuletzt=now(),
			rolle = CASE WHEN $4 THEN 'admin' ELSE konten.rolle END
		RETURNING id::text, name, email, rolle`, kennung, anzeige, n.Email, slices.Contains(a.admins, kennung)).Scan(&k.ID, &k.Name, &k.Email, &k.Rolle)
	return k, err
}

func (a *App) konten(ctx context.Context) []Konto {
	rows, err := a.db.Query(ctx, `SELECT id::text, name, email, rolle FROM konten ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []Konto
	for rows.Next() {
		var k Konto
		if rows.Scan(&k.ID, &k.Name, &k.Email, &k.Rolle) == nil {
			l = append(l, k)
		}
	}
	return l
}

type Schlange struct {
	ID, Name string
	Reihe    int
	Offen    int
}

func (a *App) schlangen(ctx context.Context) []Schlange {
	rows, err := a.db.Query(ctx, `SELECT w.id::text, w.name, w.reihe,
		(SELECT count(*) FROM tickets t WHERE t.warteschlange = w.id AND t.status NOT IN ('geloest','geschlossen'))
		FROM warteschlangen w ORDER BY w.reihe, w.name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []Schlange
	for rows.Next() {
		var s Schlange
		if rows.Scan(&s.ID, &s.Name, &s.Reihe, &s.Offen) == nil {
			l = append(l, s)
		}
	}
	return l
}

const ticketSpalten = `t.id, t.betreff, t.status, t.prioritaet, COALESCE(w.name, ''), COALESCE(t.warteschlange::text, ''),
	t.melder_name, t.melder_email, COALESCE(z.name, ''), COALESCE(t.zustaendig::text, ''),
	t.faellig, t.erste_antwort, t.erstellt, t.geaendert,
	(SELECT count(*) FROM beitraege b WHERE b.ticket = t.id AND b.art <> 'ereignis')`

const ticketVon = ` FROM tickets t
	LEFT JOIN warteschlangen w ON w.id = t.warteschlange
	LEFT JOIN konten z ON z.id = t.zustaendig `

func scanTicket(row pgx.Row) (Ticket, error) {
	var t Ticket
	err := row.Scan(&t.ID, &t.Betreff, &t.Status, &t.Prioritaet, &t.Warteschlange, &t.WarteschlangeID,
		&t.MelderName, &t.MelderEmail, &t.Zustaendig, &t.ZustaendigID,
		&t.Faellig, &t.ErsteAntwort, &t.Erstellt, &t.Geaendert, &t.Beitraege)
	return t, err
}

// Filter der Liste, aus der Adresse.
type Filter struct {
	Status        string // offen (Vorgabe) | alle | einer der Stati
	Warteschlange string
	Zustaendig    string // "" alle | ich | niemand | Konto-ID
	Suche         string
	Prioritaet    string
}

func (a *App) suchen(ctx context.Context, f Filter, ich string) ([]Ticket, error) {
	var wo []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	switch f.Status {
	case "", "offen":
		wo = append(wo, "t.status NOT IN ('geloest','geschlossen')")
	case "alle":
	default:
		wo = append(wo, "t.status = "+arg(f.Status))
	}
	if f.Warteschlange != "" {
		wo = append(wo, "t.warteschlange::text = "+arg(f.Warteschlange))
	}
	if f.Prioritaet != "" {
		wo = append(wo, "t.prioritaet = "+arg(f.Prioritaet))
	}
	switch f.Zustaendig {
	case "":
	case "ich":
		wo = append(wo, "t.zustaendig::text = "+arg(ich))
	case "niemand":
		wo = append(wo, "t.zustaendig IS NULL")
	default:
		wo = append(wo, "t.zustaendig::text = "+arg(f.Zustaendig))
	}
	if q := strings.TrimSpace(f.Suche); q != "" {
		if n, err := strconv.ParseInt(strings.TrimPrefix(q, "#"), 10, 64); err == nil {
			wo = append(wo, "t.id = "+arg(n))
		} else {
			p := arg("%" + q + "%")
			wo = append(wo, "(t.betreff ILIKE "+p+" OR t.melder_name ILIKE "+p+" OR t.melder_email ILIKE "+p+
				" OR EXISTS (SELECT 1 FROM beitraege b WHERE b.ticket = t.id AND b.text ILIKE "+p+"))")
		}
	}
	sql := "SELECT " + ticketSpalten + ticketVon
	if len(wo) > 0 {
		sql += " WHERE " + strings.Join(wo, " AND ")
	}
	sql += ` ORDER BY CASE t.prioritaet WHEN 'dringend' THEN 0 WHEN 'hoch' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END,
		t.faellig NULLS LAST, t.id DESC LIMIT 500`
	rows, err := a.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []Ticket
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		l = append(l, t)
	}
	return l, rows.Err()
}

// ---------- Seiten ----------

func (a *App) liste(w http.ResponseWriter, r *http.Request) {
	ich, err := a.ich(r)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	q := r.URL.Query()
	f := Filter{Status: q.Get("status"), Warteschlange: q.Get("warteschlange"), Zustaendig: q.Get("zustaendig"),
		Suche: q.Get("q"), Prioritaet: q.Get("prioritaet")}
	l, err := a.suchen(r.Context(), f, ich.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var zahlen struct{ Offen, Mir, Ueber, Neu int }
	a.db.QueryRow(r.Context(), `SELECT
		count(*) FILTER (WHERE status NOT IN ('geloest','geschlossen')),
		count(*) FILTER (WHERE status NOT IN ('geloest','geschlossen') AND zustaendig::text = $1),
		count(*) FILTER (WHERE status NOT IN ('geloest','geschlossen') AND faellig < now()),
		count(*) FILTER (WHERE status = 'neu')
		FROM tickets`, ich.ID).Scan(&zahlen.Offen, &zahlen.Mir, &zahlen.Ueber, &zahlen.Neu)
	a.v.zeigen(w, r, "liste", a.daten(r, "Tickets", "liste", map[string]any{
		"Tickets": l, "F": f, "Stati": Stati, "Prioritaeten": Prioritaeten,
		"Schlangen": a.schlangen(r.Context()), "Konten": a.konten(r.Context()), "Zahlen": zahlen,
	}))
}

func (a *App) neuFormular(w http.ResponseWriter, r *http.Request) {
	ich, _ := a.ich(r)
	a.v.zeigen(w, r, "neu", a.daten(r, "Neues Ticket", "neu", map[string]any{
		"Prioritaeten": Prioritaeten, "Schlangen": a.schlangen(r.Context()), "Konten": a.konten(r.Context()), "Ich": ich,
	}))
}

func (a *App) neuAnlegen(w http.ResponseWriter, r *http.Request) {
	if !pruefeFormular(w, r) {
		return
	}
	ich, err := a.ich(r)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	betreff := strings.TrimSpace(r.FormValue("betreff"))
	text := strings.TrimSpace(r.FormValue("text"))
	if betreff == "" {
		zurueck(w, r, "/neu", "Der Betreff fehlt")
		return
	}
	prio := r.FormValue("prioritaet")
	if !gueltig(Prioritaeten, prio) {
		prio = "normal"
	}
	melderName := strings.TrimSpace(r.FormValue("melder_name"))
	melderEmail := strings.TrimSpace(r.FormValue("melder_email"))
	if melderName == "" && melderEmail == "" {
		melderName, melderEmail = ich.Name, ich.Email
	}
	var id int64
	err = a.db.QueryRow(r.Context(), `INSERT INTO tickets (betreff, prioritaet, warteschlange, melder_name, melder_email,
		erstellt_von, zustaendig, faellig, status)
		VALUES ($1, $2, NULLIF($3, '')::uuid, $4, $5, $6::uuid, NULLIF($7, '')::uuid, $8,
		        CASE WHEN $7 = '' THEN 'neu' ELSE 'offen' END) RETURNING id`,
		betreff, prio, r.FormValue("warteschlange"), melderName, melderEmail, ich.ID, r.FormValue("zustaendig"),
		frist(prio, time.Now())).Scan(&id)
	if err != nil {
		zurueck(w, r, "/neu", "Fehler: "+err.Error())
		return
	}
	if text != "" {
		a.db.Exec(r.Context(), `INSERT INTO beitraege (ticket, verfasser, art, text) VALUES ($1, $2::uuid, 'antwort', $3)`, id, ich.ID, text)
	}
	zurueck(w, r, fmt.Sprintf("/t/%d", id), "Ticket angelegt")
}

func (a *App) ticketLaden(ctx context.Context, id string) (Ticket, error) {
	return scanTicket(a.db.QueryRow(ctx, "SELECT "+ticketSpalten+ticketVon+" WHERE t.id::text = $1", id))
}

func (a *App) ticketZeigen(w http.ResponseWriter, r *http.Request) {
	ich, _ := a.ich(r)
	t, err := a.ticketLaden(r.Context(), r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT b.id, COALESCE(k.name, 'Unbekannt'), b.art, b.text, b.erstellt
		FROM beitraege b LEFT JOIN konten k ON k.id = b.verfasser WHERE b.ticket = $1 ORDER BY b.erstellt, b.id`, t.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var verlauf []Beitrag
	for rows.Next() {
		var b Beitrag
		if rows.Scan(&b.ID, &b.Verfasser, &b.Art, &b.Text, &b.Erstellt) == nil {
			verlauf = append(verlauf, b)
		}
	}
	rows.Close()
	a.v.zeigen(w, r, "ticket", a.daten(r, fmt.Sprintf("#%d %s", t.ID, t.Betreff), "liste", map[string]any{
		"T": t, "Verlauf": verlauf, "Stati": Stati, "Prioritaeten": Prioritaeten,
		"Schlangen": a.schlangen(r.Context()), "Konten": a.konten(r.Context()), "Ich": ich,
		"Mail": a.mail != nil,
	}))
}

func (a *App) beitragSchreiben(w http.ResponseWriter, r *http.Request) {
	if !pruefeFormular(w, r) {
		return
	}
	ich, err := a.ich(r)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	t, err := a.ticketLaden(r.Context(), r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ziel := fmt.Sprintf("/t/%d", t.ID)
	text := strings.TrimSpace(r.FormValue("text"))
	if text == "" {
		zurueck(w, r, ziel, "Kein Text")
		return
	}
	art := "antwort"
	if r.FormValue("intern") == "ja" {
		art = "notiz"
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback(r.Context())
	tx.Exec(r.Context(), `INSERT INTO beitraege (ticket, verfasser, art, text) VALUES ($1, $2::uuid, $3, $4)`, t.ID, ich.ID, art, text)
	// Die erste Antwort eines anderen als des Melders zählt für die Frist; wer
	// antwortet und niemand ist zuständig, übernimmt das Ticket.
	tx.Exec(r.Context(), `UPDATE tickets SET geaendert = now(),
		erste_antwort = COALESCE(erste_antwort, CASE WHEN $2 = 'antwort' THEN now() END),
		zustaendig = COALESCE(zustaendig, $3::uuid),
		status = CASE WHEN status = 'neu' THEN 'offen' ELSE status END
		WHERE id = $1`, t.ID, art, ich.ID)
	if neu := r.FormValue("status"); gueltig(Stati, neu) && neu != t.Status && neu != "" {
		tx.Exec(r.Context(), `UPDATE tickets SET status = $2,
			geloest_am = CASE WHEN $2 IN ('geloest','geschlossen') THEN now() ELSE NULL END WHERE id = $1`, t.ID, neu)
		tx.Exec(r.Context(), `INSERT INTO beitraege (ticket, verfasser, art, text) VALUES ($1, $2::uuid, 'ereignis', $3)`,
			t.ID, ich.ID, "Status: "+name(Stati, neu))
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	hinweis := "Gespeichert"
	if art == "antwort" && r.FormValue("mail") == "ja" && a.mail != nil && t.MelderEmail != "" && t.MelderEmail != ich.Email {
		if err := a.mail.senden(t.MelderEmail, fmt.Sprintf("[#%d] %s", t.ID, t.Betreff), text+"\n\n"+ich.Name); err != nil {
			hinweis = "Gespeichert, aber die Mail ging nicht raus: " + err.Error()
		} else {
			hinweis = "Gespeichert und an " + t.MelderEmail + " geschickt"
		}
	}
	zurueck(w, r, ziel, hinweis)
}

func (a *App) eigenschaftenSetzen(w http.ResponseWriter, r *http.Request) {
	if !pruefeFormular(w, r) {
		return
	}
	ich, err := a.ich(r)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	t, err := a.ticketLaden(r.Context(), r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	neu := t
	if v := r.FormValue("status"); gueltig(Stati, v) {
		neu.Status = v
	}
	if v := r.FormValue("prioritaet"); gueltig(Prioritaeten, v) {
		neu.Prioritaet = v
	}
	if v := strings.TrimSpace(r.FormValue("betreff")); v != "" {
		neu.Betreff = v
	}
	neu.WarteschlangeID = r.FormValue("warteschlange")
	neu.ZustaendigID = r.FormValue("zustaendig")
	if r.FormValue("uebernehmen") == "ja" {
		neu.ZustaendigID = ich.ID
	}
	var faellig *time.Time
	if v := r.FormValue("faellig"); v != "" {
		if f, err := time.ParseInLocation("2006-01-02T15:04", v, time.Local); err == nil {
			faellig = &f
		}
	}
	if neu.Prioritaet != t.Prioritaet && r.FormValue("faellig") == datumFeld(t.Faellig) {
		f := frist(neu.Prioritaet, t.Erstellt)
		faellig = &f
	}

	var ereignisse []string
	if neu.Status != t.Status {
		ereignisse = append(ereignisse, "Status: "+name(Stati, neu.Status))
	}
	if neu.Prioritaet != t.Prioritaet {
		ereignisse = append(ereignisse, "Priorität: "+name(Prioritaeten, neu.Prioritaet))
	}
	if neu.ZustaendigID != t.ZustaendigID {
		n := "niemand"
		for _, k := range a.konten(r.Context()) {
			if k.ID == neu.ZustaendigID {
				n = k.Name
			}
		}
		ereignisse = append(ereignisse, "Zuständig: "+n)
	}
	if neu.WarteschlangeID != t.WarteschlangeID {
		n := "keine"
		for _, s := range a.schlangen(r.Context()) {
			if s.ID == neu.WarteschlangeID {
				n = s.Name
			}
		}
		ereignisse = append(ereignisse, "Warteschlange: "+n)
	}
	if neu.Betreff != t.Betreff {
		ereignisse = append(ereignisse, "Betreff: "+neu.Betreff)
	}
	_, err = a.db.Exec(r.Context(), `UPDATE tickets SET status=$2, prioritaet=$3, betreff=$4,
		warteschlange=NULLIF($5, '')::uuid, zustaendig=NULLIF($6, '')::uuid, faellig=$7, geaendert=now(),
		geloest_am = CASE WHEN $2 IN ('geloest','geschlossen') THEN COALESCE(geloest_am, now()) ELSE NULL END
		WHERE id=$1`, t.ID, neu.Status, neu.Prioritaet, neu.Betreff, neu.WarteschlangeID, neu.ZustaendigID, faellig)
	if err != nil {
		zurueck(w, r, fmt.Sprintf("/t/%d", t.ID), "Fehler: "+err.Error())
		return
	}
	if len(ereignisse) > 0 {
		a.db.Exec(r.Context(), `INSERT INTO beitraege (ticket, verfasser, art, text) VALUES ($1, $2::uuid, 'ereignis', $3)`,
			t.ID, ich.ID, strings.Join(ereignisse, ", "))
	}
	zurueck(w, r, fmt.Sprintf("/t/%d", t.ID), "Gespeichert")
}

func datumFeld(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Local().Format("2006-01-02T15:04")
}

// ---------- Warteschlangen ----------

func (a *App) warteschlangen(w http.ResponseWriter, r *http.Request) {
	a.v.zeigen(w, r, "warteschlangen", a.daten(r, "Warteschlangen", "warteschlangen", map[string]any{
		"Schlangen": a.schlangen(r.Context()), "Konten": a.konten(r.Context()),
		"Rollen": []Wahl{{"agent", "Agent"}, {"admin", "Admin"}},
	}))
}

func (a *App) warteschlangeSpeichern(w http.ResponseWriter, r *http.Request) {
	if !pruefeFormular(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		zurueck(w, r, "/warteschlangen", "Name fehlt")
		return
	}
	reihe, _ := strconv.Atoi(r.FormValue("reihe"))
	var err error
	if id := r.FormValue("id"); id != "" {
		_, err = a.db.Exec(r.Context(), `UPDATE warteschlangen SET name=$2, reihe=$3 WHERE id::text=$1`, id, name, reihe)
	} else {
		_, err = a.db.Exec(r.Context(), `INSERT INTO warteschlangen (name, reihe) VALUES ($1, $2)`, name, reihe)
	}
	if err != nil {
		zurueck(w, r, "/warteschlangen", "Fehler: "+err.Error())
		return
	}
	zurueck(w, r, "/warteschlangen", "Gespeichert")
}

func (a *App) warteschlangeLoeschen(w http.ResponseWriter, r *http.Request) {
	if !pruefeFormular(w, r) {
		return
	}
	if _, err := a.db.Exec(r.Context(), `DELETE FROM warteschlangen WHERE id::text=$1`, r.PathValue("id")); err != nil {
		zurueck(w, r, "/warteschlangen", "Fehler: "+err.Error())
		return
	}
	zurueck(w, r, "/warteschlangen", "Gelöscht, die Tickets darin haben jetzt keine Warteschlange")
}

// apiListe: offene Tickets als JSON, etwa für n8n oder Boardr.
func (a *App) apiListe(w http.ResponseWriter, r *http.Request) {
	ich, _ := a.ich(r)
	q := r.URL.Query()
	l, err := a.suchen(r.Context(), Filter{Status: q.Get("status"), Warteschlange: q.Get("warteschlange"),
		Zustaendig: q.Get("zustaendig"), Suche: q.Get("q"), Prioritaet: q.Get("prioritaet")}, ich.ID)
	if err != nil {
		alsJSON(w, 500, map[string]string{"fehler": err.Error()})
		return
	}
	type aus struct {
		ID         int64      `json:"id"`
		Betreff    string     `json:"betreff"`
		Status     string     `json:"status"`
		Prioritaet string     `json:"prioritaet"`
		Zustaendig string     `json:"zustaendig"`
		Faellig    *time.Time `json:"faellig"`
	}
	raus := []aus{}
	for _, t := range l {
		raus = append(raus, aus{t.ID, t.Betreff, t.Status, t.Prioritaet, t.Zustaendig, t.Faellig})
	}
	alsJSON(w, 200, raus)
}

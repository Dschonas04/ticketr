package main

import (
	"context"
	"net/http"
	"strings"
)

// Baustein ist eine Vorlage für Antworten.
type Baustein struct{ ID, Name, Text string }

func (a *App) bausteine(ctx context.Context) ([]Baustein, error) {
	rows, err := a.db.Query(ctx, `SELECT id::text, name, text FROM vorlagen ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []Baustein
	for rows.Next() {
		var b Baustein
		if err := rows.Scan(&b.ID, &b.Name, &b.Text); err != nil {
			return nil, err
		}
		l = append(l, b)
	}
	return l, rows.Err()
}

func (a *App) vorlagenSeite(w http.ResponseWriter, r *http.Request) {
	l, err := a.bausteine(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.v.zeigen(w, r, "vorlagen", a.daten(r, "Antwortvorlagen", "warteschlangen", map[string]any{"Vorlagen": l}))
}

func (a *App) vorlageSpeichern(w http.ResponseWriter, r *http.Request) {
	if !pruefeFormular(w, r) {
		return
	}
	name, text := strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("text"))
	if name == "" || text == "" {
		zurueck(w, r, "/vorlagen", "Name und Text gehören dazu")
		return
	}
	var err error
	if id := r.FormValue("id"); id != "" {
		_, err = a.db.Exec(r.Context(), `UPDATE vorlagen SET name=$2, text=$3 WHERE id::text=$1`, id, name, text)
	} else {
		_, err = a.db.Exec(r.Context(), `INSERT INTO vorlagen (name, text) VALUES ($1, $2)`, name, text)
	}
	if err != nil {
		zurueck(w, r, "/vorlagen", "Nicht gespeichert, gibt es den Namen schon?")
		return
	}
	zurueck(w, r, "/vorlagen", "Vorlage gespeichert")
}

func (a *App) vorlageLoeschen(w http.ResponseWriter, r *http.Request) {
	if !pruefeFormular(w, r) {
		return
	}
	if _, err := a.db.Exec(r.Context(), `DELETE FROM vorlagen WHERE id::text=$1`, r.PathValue("id")); err != nil {
		zurueck(w, r, "/vorlagen", "Fehler: "+err.Error())
		return
	}
	zurueck(w, r, "/vorlagen", "Vorlage gelöscht")
}

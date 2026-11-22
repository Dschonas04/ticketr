package main

import (
	"context"
	"net/http"
	"net/url"
)

// Ansicht ist ein gespeicherter Filter einer Person.
type Ansicht struct{ ID, Name, Abfrage string }

func (an Ansicht) Adresse() string { return "/?" + an.Abfrage }

func (a *App) ansichten(ctx context.Context, konto string) ([]Ansicht, error) {
	rows, err := a.db.Query(ctx, `SELECT id::text, name, abfrage FROM ansichten WHERE konto::text = $1 ORDER BY name`, konto)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []Ansicht
	for rows.Next() {
		var an Ansicht
		if err := rows.Scan(&an.ID, &an.Name, &an.Abfrage); err != nil {
			return nil, err
		}
		l = append(l, an)
	}
	return l, rows.Err()
}

func (a *App) ansichtSpeichern(w http.ResponseWriter, r *http.Request) {
	if !pruefeFormular(w, r) {
		return
	}
	ich, err := a.ich(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Nur die bekannten Felder: die Abfrage wird später zu einem Link.
	q := url.Values{}
	for _, f := range []string{"q", "status", "warteschlange", "zustaendig", "prioritaet"} {
		if v := einzeilig(r.PostFormValue(f), 200); v != "" {
			q.Set(f, v)
		}
	}
	name := einzeilig(r.PostFormValue("name"), 60)
	if name == "" {
		zurueck(w, r, "/?"+q.Encode(), "Die Ansicht braucht einen Namen")
		return
	}
	if _, err := a.db.Exec(r.Context(), `INSERT INTO ansichten (konto, name, abfrage) VALUES ($1::uuid, $2, $3)
		ON CONFLICT (konto, name) DO UPDATE SET abfrage = $3`, ich.ID, name, q.Encode()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	zurueck(w, r, "/?"+q.Encode(), "Ansicht gespeichert")
}

func (a *App) ansichtLoeschen(w http.ResponseWriter, r *http.Request) {
	if !pruefeFormular(w, r) {
		return
	}
	ich, err := a.ich(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := a.db.Exec(r.Context(), `DELETE FROM ansichten WHERE id::text = $1 AND konto::text = $2`, r.PathValue("id"), ich.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	zurueck(w, r, "/", "Ansicht gelöscht")
}

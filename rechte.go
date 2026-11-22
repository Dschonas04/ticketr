package main

import "net/http"

// nurAdmin: Warteschlangen und Rollen ändern nur Admins. Das Menü blendet die
// Verwaltung für Agenten aus, entscheidend ist aber diese Prüfung.
func (a *App) nurAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if k, err := a.ich(r); err != nil || k.Rolle != "admin" {
			http.Error(w, "Nur für Admins", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

// istAdmin sagt den Seiten, ob sie die Verwaltung zeigen.
func (a *App) istAdmin(r *http.Request) bool {
	k, err := a.ich(r)
	return err == nil && k.Rolle == "admin"
}

// rolleSetzen: POST /konten/{id}/rolle. Sich selbst stuft niemand herab, so
// bleibt immer jemand für die Verwaltung übrig.
func (a *App) rolleSetzen(w http.ResponseWriter, r *http.Request) {
	if !pruefeFormular(w, r) {
		return
	}
	ich, err := a.ich(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	id, rolle := r.PathValue("id"), r.FormValue("rolle")
	if rolle != "admin" && rolle != "agent" {
		zurueck(w, r, "/warteschlangen", "Diese Rolle gibt es nicht")
		return
	}
	if id == ich.ID {
		zurueck(w, r, "/warteschlangen", "Die eigene Rolle ändert jemand anderes")
		return
	}
	e, err := a.db.Exec(r.Context(), `UPDATE konten SET rolle=$2 WHERE id::text=$1`, id, rolle)
	if err != nil {
		zurueck(w, r, "/warteschlangen", "Fehler: "+err.Error())
		return
	}
	if e.RowsAffected() == 0 {
		http.NotFound(w, r)
		return
	}
	zurueck(w, r, "/warteschlangen", "Rolle gespeichert")
}

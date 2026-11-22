package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// dauerText: Stunden als lesbare Dauer, ohne Wert "keine Daten".
func dauerText(stunden *float64) string {
	switch {
	case stunden == nil:
		return "keine Daten"
	case *stunden < 1:
		return fmt.Sprintf("%.0f min", *stunden*60)
	case *stunden < 48:
		return strings.Replace(fmt.Sprintf("%.1f h", *stunden), ".", ",", 1)
	}
	return strings.Replace(fmt.Sprintf("%.1f Tage", *stunden/24), ".", ",", 1)
}

type BerichtZeile struct {
	Name                      string
	Offen, Ueber, Neu, Fertig int
}

func (a *App) berichte(w http.ResponseWriter, r *http.Request) {
	tage, _ := strconv.Atoi(r.URL.Query().Get("tage"))
	if tage != 7 && tage != 90 && tage != 365 {
		tage = 30
	}
	var neu, fertig, mitFrist, inFrist int
	var antwort, loesung *float64
	err := a.db.QueryRow(r.Context(), `WITH z AS (SELECT now() - make_interval(days => $1) AS ab)
		SELECT count(*) FILTER (WHERE t.erstellt >= z.ab),
		       count(*) FILTER (WHERE t.geloest_am >= z.ab),
		       count(*) FILTER (WHERE t.geloest_am >= z.ab AND t.faellig IS NOT NULL),
		       count(*) FILTER (WHERE t.geloest_am >= z.ab AND t.geloest_am <= t.faellig),
		       (avg(extract(epoch FROM t.erste_antwort - t.erstellt) / 3600) FILTER (WHERE t.erste_antwort >= z.ab))::float8,
		       (avg(extract(epoch FROM t.geloest_am - t.erstellt) / 3600) FILTER (WHERE t.geloest_am >= z.ab))::float8
		FROM tickets t, z`, tage).Scan(&neu, &fertig, &mitFrist, &inFrist, &antwort, &loesung)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	quote := "keine Daten"
	if mitFrist > 0 {
		quote = strconv.Itoa(inFrist*100/mitFrist) + " %"
	}
	gruppe := func(spalte, verbund string) ([]BerichtZeile, error) {
		rows, err := a.db.Query(r.Context(), `SELECT `+spalte+`,
			count(*) FILTER (WHERE t.status NOT IN ('geloest','geschlossen')),
			count(*) FILTER (WHERE t.status NOT IN ('geloest','geschlossen') AND t.faellig < now()),
			count(*) FILTER (WHERE t.erstellt >= now() - make_interval(days => $1)),
			count(*) FILTER (WHERE t.geloest_am >= now() - make_interval(days => $1))
			FROM tickets t `+verbund+` GROUP BY 1 ORDER BY 2 DESC, 1`, tage)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var l []BerichtZeile
		for rows.Next() {
			var z BerichtZeile
			if err := rows.Scan(&z.Name, &z.Offen, &z.Ueber, &z.Neu, &z.Fertig); err != nil {
				return nil, err
			}
			l = append(l, z)
		}
		return l, rows.Err()
	}
	schlangen, err := gruppe(`COALESCE(w.name, 'Ohne Warteschlange')`, `LEFT JOIN warteschlangen w ON w.id = t.warteschlange`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	personen, err := gruppe(`COALESCE(k.name, 'Niemand')`, `LEFT JOIN konten k ON k.id = t.zustaendig`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.v.zeigen(w, r, "berichte", a.daten(r, "Berichte", "berichte", map[string]any{
		"Tage": tage, "Zeiten": []int{7, 30, 90, 365}, "Neu": neu, "Fertig": fertig, "Quote": quote,
		"Antwort": dauerText(antwort), "Loesung": dauerText(loesung),
		"Gruppen": []map[string]any{{"Titel": "Nach Warteschlange", "Zeilen": schlangen}, {"Titel": "Nach Zuständigkeit", "Zeilen": personen}},
	}))
}

package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSeitenRendern(t *testing.T) {
	v := ladeVorlagen()
	jetzt := time.Now()
	frueher := jetzt.Add(-time.Hour)
	tk := Ticket{ID: 7, Betreff: "Drucker druckt nicht", Status: "offen", Prioritaet: "hoch", Warteschlange: "IT",
		WarteschlangeID: "w1", MelderName: "Anna", MelderEmail: "anna@example.org", Zustaendig: "Jonas", ZustaendigID: "k1",
		Faellig: &frueher, Erstellt: jetzt, Geaendert: jetzt, Beitraege: 2}
	konten := []Konto{{ID: "k1", Name: "Jonas", Email: "j@example.org", Rolle: "admin"}}
	schlangen := []Schlange{{ID: "w1", Name: "IT", Offen: 1}}
	faelle := map[string]map[string]any{
		"liste": {"Tickets": []Ticket{tk}, "F": Filter{}, "Stati": Stati, "Prioritaeten": Prioritaeten,
			"Schlangen": schlangen, "Konten": konten, "Zahlen": struct{ Offen, Mir, Ueber, Neu int }{1, 1, 1, 0}},
		"neu": {"Prioritaeten": Prioritaeten, "Schlangen": schlangen, "Konten": konten, "Ich": konten[0]},
		"ticket": {"T": tk, "Verlauf": []Beitrag{{Verfasser: "Anna", Art: "antwort", Text: "Geht nicht\nmehr", Erstellt: jetzt},
			{Verfasser: "Jonas", Art: "notiz", Text: "Toner?", Erstellt: jetzt}, {Verfasser: "Jonas", Art: "ereignis", Text: "Status: Offen", Erstellt: jetzt}},
			"Stati": Stati, "Prioritaeten": Prioritaeten, "Schlangen": schlangen, "Konten": konten, "Ich": konten[0], "Mail": true},
		"warteschlangen": {"Schlangen": schlangen, "Konten": konten},
	}
	for name, d := range faelle {
		rec := httptest.NewRecorder()
		v.zeigen(rec, httptest.NewRequest("GET", "/", nil), name, Daten{App: "Ticketr", D: d})
		b := rec.Body.String()
		if !strings.Contains(b, "</html>") {
			t.Errorf("%s bricht ab: %s", name, b[max(0, len(b)-300):])
		}
	}
}

func TestFrist(t *testing.T) {
	ab := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if frist("dringend", ab) != ab.Add(4*time.Hour) || frist("unbekannt", ab) != ab.Add(72*time.Hour) {
		t.Error("Frist")
	}
	tk := Ticket{Status: "geloest", Faellig: &ab}
	if tk.Ueberfaellig() {
		t.Error("gelöstes Ticket überfällig")
	}
}

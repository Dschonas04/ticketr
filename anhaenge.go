package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
)

const (
	anhangMax    = 10 << 20
	anhangAnzahl = 5
)

type Anhang struct {
	ID      string
	Beitrag int64
	Name    string
	Groesse int64
}

func (d Anhang) GroesseText() string {
	switch {
	case d.Groesse >= 1<<20:
		return strings.Replace(fmt.Sprintf("%.1f MB", float64(d.Groesse)/(1<<20)), ".", ",", 1)
	case d.Groesse >= 1<<10:
		return fmt.Sprintf("%d KB", d.Groesse>>10)
	}
	return fmt.Sprintf("%d B", d.Groesse)
}

type datei struct {
	name  string
	daten []byte
}

// dateiName lässt vom mitgeschickten Namen nur den letzten Teil ohne
// Steuer- und Formatzeichen übrig.
func dateiName(roh string) string {
	n := path.Base(strings.ReplaceAll(strings.ToValidUTF8(roh, "_"), `\`, "/"))
	n = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, n))
	if r := []rune(n); len(r) > 150 {
		n = string(r[len(r)-150:])
	}
	if n == "" || n == "." || n == ".." || n == "/" {
		return "anhang"
	}
	return n
}

// dateienLesen nimmt die Dateien aus dem Feld dateien.
func dateienLesen(r *http.Request) ([]datei, error) {
	if r.MultipartForm == nil {
		return nil, nil
	}
	felder := r.MultipartForm.File["dateien"]
	if len(felder) > anhangAnzahl {
		return nil, fmt.Errorf("höchstens %d Dateien je Beitrag", anhangAnzahl)
	}
	var l []datei
	for _, f := range felder {
		if f.Filename == "" {
			continue
		}
		if f.Size > anhangMax {
			return nil, fmt.Errorf("%s ist größer als 10 MB", dateiName(f.Filename))
		}
		h, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(h, anhangMax+1))
		h.Close()
		if err != nil {
			return nil, err
		}
		l = append(l, datei{dateiName(f.Filename), b})
	}
	return l, nil
}

func dateienSpeichern(ctx context.Context, tx pgx.Tx, ticket, beitrag int64, l []datei) error {
	for _, d := range l {
		if _, err := tx.Exec(ctx, `INSERT INTO anhaenge (ticket, beitrag, name, groesse, daten) VALUES ($1, $2, $3, $4, $5)`,
			ticket, beitrag, d.name, len(d.daten), d.daten); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) anhaengeVon(ctx context.Context, ticket int64) (map[int64][]Anhang, error) {
	rows, err := a.db.Query(ctx, `SELECT id::text, beitrag, name, groesse FROM anhaenge WHERE ticket=$1 ORDER BY erstellt, name`, ticket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int64][]Anhang{}
	for rows.Next() {
		var d Anhang
		if err := rows.Scan(&d.ID, &d.Beitrag, &d.Name, &d.Groesse); err != nil {
			return nil, err
		}
		m[d.Beitrag] = append(m[d.Beitrag], d)
	}
	return m, rows.Err()
}

// anhangLaden gibt immer zum Herunterladen aus, nie zum Anzeigen: eine
// hochgeladene HTML- oder SVG-Datei läuft so nicht unter der Adresse der App.
func (a *App) anhangLaden(w http.ResponseWriter, r *http.Request) {
	var name string
	var daten []byte
	if err := a.db.QueryRow(r.Context(), `SELECT name, daten FROM anhaenge WHERE id::text=$1 AND ticket::text=$2`,
		r.PathValue("anhang"), r.PathValue("id")).Scan(&name, &daten); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(daten))
}

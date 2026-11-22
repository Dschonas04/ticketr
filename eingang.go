package main

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
)

// eingang ist, was von außen kommt: aus dem Portal oder per Mail.
type eingang struct {
	betreff, text, name, email string
	portalHash                 string
	dateien                    []datei
}

// sauber macht fremden Text speicherbar: gültiges UTF-8 ohne Nullzeichen,
// höchstens max Zeichen. Postgres lehnt beides sonst ab.
func sauber(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ToValidUTF8(s, "?"), "\x00", ""))
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

// einzeilig: für Betreff, Namen und Adressen.
func einzeilig(s string, max int) string {
	return sauber(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s), max)
}

// passendeSchlange: die erste Warteschlange, deren Stichwort im Text steht,
// sonst die oberste. Leer, wenn es keine gibt.
func passendeSchlange(ctx context.Context, tx pgx.Tx, text string) (string, error) {
	rows, err := tx.Query(ctx, `SELECT id::text, stichworte FROM warteschlangen ORDER BY reihe, name`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	text = strings.ToLower(text)
	var erste, treffer string
	for rows.Next() {
		var id, worte string
		if err := rows.Scan(&id, &worte); err != nil {
			return "", err
		}
		if erste == "" {
			erste = id
		}
		for _, w := range strings.Split(worte, ",") {
			if w = strings.ToLower(strings.TrimSpace(w)); treffer == "" && w != "" && strings.Contains(text, w) {
				treffer = id
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if treffer != "" {
		return treffer, nil
	}
	return erste, nil
}

func ticketVonAussen(ctx context.Context, tx pgx.Tx, e eingang) (int64, error) {
	schlange, err := passendeSchlange(ctx, tx, e.betreff+" "+e.text)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO tickets (betreff, warteschlange, melder_name, melder_email, faellig, portal_hash)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, NULLIF($6, '')) RETURNING id`,
		e.betreff, schlange, e.name, e.email, frist("normal", time.Now()), e.portalHash).Scan(&id); err != nil {
		return 0, err
	}
	return id, beitragVonAussen(ctx, tx, id, e)
}

func beitragVonAussen(ctx context.Context, tx pgx.Tx, ticket int64, e eingang) error {
	von := e.name
	if von == "" {
		von = e.email
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO beitraege (ticket, art, text, von_name) VALUES ($1, 'antwort', $2, $3) RETURNING id`,
		ticket, e.text, von).Scan(&id); err != nil {
		return err
	}
	if err := dateienSpeichern(ctx, tx, ticket, id, e.dateien); err != nil {
		return err
	}
	// Wer sich meldet, holt ein wartendes oder erledigtes Ticket zurück.
	_, err := tx.Exec(ctx, `UPDATE tickets SET geaendert = now(), geloest_am = NULL,
		status = CASE WHEN status = 'neu' THEN 'neu' ELSE 'offen' END WHERE id = $1`, ticket)
	return err
}

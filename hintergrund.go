package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"
)

// antwortBis: ein Viertel der Lösungsfrist bleibt für die erste Antwort.
func antwortBis(prio string, erstellt time.Time) time.Time {
	return erstellt.Add(frist(prio, erstellt).Sub(erstellt) / 4)
}

func (t Ticket) AntwortBis() time.Time { return antwortBis(t.Prioritaet, t.Erstellt) }
func (t Ticket) AntwortUeberfaellig() bool {
	return t.Offen() && t.ErsteAntwort == nil && time.Now().After(t.AntwortBis())
}

// regelmaessig läuft bis zum Ende der App: jede Minute Fristen und Post.
func regelmaessig(ctx context.Context, was string, tu func(context.Context) error) {
	uhr := time.NewTicker(time.Minute)
	defer uhr.Stop()
	for {
		einmal, ende := context.WithTimeout(ctx, 5*time.Minute)
		if err := tu(einmal); err != nil && ctx.Err() == nil {
			log.Printf("%s: %v", was, err)
		}
		ende()
		select {
		case <-ctx.Done():
			return
		case <-uhr.C:
		}
	}
}

// fristenPruefen erinnert je Frist einmal, kurz bevor sie abläuft. Eine neue
// Frist erinnert wieder.
func (a *App) fristenPruefen(ctx context.Context) error {
	type faellig struct {
		id             int64
		betreff, email string
		antwort        bool
		bis            time.Time
	}
	rows, err := a.db.Query(ctx, `SELECT t.id, t.betreff, t.prioritaet, t.erstellt, t.faellig, t.erste_antwort,
		t.erinnert_antwort, t.erinnert_frist, COALESCE(k.email, '')
		FROM tickets t LEFT JOIN konten k ON k.id = t.zustaendig
		WHERE t.status NOT IN ('geloest','geschlossen')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var l []faellig
	jetzt := time.Now()
	for rows.Next() {
		var id int64
		var betreff, prio, email string
		var erstellt time.Time
		var loesung, erste, gemahntA, gemahntF *time.Time
		if err := rows.Scan(&id, &betreff, &prio, &erstellt, &loesung, &erste, &gemahntA, &gemahntF, &email); err != nil {
			return err
		}
		if bis := antwortBis(prio, erstellt).Truncate(time.Microsecond); erste == nil &&
			jetzt.After(bis.Add(-15*time.Minute)) && (gemahntA == nil || !gemahntA.Equal(bis)) {
			l = append(l, faellig{id, betreff, email, true, bis})
		}
		if loesung != nil && jetzt.After(loesung.Add(-time.Hour)) && (gemahntF == nil || !gemahntF.Equal(*loesung)) {
			l = append(l, faellig{id, betreff, email, false, *loesung})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	for _, f := range l {
		spalte, was := "erinnert_frist", "Lösung"
		if f.antwort {
			spalte, was = "erinnert_antwort", "erste Antwort"
		}
		e, err := a.db.Exec(ctx, `WITH g AS (
				UPDATE tickets SET `+spalte+` = $2::timestamptz WHERE id = $1 AND `+spalte+` IS DISTINCT FROM $2::timestamptz RETURNING id)
			INSERT INTO beitraege (ticket, art, text, von_name) SELECT id, 'ereignis', $3, 'Ticketr' FROM g`,
			f.id, f.bis, "Frist für "+was+": "+f.bis.Local().Format("02.01.2006 15:04"))
		if err != nil {
			return err
		}
		if e.RowsAffected() == 0 {
			continue
		}
		a.melden("ticket.frist", f.id, f.betreff)
		if a.mail != nil && f.email != "" {
			if err := a.mail.senden(f.email, fmt.Sprintf("Frist: [#%d] %s", f.id, f.betreff),
				"Die Frist für "+was+" endet am "+f.bis.Local().Format("02.01.2006 15:04")+"."); err != nil {
				log.Printf("erinnerung %d: %v", f.id, err)
			}
		}
	}
	return nil
}

// Webhook meldet Ereignisse, etwa an n8n. Mit Geheimnis ist der Inhalt
// unterschrieben (X-Ticketr-Signatur: sha256=<HMAC>).
type Webhook struct {
	ziel, geheim string
	klient       *http.Client
	plaetze      chan struct{}
}

func neuerWebhook(ziel, geheim string) *Webhook {
	if ziel == "" {
		return nil
	}
	if u, err := url.Parse(ziel); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		log.Printf("webhook: TICKETR_WEBHOOK ist keine http-Adresse, bleibt aus")
		return nil
	}
	return &Webhook{ziel: ziel, geheim: geheim, plaetze: make(chan struct{}, 8), klient: &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (a *App) melden(was string, id int64, betreff string) {
	if a.webhook == nil {
		return
	}
	select {
	case a.webhook.plaetze <- struct{}{}:
		go func() {
			defer func() { <-a.webhook.plaetze }()
			if err := a.webhook.senden(was, id, betreff); err != nil {
				log.Printf("webhook %s: %v", was, err)
			}
		}()
	default:
		log.Printf("webhook %s: zu viele gleichzeitig, übergangen", was)
	}
}

func (h *Webhook) senden(was string, id int64, betreff string) error {
	b, err := json.Marshal(map[string]any{"ereignis": was, "ticket": id, "betreff": betreff, "zeit": time.Now().UTC()})
	if err != nil {
		return err
	}
	r, err := http.NewRequest(http.MethodPost, h.ziel, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("Adresse ungültig")
	}
	r.Header.Set("Content-Type", "application/json")
	if h.geheim != "" {
		m := hmac.New(sha256.New, []byte(h.geheim))
		m.Write(b)
		r.Header.Set("X-Ticketr-Signatur", "sha256="+hex.EncodeToString(m.Sum(nil)))
	}
	res, err := h.klient.Do(r)
	if err != nil {
		// Die Adresse kann einen Schlüssel enthalten und gehört nicht ins Log.
		return fmt.Errorf("Ziel nicht erreichbar")
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("Antwort %d", res.StatusCode)
	}
	return nil
}

# Ticketr

Ticketsystem für Anfragen, Störungen und Aufgaben, mit E-Mail-Eingang und Kundenportal.

**Stand: Grundgerüst.** Der Server startet und zeigt eine Startseite, die
Funktionen unten sind geplant.

## Geplant

* **Tickets** mit Status, Priorität, Kategorie, Zuständigkeit und Fälligkeit
* **Warteschlangen** je Team, mit Regeln für die automatische Zuordnung
* **E-Mail-Eingang** per IMAP: neue Mail wird Ticket, Antwort hängt sich an; Antworten gehen per SMTP raus
* **Kundenportal**: Anfragen stellen und den Stand sehen, ohne Zugang zum internen Bereich
* **SLA-Fristen** für erste Antwort und Lösung, mit Erinnerung vor Ablauf
* **Interne Notizen** neben den öffentlichen Antworten, Anhänge, Vorlagen für Antworten
* **Volltextsuche**, gespeicherte Filter, Berichte (offene Tickets, Bearbeitungszeit)
* **Anmeldung über Keycloak** (OIDC), Webhooks für n8n

## Schritte

1. Datenmodell und Ticketliste mit Anlegen, Bearbeiten, Status
2. Konten und Anmeldung über Keycloak, Rollen Agent und Admin
3. Verlauf je Ticket: Antworten, Notizen, Anhänge
4. E-Mail-Eingang (IMAP) und Versand (SMTP)
5. Kundenportal
6. SLA-Fristen, Erinnerungen, Berichte

## Starten

```bash
cp .env.beispiel .env            # Kennwort für die Datenbank eintragen
docker compose up -d --build
```

Danach unter `http://<host>:8350`. Im Homelab kommt TLS über Caddy mit
der Homelab-CA davor, der Dienst selbst lauscht dann auf `127.0.0.1:28350`.

## Entwickeln

```bash
go run .
go test ./...
```

Go 1.27.4, Datenbank PostgreSQL. Das Schema steht in `schema.sql`.

## Lizenz

MIT, siehe [LICENSE](LICENSE).

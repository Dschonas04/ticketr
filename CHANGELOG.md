# Änderungen

## Unveröffentlicht

- Tickets anlegen, bearbeiten, übernehmen; Status, Priorität, Warteschlange, Zuständigkeit, Frist nach Priorität, überfällige Tickets markiert.
- Verlauf je Ticket: Antworten, interne Notizen, Änderungen als Ereignisse; erste Antwort wird festgehalten.
- Liste mit Filtern (Status, Warteschlange, Zuständig, Priorität) und Suche über Betreff, Meldende und Text, `#Nummer` springt.
- Antworten per Mail an die meldende Person, wenn `TICKETR_SMTP` gesetzt ist.
- `GET /api/tickets` als JSON.
- Anmeldung über Keycloak oder eigene Konten; die Ersteinrichtung braucht den Code aus dem Log.
- Grundgerüst: Server mit Startseite, /healthz und /api/version, Schemaentwurf, Docker, Prüfung in GitHub Actions.

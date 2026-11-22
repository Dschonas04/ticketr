# Änderungen

## Unveröffentlicht

- Sicherheit: Die SSO-Einstellung hat einen Leser für Prüfung und Speichern und lehnt Unlesbares ab.
- Doppelte id im Seitengerüst behoben.
- Sicherheit: Die Sperre der Verwaltung wartet weder auf den Browser noch auf den Anbieter und hat eine Zeitgrenze.
- Kundenportal, E-Mail-Eingang per IMAP, Fristen für die erste Antwort mit Erinnerung.
- Warteschlangen mit Stichworten, gespeicherte Ansichten, Webhook mit Unterschrift.
- SMTP mit Zeitgrenze.
- Sicherheit: Änderungen an Konten, Rollen und Anmeldung laufen nacheinander, Prüfung und Schreiben unter einer Sperre.
- Sicherheit: Mit einem Konto enden seine Rechte; geprüft wird erst, wenn die Anfrage vollständig da ist.
- Anhänge an Tickets und Antworten (immer als Download), Antwortvorlagen, Berichte mit Antwort- und Lösungszeiten.
- Liste mit Kennzahlen und Filter für Überfälliges.
- Sicherheit: Admin wird man nur mit dem Einrichtungscode oder über `TICKETR_ADMIN`, nicht durch die erste Anmeldung. Kennungen von eigenen Konten und SSO sind getrennt.
- Oberfläche überarbeitet: Kopfzeile, Tabellen und Felder am Handy, Anmeldeseite in den Farben der App.
- Logo auf der Anmeldeseite wird wieder ausgeliefert, der Hinweis zur Ersteinrichtung stimmt.
- Sicherheit: Warteschlangen und Rollen ändern nur noch Admins.
- `GET /api/tickets` beachtet den Filter `prioritaet`.
- Tickets anlegen, bearbeiten, übernehmen; Status, Priorität, Warteschlange, Zuständigkeit, Frist nach Priorität, überfällige Tickets markiert.
- Verlauf je Ticket: Antworten, interne Notizen, Änderungen als Ereignisse; erste Antwort wird festgehalten.
- Liste mit Filtern (Status, Warteschlange, Zuständig, Priorität) und Suche über Betreff, Meldende und Text, `#Nummer` springt.
- Antworten per Mail an die meldende Person, wenn `TICKETR_SMTP` gesetzt ist.
- `GET /api/tickets` als JSON.
- Anmeldung über Keycloak oder eigene Konten; die Ersteinrichtung braucht den Code aus dem Log.
- Grundgerüst: Server mit Startseite, /healthz und /api/version, Schemaentwurf, Docker, Prüfung in GitHub Actions.

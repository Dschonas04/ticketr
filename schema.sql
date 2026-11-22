-- Datenbankschema von Ticketr. Idempotent: wird bei jedem Start eingespielt.
CREATE TABLE IF NOT EXISTS konten (
	id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	kennung   text NOT NULL UNIQUE,                -- Keycloak-sub oder lokaler Name
	name      text NOT NULL,
	email     text NOT NULL DEFAULT '',
	rolle     text NOT NULL DEFAULT 'agent',       -- agent | admin
	zuletzt   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS warteschlangen (
	id     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	name   text NOT NULL UNIQUE,
	reihe  int NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS tickets (
	id             bigserial PRIMARY KEY,
	betreff        text NOT NULL,
	status         text NOT NULL DEFAULT 'neu',     -- neu | offen | wartet | geloest | geschlossen
	prioritaet     text NOT NULL DEFAULT 'normal',  -- niedrig | normal | hoch | dringend
	warteschlange  uuid REFERENCES warteschlangen(id) ON DELETE SET NULL,
	melder_name    text NOT NULL DEFAULT '',
	melder_email   text NOT NULL DEFAULT '',
	erstellt_von   uuid REFERENCES konten(id) ON DELETE SET NULL,
	zustaendig     uuid REFERENCES konten(id) ON DELETE SET NULL,
	faellig        timestamptz,
	erste_antwort  timestamptz,
	geloest_am     timestamptz,
	erstellt       timestamptz NOT NULL DEFAULT now(),
	geaendert      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS tickets_status ON tickets(status);

-- Verlauf: Antworten, interne Notizen und Änderungen am Ticket.
CREATE TABLE IF NOT EXISTS beitraege (
	id        bigserial PRIMARY KEY,
	ticket    bigint NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
	verfasser uuid REFERENCES konten(id) ON DELETE SET NULL,
	art       text NOT NULL DEFAULT 'antwort',      -- antwort | notiz | ereignis
	text      text NOT NULL,
	erstellt  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS beitraege_ticket ON beitraege(ticket, erstellt);

INSERT INTO warteschlangen (name, reihe) VALUES ('Allgemein', 0) ON CONFLICT DO NOTHING;

-- Dateien an Beiträgen. Sie liegen in der Datenbank und damit in jeder Sicherung.
CREATE TABLE IF NOT EXISTS anhaenge (
	id       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	ticket   bigint NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
	beitrag  bigint NOT NULL REFERENCES beitraege(id) ON DELETE CASCADE,
	name     text NOT NULL,
	groesse  bigint NOT NULL,
	daten    bytea NOT NULL,
	erstellt timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS anhaenge_ticket ON anhaenge(ticket);

-- Textbausteine für Antworten.
CREATE TABLE IF NOT EXISTS vorlagen (
	id    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	name  text NOT NULL UNIQUE,
	text  text NOT NULL
);

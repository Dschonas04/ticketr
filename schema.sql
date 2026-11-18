-- Datenbankschema von Ticketr (Entwurf).
CREATE TABLE IF NOT EXISTS konten (
	id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	name       text NOT NULL,
	email      text NOT NULL UNIQUE,
	rolle      text NOT NULL DEFAULT 'kunde', -- kunde | agent | admin
	erstellt   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS warteschlangen (
	id   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	name text NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS tickets (
	id             bigserial PRIMARY KEY,           -- die Nummer, die Menschen nennen
	betreff        text NOT NULL,
	status         text NOT NULL DEFAULT 'neu',     -- neu | offen | wartet | geloest | geschlossen
	prioritaet     text NOT NULL DEFAULT 'normal',  -- niedrig | normal | hoch | dringend
	warteschlange  uuid REFERENCES warteschlangen(id),
	melder         uuid REFERENCES konten(id),
	zustaendig     uuid REFERENCES konten(id),
	faellig        timestamptz,
	erstellt       timestamptz NOT NULL DEFAULT now(),
	geaendert      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS beitraege (
	id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	ticket    bigint NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
	verfasser uuid REFERENCES konten(id),
	intern    boolean NOT NULL DEFAULT false,       -- Notiz, für Kunden unsichtbar
	text      text NOT NULL,
	erstellt  timestamptz NOT NULL DEFAULT now()
);

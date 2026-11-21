package chatsvr

import (
	"database/sql"
	"testing"
)

func TestLocalSchemaV1(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	columns := func(table string) map[string]bool {
		rows, err := app.store.db.Query(`pragma table_info(` + table + `)`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]bool{}
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull, pk int
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
				t.Fatal(err)
			}
			out[name] = true
		}
		return out
	}

	if !columns("sessions")["server_id"] {
		t.Fatal("sessions.server_id missing")
	}
	if !columns("attachments")["file_uploaded_at"] {
		t.Fatal("attachments.file_uploaded_at missing")
	}
	var version string
	if err := app.store.db.QueryRow(`select v from kv where k='schema_version'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != "2" {
		t.Fatalf("schema version = %q", version)
	}
}

func TestLegacySessionsGetsServerIDColumn(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/legacy.db")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`create table sessions (
		id text primary key, title text not null default '', owner_device_id text not null,
		sync_state text not null default 'local', created_at integer not null, updated_at integer not null
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`select count(*) from pragma_table_info('sessions') where name='server_id'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("server_id column count = %d", count)
	}
}

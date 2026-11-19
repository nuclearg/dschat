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
	if version != "1" {
		t.Fatalf("schema version = %q", version)
	}
}

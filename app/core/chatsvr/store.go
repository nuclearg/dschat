package chatsvr

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"dschat/app/core/apierr"
	"dschat/app/core/model"
	"dschat/app/core/xdb"
)

const (
	SyncLocal   = "local"
	SyncSynced  = "synced"
	SyncPending = "pending"
)

type Store struct {
	db *sql.DB
}

func OpenStore(dataDir string) (*Store, error) {
	db, err := xdb.Open(filepath.Join(dataDir, "chatsvr.db"))
	if err != nil {
		return nil, err
	}
	if err := migrateLocal(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func migrateLocal(db *sql.DB) error {
	_, err := db.Exec(`
create table if not exists sessions (
  id text primary key,
  title text not null default '',
  owner_device_id text not null,
  sync_state text not null default 'local',
  server_id text not null default '',
  created_at integer not null,
  updated_at integer not null
);
create table if not exists turns (
  id text primary key,
  session_id text not null references sessions(id) on delete cascade,
  parent_turn_id text not null,
  q_json text not null,
  created_at integer not null
);
create index if not exists turns_session on turns(session_id);
create table if not exists answers (
  turn_id text primary key references turns(id) on delete cascade,
  a_json text not null,
  sync_state text not null default 'local',
  created_at integer not null
);
create table if not exists selections (
  session_id text not null,
  parent_turn_id text not null,
  child_turn_id text not null,
  primary key (session_id, parent_turn_id)
);
create table if not exists kv (
  k text primary key,
  v text not null
);
create table if not exists attachments (
  id text primary key,
  session_id text not null default '',
  name text not null,
  mime text not null,
  size integer not null,
  path text not null,
  is_image integer not null default 0,
  file_id text not null default '',
  file_uploaded_at integer not null default 0,
  created_at integer not null
);
create table if not exists local_output (
  turn_id text primary key,
  session_id text not null,
  blocks_json text not null,
  reason text not null,
  created_at integer not null
);
create table if not exists drafts (
  session_id text primary key,
  text text not null,
  updated_at integer not null
);
`)
	if err != nil {
		return err
	}
	// 旧库补列；已存在时忽略错误。
	_, _ = db.Exec(`alter table attachments add column file_uploaded_at integer not null default 0`)
	_, _ = db.Exec(`alter table sessions add column server_id text not null default ''`)
	return nil
}

func (s *Store) KV(k string) (string, error) {
	var v string
	err := s.db.QueryRow(`select v from kv where k=?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetKV(k, v string) error {
	_, err := s.db.Exec(`insert into kv(k,v) values(?,?) on conflict(k) do update set v=excluded.v`, k, v)
	return err
}

func (s *Store) CreateSession(x model.Session) error {
	_, err := s.db.Exec(`insert into sessions(id,title,owner_device_id,sync_state,server_id,created_at,updated_at) values(?,?,?,?,?,?,?)`,
		x.ID, x.Title, x.OwnerID, x.SyncState, x.ServerID, x.CreatedAt, x.UpdatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return apierr.New(409, "SESSION_EXISTS", "session already exists")
	}
	return err
}

func (s *Store) Session(id string) (*model.Session, error) {
	var x model.Session
	err := s.db.QueryRow(`select id,title,owner_device_id,sync_state,server_id,created_at,updated_at from sessions where id=?`, id).
		Scan(&x.ID, &x.Title, &x.OwnerID, &x.SyncState, &x.ServerID, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apierr.New(404, "SESSION_NOT_FOUND", "session not found")
	}
	if err != nil {
		return nil, err
	}
	return &x, nil
}

func (s *Store) Sessions() ([]model.Session, error) {
	rows, err := s.db.Query(`select id,title,owner_device_id,sync_state,server_id,created_at,updated_at from sessions order by updated_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Session{}
	for rows.Next() {
		var x model.Session
		if err := rows.Scan(&x.ID, &x.Title, &x.OwnerID, &x.SyncState, &x.ServerID, &x.CreatedAt, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) SetSessionTitle(id, title string) error {
	_, err := s.db.Exec(`update sessions set title=?, updated_at=? where id=?`, title, model.NowMS(), id)
	return err
}

func (s *Store) SetSessionTitleIfEmpty(id, title string) error {
	_, err := s.db.Exec(`update sessions set title=?, updated_at=? where id=? and title=''`, title, model.NowMS(), id)
	return err
}

func (s *Store) TouchSession(id string) error {
	_, err := s.db.Exec(`update sessions set updated_at=? where id=?`, model.NowMS(), id)
	return err
}

func (s *Store) SetSessionOwner(id, owner string) error {
	_, err := s.db.Exec(`update sessions set owner_device_id=?, updated_at=? where id=?`, owner, model.NowMS(), id)
	return err
}

func (s *Store) SetSessionSyncState(id, state string) error {
	_, err := s.db.Exec(`update sessions set sync_state=? where id=?`, state, id)
	return err
}

// SetSessionSynced marks a session as belonging to a specific sessionsvr.
func (s *Store) SetSessionSynced(id, serverID string) error {
	_, err := s.db.Exec(`update sessions set sync_state='synced', server_id=? where id=?`, serverID, id)
	return err
}

// DetachSyncedSessions turns all cloud-backed sessions into local sessions.
// Used when the user switches to a different sessionsvr: the old server's data
// must never be reconciled against or deleted by the new server's session list.
func (s *Store) DetachSyncedSessions(owner string) error {
	_, err := s.db.Exec(`update sessions set sync_state='local', owner_device_id=?, server_id='' where sync_state='synced'`, owner)
	return err
}

func (s *Store) DeleteSession(id string) error {
	_, err := s.db.Exec(`delete from sessions where id=?`, id)
	return err
}

// DeleteSessionWithAttachments physically deletes a local session and returns
// the attachment files that should be removed from disk.
func (s *Store) DeleteSessionWithAttachments(id string) ([]string, error) {
	rows, err := s.db.Query(`select path from attachments where session_id=? and path<>''`, id)
	if err != nil {
		return nil, err
	}
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return nil, err
		}
		paths = append(paths, path)
	}
	rows.Close()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`delete from attachments where session_id=?`,
		`delete from local_output where session_id=?`,
		`delete from drafts where session_id=?`,
		`delete from selections where session_id=?`,
		`delete from answers where turn_id in (select id from turns where session_id=?)`,
		`delete from turns where session_id=?`,
		`delete from sessions where id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return paths, nil
}

func (s *Store) UpsertTurn(t model.Turn) error {
	qJSON, err := json.Marshal(t.Q)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`insert into turns(id,session_id,parent_turn_id,q_json,created_at) values(?,?,?,?,?)
on conflict(id) do update set q_json=excluded.q_json`, t.ID, t.SessionID, t.ParentTurnID, string(qJSON), t.CreatedAt)
	return err
}

func (s *Store) Turns(sessionID string) ([]model.Turn, error) {
	rows, err := s.db.Query(`
select t.id, t.session_id, t.parent_turn_id, t.q_json, t.created_at, a.a_json, a.sync_state
from turns t left join answers a on a.turn_id=t.id
where t.session_id=? order by t.created_at asc, t.id asc`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Turn{}
	for rows.Next() {
		var t model.Turn
		var qJSON string
		var aJSON, syncState sql.NullString
		if err := rows.Scan(&t.ID, &t.SessionID, &t.ParentTurnID, &qJSON, &t.CreatedAt, &aJSON, &syncState); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(qJSON), &t.Q); err != nil {
			return nil, fmt.Errorf("decode q %s: %w", t.ID, err)
		}
		if aJSON.Valid {
			var a model.Answer
			if err := json.Unmarshal([]byte(aJSON.String), &a); err != nil {
				return nil, fmt.Errorf("decode answer %s: %w", t.ID, err)
			}
			t.Answer = &a
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Turn(id string) (*model.Turn, error) {
	var t model.Turn
	var qJSON string
	var aJSON sql.NullString
	err := s.db.QueryRow(`select t.id,t.session_id,t.parent_turn_id,t.q_json,t.created_at,a.a_json
from turns t left join answers a on a.turn_id=t.id where t.id=?`, id).
		Scan(&t.ID, &t.SessionID, &t.ParentTurnID, &qJSON, &t.CreatedAt, &aJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apierr.New(404, "TURN_NOT_FOUND", "turn not found")
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(qJSON), &t.Q); err != nil {
		return nil, err
	}
	if aJSON.Valid {
		var a model.Answer
		if err := json.Unmarshal([]byte(aJSON.String), &a); err != nil {
			return nil, err
		}
		t.Answer = &a
	}
	return &t, nil
}

func (s *Store) PutAnswer(turnID string, a model.Answer, syncState string) error {
	aJSON, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`insert into answers(turn_id,a_json,sync_state,created_at) values(?,?,?,?)
on conflict(turn_id) do update set a_json=excluded.a_json, sync_state=excluded.sync_state`, turnID, string(aJSON), syncState, a.CreatedAt)
	return err
}

func (s *Store) HasPending(sessionID string) (bool, error) {
	var n int
	err := s.db.QueryRow(`
select exists(select 1 from answers a join turns t on t.id=a.turn_id
  where t.session_id=? and a.sync_state<>'synced')`, sessionID).Scan(&n)
	return n == 1, err
}

func (s *Store) AnswerState(turnID string) (string, bool, error) {
	var state string
	err := s.db.QueryRow(`select sync_state from answers where turn_id=?`, turnID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return state, err == nil, err
}

func (s *Store) SetAnswerSync(turnID, state string) error {
	_, err := s.db.Exec(`update answers set sync_state=? where turn_id=?`, state, turnID)
	return err
}

func (s *Store) DeleteTurn(id string) error {
	_, err := s.db.Exec(`delete from turns where id=?`, id)
	return err
}

func (s *Store) DeleteSelection(sessionID, parent string) error {
	_, err := s.db.Exec(`delete from selections where session_id=? and parent_turn_id=?`, sessionID, parent)
	return err
}

func (s *Store) SetSelection(sessionID, parent, child string) error {
	_, err := s.db.Exec(`insert into selections(session_id,parent_turn_id,child_turn_id) values(?,?,?)
on conflict(session_id,parent_turn_id) do update set child_turn_id=excluded.child_turn_id`, sessionID, parent, child)
	return err
}

func (s *Store) Selections(sessionID string) (map[string]string, error) {
	rows, err := s.db.Query(`select parent_turn_id, child_turn_id from selections where session_id=?`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, c string
		if err := rows.Scan(&p, &c); err != nil {
			return nil, err
		}
		out[p] = c
	}
	return out, rows.Err()
}

func (s *Store) LocalOutput(turnID string) ([]map[string]any, string, bool) {
	var blocksJSON, reason string
	err := s.db.QueryRow(`select blocks_json, reason from local_output where turn_id=?`, turnID).Scan(&blocksJSON, &reason)
	if err != nil {
		return nil, "", false
	}
	var blocks []map[string]any
	_ = json.Unmarshal([]byte(blocksJSON), &blocks)
	return blocks, reason, true
}

func (s *Store) PutLocalOutput(turnID, sessionID string, blocks []map[string]any, reason string) error {
	b, _ := json.Marshal(blocks)
	_, err := s.db.Exec(`insert into local_output(turn_id,session_id,blocks_json,reason,created_at) values(?,?,?,?,?)
on conflict(turn_id) do update set blocks_json=excluded.blocks_json, reason=excluded.reason`,
		turnID, sessionID, string(b), reason, model.NowMS())
	return err
}

func (s *Store) LocalOutputs(sessionID string) (map[string][]map[string]any, error) {
	rows, err := s.db.Query(`select turn_id, blocks_json from local_output where session_id=?`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]map[string]any{}
	for rows.Next() {
		var id, b string
		if err := rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		var blocks []map[string]any
		_ = json.Unmarshal([]byte(b), &blocks)
		out[id] = blocks
	}
	return out, rows.Err()
}

func (s *Store) DeleteLocalOutput(turnID string) error {
	_, err := s.db.Exec(`delete from local_output where turn_id=?`, turnID)
	return err
}

type Attachment struct {
	ID             string
	SessionID      string
	Name           string
	MIME           string
	Size           int64
	Path           string
	IsImage        bool
	FileID         string
	FileUploadedAt int64
	CreatedAt      int64
}

func (s *Store) PutAttachment(a Attachment) error {
	_, err := s.db.Exec(`insert or replace into attachments(id,session_id,name,mime,size,path,is_image,file_id,file_uploaded_at,created_at)
values(?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.SessionID, a.Name, a.MIME, a.Size, a.Path, boolInt(a.IsImage), a.FileID, a.FileUploadedAt, a.CreatedAt)
	return err
}

func (s *Store) Attachment(id string) (*Attachment, error) {
	var a Attachment
	var isImage int
	err := s.db.QueryRow(`select id,session_id,name,mime,size,path,is_image,file_id,file_uploaded_at,created_at from attachments where id=?`, id).
		Scan(&a.ID, &a.SessionID, &a.Name, &a.MIME, &a.Size, &a.Path, &isImage, &a.FileID, &a.FileUploadedAt, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apierr.New(404, "ATTACHMENT_NOT_FOUND", "attachment not found")
	}
	a.IsImage = isImage != 0
	return &a, err
}

func (s *Store) SetAttachmentSessionID(id, sessionID string) error {
	_, err := s.db.Exec(`update attachments set session_id=? where id=?`, sessionID, id)
	return err
}

func (s *Store) SetAttachmentImageMeta(id, mimeType string, isImage bool) error {
	_, err := s.db.Exec(`update attachments set mime=?, is_image=? where id=?`, mimeType, boolInt(isImage), id)
	return err
}

func (s *Store) SetAttachmentFileID(id, fileID string, uploadedAt int64) error {
	_, err := s.db.Exec(`update attachments set file_id=?, file_uploaded_at=? where id=?`, fileID, uploadedAt, id)
	return err
}

func (s *Store) ClearAttachmentFileID(id string) error {
	_, err := s.db.Exec(`update attachments set file_id='', file_uploaded_at=0 where id=?`, id)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
